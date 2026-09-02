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
	"worktracker/internal/nativewatcher"
)

type Buckets struct{ Window, AFK, Context, Browser string }

type Agent struct {
	Config   config.Config
	Core     *coreclient.Client
	AW       *activitywatch.Client
	Native   *nativewatcher.Watcher
	Location *location.Detector
	Apps     []appstate.Detector
	Logger   *slog.Logger
}

func New(cfg config.Config, client *coreclient.Client, logger *slog.Logger) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	native, _ := nativewatcher.New(nativewatcher.WindowsReader{}, cfg.NativeAFKThreshold(), max(3*cfg.NativePollInterval(), 10*time.Second))
	return &Agent{
		Config: cfg, Core: client,
		AW:       activitywatch.New(cfg.Server, cfg.HTTPTimeout(), time.Duration(cfg.RetryMaxSeconds*float64(time.Second))),
		Native:   native,
		Location: location.New(location.PowerShellRunner{}, cfg.OfficeGatewayMACs, cfg.HomeGatewayMACs, cfg.NetworkStale()),
		Apps:     []appstate.Detector{&appstate.VLCDetector{URL: cfg.VLC.URL, Password: cfg.VLC.Password, Client: &http.Client{Timeout: cfg.HTTPTimeout()}}},
		Logger:   logger,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	if a.Core == nil {
		return fmt.Errorf("Core client is required")
	}
	mode := a.Config.AcquisitionMode()
	if (mode == "shadow" || mode == "native") && a.Native == nil {
		return fmt.Errorf("native Windows acquisition is required in %s mode", mode)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	diagnostics := newAcquisitionState(mode, a.Config.ParityTolerance(), a.Config.NativeAFKThreshold(), a.Config.LockApps, a.Config.LockTitleContains)

	var buckets Buckets
	if usesActivityWatch(mode) {
		buckets, err = a.resolveBuckets(ctx, hostname)
		if err != nil {
			a.Logger.Warn("ActivityWatch discovery degraded", "error", err)
		} else if browserBucket, bucketErr := a.ensureBrowserBucket(ctx, hostname, buckets.Browser); bucketErr != nil {
			a.Logger.Warn("ActivityWatch browser compatibility unavailable", "error", bucketErr)
		} else {
			buckets.Browser = browserBucket
		}
	}

	browserErrors, shutdownBrowser, err := a.startBrowserReceiver(ctx, hostname, buckets.Browser, usesActivityWatch(mode))
	if err != nil {
		return err
	}
	defer shutdownBrowser()
	if mode == "shadow" || mode == "native" {
		a.startNativeLoop(ctx, hostname, mode, diagnostics)
	}

	now := time.Now().UTC()
	locationResult, locationErr := a.Location.Observe(ctx, now)
	if locationErr != nil {
		a.Logger.Warn("network acquisition degraded")
	}
	dayStart, _, _ := a.Config.ReportingPeriod(now)
	weekday := (int(dayStart.Weekday()) + 6) % 7
	queryStart := dayStart.AddDate(0, 0, -weekday)
	if sendErr := a.acquireAndSend(ctx, hostname, &buckets, queryStart, now, locationResult, diagnostics); sendErr != nil {
		a.Logger.Warn("initial observation forwarding failed", "error", sendErr)
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
		case browserErr := <-browserErrors:
			if browserErr != nil {
				a.Logger.Error("browser receiver stopped", "error", browserErr)
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
			if sendErr := a.acquireAndSend(ctx, hostname, &buckets, start, tick.UTC(), locationResult, diagnostics); sendErr != nil {
				a.Logger.Warn("observation forwarding failed", "error", sendErr)
			} else {
				lastQuery = tick.UTC()
			}
		}
	}
}

func usesActivityWatch(mode string) bool { return mode == "activitywatch" || mode == "shadow" }

func (a *Agent) acquireAndSend(ctx context.Context, hostname string, buckets *Buckets, start, end time.Time, network location.Result, diagnostics *acquisitionState) error {
	mode := a.Config.AcquisitionMode()
	batch := coreprotocol.Batch{SchemaVersion: coreprotocol.SchemaVersion, AgentID: hostname, SentAt: end}

	var awErr error
	if usesActivityWatch(mode) {
		if buckets.Window == "" || buckets.AFK == "" {
			*buckets, awErr = a.resolveBuckets(ctx, hostname)
		}
		if awErr == nil {
			awErr = a.appendActivityWatch(ctx, &batch, *buckets, start, end)
		}
		if awErr != nil {
			diagnostics.updateActivityWatch(nil, nil, awErr, end)
		} else {
			diagnostics.updateActivityWatch(batch.Windows, batch.AFK, nil, end)
		}
	}
	batch.Acquisition = diagnostics.snapshot()
	a.appendHostContext(ctx, &batch, end, network)
	if err := batch.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("normalize observations: %w", err)
	}
	if err := a.Core.Send(ctx, batch); err != nil {
		return err
	}
	if awErr != nil {
		a.Logger.Warn("ActivityWatch acquisition degraded", "error", awErr)
	}
	return nil
}

