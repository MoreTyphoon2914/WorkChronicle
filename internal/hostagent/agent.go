package hostagent

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
	"worktracker/internal/coreclient"
	"worktracker/internal/coreprotocol"
	"worktracker/internal/location"
)

type Buckets struct{ Window, AFK, Context, Browser string }

type Agent struct {
	Config   config.Config
	Core     *coreclient.Client
	AW       *activitywatch.Client
	Location *location.Detector
	Apps     []appstate.Detector
	Logger   *slog.Logger
}

func New(cfg config.Config, client *coreclient.Client, logger *slog.Logger) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	return &Agent{
		Config: cfg, Core: client,
		AW:       activitywatch.New(cfg.Server, cfg.HTTPTimeout(), time.Duration(cfg.RetryMaxSeconds*float64(time.Second))),
		Location: location.New(location.PowerShellRunner{}, cfg.OfficeGatewayMACs, cfg.HomeGatewayMACs, cfg.NetworkStale()),
		Apps:     []appstate.Detector{&appstate.VLCDetector{URL: cfg.VLC.URL, Password: cfg.VLC.Password, Client: &http.Client{Timeout: cfg.HTTPTimeout()}}},
		Logger:   logger,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	if a.Core == nil {
		return fmt.Errorf("Core client is required")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	buckets, err := a.resolveBuckets(ctx, hostname)
	if err != nil {
		return fmt.Errorf("resolve ActivityWatch buckets: %w", err)
	}
	browserBucket, err := a.ensureBrowserBucket(ctx, hostname, buckets.Browser)
	if err != nil {
		return err
	}
	buckets.Browser = browserBucket

	browserErrors, shutdownBrowser, err := a.startBrowserReceiver(ctx, hostname, browserBucket)
	if err != nil {
		return err
	}
	defer shutdownBrowser()

	now := time.Now().UTC()
	locationResult, locationErr := a.Location.Observe(ctx, now)
	if locationErr != nil {
		a.Logger.Warn("network acquisition degraded")
	}
	dayStart, _, _ := a.Config.ReportingPeriod(now)
	weekday := (int(dayStart.Weekday()) + 6) % 7
	queryStart := dayStart.AddDate(0, 0, -weekday)
	if err := a.acquireAndSend(ctx, hostname, buckets, queryStart, now, locationResult); err != nil {
		a.Logger.Warn("initial observation forwarding failed", "error", err)
	}
	lastQuery := now

	poll := time.NewTicker(a.Config.PollInterval())
	defer poll.Stop()
	networkTick := time.NewTicker(a.Config.NetworkRefresh())
	defer networkTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-browserErrors:
			if err != nil {
				a.Logger.Error("browser receiver stopped", "error", err)
			}
			browserErrors = nil
		case tick := <-networkTick.C:
			locationResult, locationErr = a.Location.Observe(ctx, tick.UTC())
			if locationErr != nil {
				a.Logger.Warn("network acquisition degraded")
			}
		case tick := <-poll.C:
			lookback := max(2*a.Config.StatusStale(), time.Minute)
			start := lastQuery.Add(-lookback)
			if err := a.acquireAndSend(ctx, hostname, buckets, start, tick.UTC(), locationResult); err != nil {
				a.Logger.Warn("observation forwarding failed", "error", err)
			} else {
				lastQuery = tick.UTC()
			}
		}
	}
}

