package activitywatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"worktracker/internal/classifier"
	"worktracker/internal/model"
)

type Event struct {
	ID        int64          `json:"id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Duration  float64        `json:"duration,omitempty"`
	Data      map[string]any `json:"data"`
}

type Bucket struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Client      string    `json:"client"`
	Hostname    string    `json:"hostname"`
	LastUpdated time.Time `json:"last_updated"`
}

type Client struct {
	BaseURL    string
	HTTP       *http.Client
	Retries    int
	MaxBackoff time.Duration
}

func New(base string, timeout, maxBackoff time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: timeout}, Retries: 4, MaxBackoff: maxBackoff}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	backoff := 250 * time.Millisecond
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTP.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if out == nil {
					io.Copy(io.Discard, resp.Body)
					return nil
				}
				return json.NewDecoder(resp.Body).Decode(out)
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			err = fmt.Errorf("activitywatch %s %s: %s: %s", method, path, resp.Status, string(b))
			if resp.StatusCode < 500 && resp.StatusCode != 429 {
				return err
			}
		}
		if attempt >= c.Retries {
			return err
		}
		wait := backoff + time.Duration(rand.Int64N(int64(max(backoff/4, time.Millisecond))))
		if wait > c.MaxBackoff {
			wait = c.MaxBackoff
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		backoff *= 2
		if backoff > c.MaxBackoff {
			backoff = c.MaxBackoff
		}
	}
}

func (c *Client) Buckets(ctx context.Context) (map[string]Bucket, error) {
	var b map[string]Bucket
	err := c.do(ctx, http.MethodGet, "/api/0/buckets/", nil, &b)
	return b, err
}

func (c *Client) Latest(ctx context.Context, bucket string) (Event, error) {
	var events []Event
	path := "/api/0/buckets/" + url.PathEscape(bucket) + "/events?limit=1"
	if err := c.do(ctx, http.MethodGet, path, nil, &events); err != nil {
		return Event{}, err
	}
	if len(events) == 0 {
		return Event{}, errors.New("bucket has no events")
	}
	return events[0], nil
}

func Discover(buckets map[string]Bucket, explicit, hostname, kind string) (string, error) {
	if explicit != "" {
		if _, ok := buckets[explicit]; !ok {
			return "", fmt.Errorf("configured %s bucket %q not found", kind, explicit)
		}
		return explicit, nil
	}
	var ids []string
	for id, b := range buckets {
		low := strings.ToLower(id + " " + b.Type)
		if hostname != "" && !strings.EqualFold(b.Hostname, hostname) && !strings.Contains(strings.ToLower(id), strings.ToLower(hostname)) {
			continue
		}
		if strings.Contains(low, kind) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", fmt.Errorf("no %s bucket found for host %s", kind, hostname)
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous %s buckets: %s", kind, strings.Join(ids, ", "))
	}
	return ids[0], nil
}

func (c *Client) CreateBucket(ctx context.Context, id, eventType, hostname string) error {
	body := map[string]any{"id": id, "type": eventType, "client": "worktracker", "hostname": hostname}
	err := c.do(ctx, http.MethodPost, "/api/0/buckets/"+url.PathEscape(id), body, nil)
	if err != nil && strings.Contains(err.Error(), "409") {
		return nil
	}
	return err
}

func (c *Client) Heartbeat(ctx context.Context, bucket string, e Event, pulsetime time.Duration) error {
	path := "/api/0/buckets/" + url.PathEscape(bucket) + "/heartbeat?pulsetime=" + fmt.Sprintf("%.3f", pulsetime.Seconds())
	return c.do(ctx, http.MethodPost, path, e, nil)
}

func (c *Client) InsertEvents(ctx context.Context, bucket string, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	path := "/api/0/buckets/" + url.PathEscape(bucket) + "/events"
	return c.do(ctx, http.MethodPost, path, events, nil)
}

type QueryResult struct {
	Windows []Event `json:"windows"`
	AFK     []Event `json:"afk"`
	Context []Event `json:"context"`
	Browser []Event `json:"browser"`
}

func (c *Client) Query(ctx context.Context, windowBucket, afkBucket, contextBucket, browserBucket string, start, end time.Time) (QueryResult, error) {
	period := start.Format(time.RFC3339Nano) + "/" + end.Format(time.RFC3339Nano)
	contextQuery := `context = [];`
	if contextBucket != "" {
		contextQuery = fmt.Sprintf(`context = flood(query_bucket(find_bucket("%s")));`, escapeQuery(contextBucket))
	}
	browserQuery := `browser = [];`
	if browserBucket != "" {
		browserQuery = fmt.Sprintf(`browser = query_bucket(find_bucket("%s"));`, escapeQuery(browserBucket))
	}
	q := []string{
		fmt.Sprintf(`windows = flood(query_bucket(find_bucket("%s")));`, escapeQuery(windowBucket)),
		fmt.Sprintf(`afk = flood(query_bucket(find_bucket("%s")));`, escapeQuery(afkBucket)),
		contextQuery,
		browserQuery,
		`RETURN = {"windows": windows, "afk": afk, "context": context, "browser": browser};`,
	}
	body := map[string]any{"query": q, "timeperiods": []string{period}}
	var result []QueryResult
	if err := c.do(ctx, http.MethodPost, "/api/0/query/", body, &result); err != nil {
		return QueryResult{}, err
	}
	if len(result) == 0 {
		return QueryResult{}, errors.New("activitywatch query returned no result")
	}
	return result[0], nil
}

func Normalize(q QueryResult) ([]model.WindowEvent, []model.AFKEvent, []model.ContextEvent) {
	ws := make([]model.WindowEvent, 0, len(q.Windows))
	for _, e := range q.Windows {
		ws = append(ws, model.WindowEvent{Start: e.Timestamp, End: e.Timestamp.Add(time.Duration(e.Duration * float64(time.Second))), App: stringValue(e.Data["app"]), Title: stringValue(e.Data["title"])})
	}
	as := make([]model.AFKEvent, 0, len(q.AFK))
	for _, e := range q.AFK {
		as = append(as, model.AFKEvent{Start: e.Timestamp, End: e.Timestamp.Add(time.Duration(e.Duration * float64(time.Second))), Status: stringValue(e.Data["status"])})
	}
	cs := make([]model.ContextEvent, 0, len(q.Context))
	for _, e := range q.Context {
		cs = append(cs, classifier.ParseContext(e.Timestamp, e.Timestamp.Add(time.Duration(e.Duration*float64(time.Second))), e.Data))
	}
	return ws, as, cs
}

func escapeQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}
func stringValue(v any) string { s, _ := v.(string); return s }
