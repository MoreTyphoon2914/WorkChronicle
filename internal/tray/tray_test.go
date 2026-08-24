package tray

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"worktracker/internal/model"
	"worktracker/internal/workpolicy"
)

func TestStatusMappingToTrayStates(t *testing.T) {
	for _, test := range []struct {
		state model.WorkState
		icon  IconState
	}{{model.Working, IconWorking}, {model.Break, IconBreak}, {model.Untracked, IconUntracked}, {"", IconUntracked}} {
		view := MapStatus(model.StatusReport{WorkState: test.state, TrackerHealth: model.Healthy})
		if view.Icon != test.icon {
			t.Fatalf("state %q mapped to %q, want %q", test.state, view.Icon, test.icon)
		}
	}
}

func TestStatusMappingIncludesPresentationFields(t *testing.T) {
	status := model.StatusReport{
		WorkState: model.Working, Location: model.Remote, LocationEvidence: model.Confirmed,
		Foreground: model.ForegroundStatus{Executable: "firefox.exe", Title: "Documentation"},
		PassiveDetectorEvidence: map[string]model.PassiveEvidence{
			"vlc":               {Detector: "vlc", State: "paused", Available: true},
			"browser:firefox:7": {Detector: "browser", State: "playing", Available: true, PassiveWork: true},
		},
		TrackerHealth:    model.Healthy,
		Totals:           model.Totals{WorkingSeconds: 7 * 3600},
		WorkEvaluation:   workpolicy.Evaluation{Band: workpolicy.Standard},
		RemainingSeconds: 3600,
	}
	view := MapStatus(status)
	if view.Location != "REMOTE (confirmed)" || view.Working != "7h 00m" || view.Remaining != "1h 00m" {
		t.Fatalf("unexpected status mapping: %#v", view)
	}
	if view.Evaluation != "STANDARD" || !strings.Contains(view.Foreground, "firefox.exe") {
		t.Fatalf("evaluation/foreground missing: %#v", view)
	}
	if !strings.Contains(view.PassiveEvidence, "browser:firefox:7: playing") || !strings.Contains(view.PassiveEvidence, "vlc: paused") {
		t.Fatalf("passive summary missing independent evidence: %q", view.PassiveEvidence)
	}
}

func TestMissingAndDegradedHealthDisplay(t *testing.T) {
	missing := MapStatus(model.StatusReport{WorkState: model.Untracked})
	if missing.Health != "degraded (health evidence unavailable)" {
		t.Fatalf("missing health rendered as %q", missing.Health)
	}
	degraded := MapStatus(model.StatusReport{WorkState: model.Working, TrackerHealth: model.Degraded})
	if degraded.Health != "degraded" {
		t.Fatalf("degraded health rendered as %q", degraded.Health)
	}
}

type fakeUI struct {
	updates chan ViewModel
	run     func(context.Context, func()) error
}

func (u *fakeUI) Update(view ViewModel)                      { u.updates <- view }
func (u *fakeUI) Run(ctx context.Context, exit func()) error { return u.run(ctx, exit) }

func TestProviderFailureProducesFailedUntrackedView(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ui := &fakeUI{updates: make(chan ViewModel, 1)}
	ui.run = func(ctx context.Context, _ func()) error {
		view := <-ui.updates
		if view.Icon != IconUntracked || view.Health != "failed" || !strings.Contains(view.ProviderError, "offline") {
			t.Errorf("unexpected failure view: %#v", view)
		}
		cancel()
		<-ctx.Done()
		return nil
	}
	provider := ProviderFunc(func(context.Context) (model.StatusReport, error) {
		return model.StatusReport{}, errors.New("reporting provider offline")
	})
	if err := (Controller{Provider: provider, UI: ui, Interval: time.Hour}).Run(ctx, cancel); err != nil {
		t.Fatal(err)
	}
}

func TestControllerCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ui := &fakeUI{updates: make(chan ViewModel, 1)}
	var once sync.Once
	ui.run = func(ctx context.Context, requestExit func()) error {
		<-ui.updates
		once.Do(requestExit)
		<-ctx.Done()
		return nil
	}
	provider := ProviderFunc(func(context.Context) (model.StatusReport, error) {
		return model.StatusReport{WorkState: model.Working, TrackerHealth: model.Healthy}, nil
	})
	done := make(chan error, 1)
	go func() { done <- (Controller{Provider: provider, UI: ui, Interval: time.Millisecond}).Run(ctx, cancel) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("tray controller did not shut down after Exit")
	}
}
