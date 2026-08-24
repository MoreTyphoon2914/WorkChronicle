package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"worktracker/internal/activitywatch"
	"worktracker/internal/appstate"
	"worktracker/internal/browsercontext"
	"worktracker/internal/config"
	"worktracker/internal/location"
	"worktracker/internal/model"
)

type Collector struct {
	Config   config.Config
	AW       *activitywatch.Client
	Apps     *appstate.Registry
	Location *location.Detector
	Logger   *slog.Logger
	queue    []activitywatch.Event
}

func NewCollector(cfg config.Config, logger *slog.Logger) *Collector {
	aw := activitywatch.New(cfg.Server, cfg.HTTPTimeout(), time.Duration(cfg.RetryMaxSeconds*float64(time.Second)))
	vlc := &appstate.VLCDetector{URL: cfg.VLC.URL, Password: cfg.VLC.Password, Client: &http.Client{Timeout: cfg.HTTPTimeout()}}
	return &Collector{Config: cfg, AW: aw, Apps: appstate.New(appstate.NewSource(vlc, appstate.PassiveWhen("playing"))), Location: location.New(location.PowerShellRunner{}, cfg.OfficeGatewayMACs, cfg.HomeGatewayMACs, cfg.NetworkStale()), Logger: logger}
}

func (c *Collector) Run(ctx context.Context) error {
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	bucket := "aw-watcher-work-context_" + host
	buckets, err := c.AW.Buckets(ctx)
	if err != nil {
		return fmt.Errorf("list ActivityWatch buckets: %w", err)
	}
	_, exists := buckets[bucket]
	if !exists {
		for id := range buckets {
			if strings.EqualFold(id, bucket) {
				bucket = id
				exists = true
				break
			}
		}
	}
	if !exists {
		if err := c.AW.CreateBucket(ctx, bucket, "workcontext", host); err != nil {
			return fmt.Errorf("create context bucket: %w", err)
		}
	}
	var browserErrors <-chan error
	if c.Config.BrowserIngest.Enabled {
		browserBucket := "aw-watcher-browser-context_" + host
		browserExists := false
		for id := range buckets {
			if strings.EqualFold(id, browserBucket) {
				browserBucket = id
				browserExists = true
				break
			}
		}
		if !browserExists {
			if err := c.AW.CreateBucket(ctx, browserBucket, "browsercontext", host); err != nil {
				return fmt.Errorf("create browser context bucket: %w", err)
			}
		}
		listener, err := browsercontext.ListenLoopback(c.Config.BrowserIngest.Port)
		if err != nil {
			return fmt.Errorf("listen for browser context on 127.0.0.1:%d: %w", c.Config.BrowserIngest.Port, err)
		}
		browserServer := browsercontext.NewServer(browserStore{aw: c.AW, bucket: browserBucket}, c.Config.BrowserIngest.MaxBodyBytes)
		errCh := make(chan error, 1)
		browserErrors = errCh
		go func() { errCh <- browserServer.Serve(listener) }()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = browserServer.Shutdown(shutdownCtx)
		}()
		c.Logger.Info("browser context ingestion listening", "address", listener.Addr().String())
	}
	poll := time.NewTicker(c.Config.PollInterval())
	defer poll.Stop()
	networkTick := time.NewTicker(c.Config.NetworkRefresh())
	defer networkTick.Stop()
	loc, locErr := c.Location.Observe(ctx, time.Now().UTC())
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case serverErr := <-browserErrors:
			if serverErr != nil {
				return fmt.Errorf("browser context server stopped: %w", serverErr)
			}
			browserErrors = nil
		case <-networkTick.C:
			loc, locErr = c.Location.Observe(ctx, time.Now().UTC())
			if locErr != nil {
				c.Logger.Warn("network detection degraded")
			}
		case now := <-poll.C:
			snapshot := c.Apps.Observe(ctx)
			for id, detectorErr := range snapshot.Failures {
				c.Logger.Warn("passive-work detector degraded", "detector", id, "error", detectorErr)
			}
			data := map[string]any{"schema_version": 1, "location": string(loc.Location), "location_evidence": string(loc.Evidence), "network_health": string(loc.Health), "app_states": appData(snapshot.Evidence)}
			e := activitywatch.Event{Timestamp: now.UTC(), Data: data}
			c.flush(ctx, bucket)
			if err := c.AW.Heartbeat(ctx, bucket, e, c.Config.PollInterval()+2*time.Second); err != nil {
				c.enqueue(e)
				c.Logger.Warn("context heartbeat queued", "error", err, "queued", len(c.queue))
			}
		}
	}
}

type browserStore struct {
	aw     *activitywatch.Client
	bucket string
}

func (s browserStore) Save(ctx context.Context, observation browsercontext.Observation) error {
	b, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	event := activitywatch.Event{Timestamp: observation.ObservedAt.UTC(), Data: data}
	return s.aw.InsertEvents(ctx, s.bucket, []activitywatch.Event{event})
}
func (c *Collector) enqueue(e activitywatch.Event) {
	if len(c.queue) >= c.Config.ContextQueueSize {
		c.queue = c.queue[1:]
		c.Logger.Error("context queue overflow; oldest observation lost")
	}
	c.queue = append(c.queue, e)
}
func (c *Collector) flush(ctx context.Context, bucket string) {
	for len(c.queue) > 0 {
		if err := c.AW.Heartbeat(ctx, bucket, c.queue[0], c.Config.PollInterval()+2*time.Second); err != nil {
			return
		}
		c.queue = c.queue[1:]
	}
}
func appData(apps map[string]model.PassiveEvidence) map[string]any {
	out := map[string]any{}
	for id, o := range apps {
		out[id] = map[string]any{"state": o.State, "available": o.Available, "passive_work": o.PassiveWork}
	}
	return out
}
