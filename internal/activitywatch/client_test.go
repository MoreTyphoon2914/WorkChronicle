package activitywatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoverOverridesAndAmbiguity(t *testing.T) {
	b := map[string]Bucket{"aw-watcher-window_HOST": {ID: "aw-watcher-window_HOST", Type: "currentwindow", Hostname: "HOST"}, "aw-watcher-window-2_HOST": {ID: "aw-watcher-window-2_HOST", Type: "currentwindow", Hostname: "HOST"}}
	if got, err := Discover(b, "aw-watcher-window_HOST", "HOST", "window"); err != nil || got != "aw-watcher-window_HOST" {
		t.Fatalf("override got=%q err=%v", got, err)
	}
	if _, err := Discover(b, "", "HOST", "window"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity: %v", err)
	}
}

func TestQueryAndRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/0/query/" {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		q := strings.Join(anyStrings(body["query"]), " ")
		if !strings.Contains(q, "context = []") {
			t.Errorf("query did not tolerate absent context: %s", q)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"windows":[{"timestamp":"2026-08-17T09:00:00Z","duration":60,"data":{"app":"x.exe","title":"x"}}],"afk":[],"context":[]}]`))
	}))
	defer srv.Close()
	c := New(srv.URL, time.Second, 10*time.Millisecond)
	c.Retries = 2
	r, err := c.Query(context.Background(), "w", "a", "", "", time.Now().Add(-time.Hour), time.Now())
	if err != nil || len(r.Windows) != 1 || calls.Load() != 2 {
		t.Fatalf("result=%#v calls=%d err=%v", r, calls.Load(), err)
	}
}

func TestInsertEvents(t *testing.T) {
	var received []Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/0/buckets/browser/events" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := New(srv.URL, time.Second, time.Millisecond)
	event := Event{Timestamp: time.Now().UTC(), Data: map[string]any{"schema_version": 1}}
	if err := c.InsertEvents(context.Background(), "browser", []Event{event}); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].Data["schema_version"] != float64(1) {
		t.Fatalf("received=%#v", received)
	}
}
func anyStrings(v any) []string {
	xs, _ := v.([]any)
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