func (a *Agent) acquireAndSend(ctx context.Context, hostname string, buckets Buckets, start, end time.Time, network location.Result) error {
	query, err := a.AW.Query(ctx, buckets.Window, buckets.AFK, buckets.Context, buckets.Browser, start, end)
	if err != nil {
		return fmt.Errorf("query ActivityWatch: %w", err)
	}
	batch := coreprotocol.Batch{SchemaVersion: coreprotocol.SchemaVersion, AgentID: hostname, SentAt: end}
	for _, event := range query.Windows {
		executable, _ := event.Data["app"].(string)
		if strings.TrimSpace(executable) == "" {
			continue
		}
		title, _ := event.Data["title"].(string)
		batch.Windows = append(batch.Windows, coreprotocol.WindowObservation{Start: event.Timestamp, End: eventEnd(event), Executable: executable, Title: title})
	}
	for _, event := range query.AFK {
		status, _ := event.Data["status"].(string)
		if strings.TrimSpace(status) == "" {
			continue
		}
		batch.AFK = append(batch.AFK, coreprotocol.AFKObservation{Start: event.Timestamp, End: eventEnd(event), Status: status})
	}
	for _, event := range query.Context {
		batch.StoredContext = append(batch.StoredContext, coreprotocol.StoredContextObservation{Start: event.Timestamp, End: eventEnd(event), Data: event.Data})
	}
	for _, event := range query.Browser {
		observation, err := browsercontext.DecodeStored(event.Data)
		if err == nil {
			batch.Browser = append(batch.Browser, observation)
		}
	}

	apps := make(map[string]coreprotocol.AppObservation, len(a.Apps))
	for _, detector := range a.Apps {
		observation, observeErr := detector.Observe(ctx)
		if observeErr != nil {
			observation = appstate.Observation{Detector: detector.ID(), State: "unavailable", Available: false, ObservedAt: end}
			a.Logger.Warn("app-state acquisition degraded", "source", detector.ID())
		}
		apps[detector.ID()] = coreprotocol.AppObservation{SourceID: detector.ID(), State: observation.State, Available: observation.Available, ObservedAt: observation.ObservedAt}
	}
	batch.HostContext = []coreprotocol.HostContextObservation{{Start: end, End: end, Location: network.Location, LocationEvidence: network.Evidence, Health: network.Health, Apps: apps}}
	if err := batch.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("normalize observations: %w", err)
	}
	return a.Core.Send(ctx, batch)
}

func (a *Agent) resolveBuckets(ctx context.Context, hostname string) (Buckets, error) {
	all, err := a.AW.Buckets(ctx)
	if err != nil {
		return Buckets{}, err
	}
	window, err := activitywatch.Discover(all, a.Config.WindowBucket, hostname, "window")
	if err != nil {
		return Buckets{}, err
	}
	afk, err := activitywatch.Discover(all, a.Config.AFKBucket, hostname, "afk")
	if err != nil {
		return Buckets{}, err
	}
	return Buckets{
		Window: window, AFK: afk,
		Context: existingBucket(all, "aw-watcher-work-context_"+hostname),
		Browser: existingBucket(all, "aw-watcher-browser-context_"+hostname),
	}, nil
}

func existingBucket(all map[string]activitywatch.Bucket, expected string) string {
	for id := range all {
		if strings.EqualFold(id, expected) {
			return id
		}
	}
	return ""
}

func (a *Agent) ensureBrowserBucket(ctx context.Context, hostname, existing string) (string, error) {
	if existing != "" {
		return existing, nil
	}
	id := "aw-watcher-browser-context_" + hostname
	if err := a.AW.CreateBucket(ctx, id, "browsercontext", hostname); err != nil {
		return "", fmt.Errorf("create browser compatibility bucket: %w", err)
	}
	return id, nil
}

func (a *Agent) startBrowserReceiver(ctx context.Context, hostname, bucket string) (<-chan error, func(), error) {
	listener, err := browsercontext.ListenLoopback(a.Config.BrowserIngest.Port)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for browser context: %w", err)
	}
	store := browserForwardStore{core: a.Core, aw: a.AW, bucket: bucket, agentID: hostname, logger: a.Logger}
	server := browsercontext.NewServer(store, a.Config.BrowserIngest.MaxBodyBytes)
	errors := make(chan error, 1)
	go func() { errors <- server.Serve(listener) }()
	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
	a.Logger.Info("browser observations forwarding to Core", "address", listener.Addr().String())
	return errors, shutdown, nil
}

type browserForwardStore struct {
	core    *coreclient.Client
	aw      *activitywatch.Client
	bucket  string
	agentID string
	logger  *slog.Logger
}

func (s browserForwardStore) Save(ctx context.Context, observation browsercontext.Observation) error {
	batch := coreprotocol.Batch{SchemaVersion: coreprotocol.SchemaVersion, AgentID: s.agentID, SentAt: time.Now().UTC(), Browser: []browsercontext.Observation{observation}}
	if err := s.core.Send(ctx, batch); err != nil {
		return err
	}
	b, _ := json.Marshal(observation)
	var data map[string]any
	_ = json.Unmarshal(b, &data)
	if err := s.aw.InsertEvents(ctx, s.bucket, []activitywatch.Event{{Timestamp: observation.ObservedAt.UTC(), Data: data}}); err != nil {
		s.logger.Warn("ActivityWatch browser compatibility mirror failed")
	}
	return nil
}

func eventEnd(event activitywatch.Event) time.Time {
	return event.Timestamp.Add(time.Duration(event.Duration * float64(time.Second)))
}