func (a *Agent) startNativeLoop(ctx context.Context, hostname, mode string, diagnostics *acquisitionState) {
	go func() {
		sample := func(at time.Time) {
			result := a.Native.Observe(ctx, at.UTC())
			diagnostics.updateNative(result, at.UTC())
			batch := coreprotocol.Batch{SchemaVersion: coreprotocol.SchemaVersion, AgentID: hostname, SentAt: at.UTC(), Acquisition: diagnostics.snapshot()}
			if result.Session != nil {
				batch.Sessions = append(batch.Sessions, *result.Session)
			}
			if mode == "native" {
				if result.Window != nil {
					batch.Windows = append(batch.Windows, *result.Window)
				}
				if result.AFK != nil {
					batch.AFK = append(batch.AFK, *result.AFK)
				}
			} else {
				if result.Window != nil {
					batch.ShadowWindows = append(batch.ShadowWindows, *result.Window)
				}
				if result.AFK != nil {
					batch.ShadowAFK = append(batch.ShadowAFK, *result.AFK)
				}
			}
			if err := batch.NormalizeAndValidate(); err != nil {
				a.Logger.Error("native observation normalization failed", "error", err)
				return
			}
			if err := a.Core.Send(ctx, batch); err != nil && ctx.Err() == nil {
				a.Logger.Warn("native observation forwarding failed", "error", err)
			}
		}
		sample(time.Now())
		ticker := time.NewTicker(a.Config.NativePollInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				sample(at)
			}
		}
	}()
}

func (a *Agent) appendActivityWatch(ctx context.Context, batch *coreprotocol.Batch, buckets Buckets, start, end time.Time) error {
	query, err := a.AW.Query(ctx, buckets.Window, buckets.AFK, buckets.Context, buckets.Browser, start, end)
	if err != nil {
		return fmt.Errorf("query ActivityWatch: %w", err)
	}
	for _, event := range query.Windows {
		executable, _ := event.Data["app"].(string)
		if strings.TrimSpace(executable) == "" {
			continue
		}
		title, _ := event.Data["title"].(string)
		batch.Windows = append(batch.Windows, coreprotocol.WindowObservation{Start: event.Timestamp, End: eventEnd(event), Executable: executable, Title: title, Source: coreprotocol.SourceActivityWatch})
	}
	for _, event := range query.AFK {
		status, _ := event.Data["status"].(string)
		if strings.TrimSpace(status) == "" {
			continue
		}
		batch.AFK = append(batch.AFK, coreprotocol.AFKObservation{Start: event.Timestamp, End: eventEnd(event), Status: status, Source: coreprotocol.SourceActivityWatch})
	}
	for _, event := range query.Context {
		batch.StoredContext = append(batch.StoredContext, coreprotocol.StoredContextObservation{Start: event.Timestamp, End: eventEnd(event), Data: event.Data})
	}
	for _, event := range query.Browser {
		observation, decodeErr := browsercontext.DecodeStored(event.Data)
		if decodeErr == nil {
			batch.Browser = append(batch.Browser, observation)
		}
	}
	return nil
}

func (a *Agent) appendHostContext(ctx context.Context, batch *coreprotocol.Batch, end time.Time, network location.Result) {
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
}

func latestAuthoritativeObservation(windows []coreprotocol.WindowObservation, afk []coreprotocol.AFKObservation) *time.Time {
	var latest time.Time
	for _, item := range windows {
		if item.End.After(latest) {
			latest = item.End
		}
	}
	for _, item := range afk {
		if item.End.After(latest) {
			latest = item.End
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
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
	return Buckets{Window: window, AFK: afk, Context: existingBucket(all, "aw-watcher-work-context_"+hostname), Browser: existingBucket(all, "aw-watcher-browser-context_"+hostname)}, nil
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

func (a *Agent) startBrowserReceiver(ctx context.Context, hostname, bucket string, mirrorAW bool) (<-chan error, func(), error) {
	listener, err := browsercontext.ListenLoopback(a.Config.BrowserIngest.Port)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for browser context: %w", err)
	}
	store := browserForwardStore{core: a.Core, bucket: bucket, agentID: hostname, logger: a.Logger}
	if mirrorAW && bucket != "" {
		store.aw = a.AW
	}
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
	core            *coreclient.Client
	aw              *activitywatch.Client
	bucket, agentID string
	logger          *slog.Logger
}

func (s browserForwardStore) Save(ctx context.Context, observation browsercontext.Observation) error {
	batch := coreprotocol.Batch{SchemaVersion: coreprotocol.SchemaVersion, AgentID: s.agentID, SentAt: time.Now().UTC(), Browser: []browsercontext.Observation{observation}}
	if err := s.core.Send(ctx, batch); err != nil {
		return err
	}
	if s.aw != nil && s.bucket != "" {
		b, _ := json.Marshal(observation)
		var data map[string]any
		_ = json.Unmarshal(b, &data)
		if err := s.aw.InsertEvents(ctx, s.bucket, []activitywatch.Event{{Timestamp: observation.ObservedAt.UTC(), Data: data}}); err != nil {
			s.logger.Warn("ActivityWatch browser compatibility mirror failed")
		}
	}
	return nil
}

func eventEnd(event activitywatch.Event) time.Time {
	return event.Timestamp.Add(time.Duration(event.Duration * float64(time.Second)))
}
