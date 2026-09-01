package hostagent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"worktracker/internal/config"
	"worktracker/internal/coreclient"
	"worktracker/internal/coreprotocol"
	"worktracker/internal/nativewatcher"
)

type steadyReader struct{}

func (steadyReader) Foreground(context.Context) (nativewatcher.Foreground, error) {
	return nativewatcher.Foreground{Executable: "Code.exe"}, nil
}
func (steadyReader) IdleDuration(context.Context) (time.Duration, error) { return 0, nil }
func (steadyReader) SessionLocked(context.Context) (bool, error)         { return false, nil }

func TestNativeLoopKeepsCadenceIndependently(t *testing.T) {
	times := make(chan time.Time, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch coreprotocol.Batch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
			http.Error(w, "bad batch", http.StatusBadRequest)
			return
		}
		if len(batch.Sessions) == 1 {
			times <- batch.Sessions[0].ObservedAt
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	w, err := nativewatcher.New(steadyReader{}, time.Minute, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{HostAcquisition: config.HostAcquisition{Mode: "shadow", NativePollSeconds: 0.05, NativeAFKThresholdSeconds: 60, ParityToleranceSeconds: 5}}
	agent := &Agent{Config: cfg, Core: coreclient.New(server.URL, "0123456789abcdef", time.Second), Native: w, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	diagnostics := newAcquisitionState("shadow", 5*time.Second, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent.startNativeLoop(ctx, "host", "shadow", diagnostics)
	observed := make([]time.Time, 0, 4)
	deadline := time.After(time.Second)
	for len(observed) < 4 {
		select {
		case at := <-times:
			observed = append(observed, at)
		case <-deadline:
			t.Fatalf("received %d native observations", len(observed))
		}
	}
	for i := 1; i < len(observed); i++ {
		gap := observed[i].Sub(observed[i-1])
		if gap < 25*time.Millisecond || gap > 100*time.Millisecond {
			t.Fatalf("native cadence gap=%s", gap)
		}
	}
}

func TestAcquisitionStateRetainsIndependentFailureHealth(t *testing.T) {
	now := time.Now().UTC()
	state := newAcquisitionState("shadow", 5*time.Second, []string{"lockapp.exe"}, nil)
	state.updateActivityWatch([]coreprotocol.WindowObservation{{Start: now, End: now, Executable: "Code.exe"}}, []coreprotocol.AFKObservation{{Start: now, End: now, Status: "not-afk"}}, nil, now)
	state.updateNative(nativewatcher.Result{
		Window:     &coreprotocol.WindowObservation{Start: now, End: now, Executable: "Code.exe", Source: coreprotocol.SourceNativeWindows},
		AFK:        &coreprotocol.AFKObservation{Start: now, End: now, Status: "not-afk", Source: coreprotocol.SourceNativeWindows},
		Session:    &coreprotocol.SessionObservation{ObservedAt: now, Source: coreprotocol.SourceNativeWindows},
		Foreground: nativewatcher.ComponentResult{Connected: true, LastObservation: &now},
		Input:      nativewatcher.ComponentResult{Connected: true, LastObservation: &now},
		SessionAPI: nativewatcher.ComponentResult{Connected: true, LastObservation: &now},
	}, now)
	got := state.snapshot()
	if !got.ActivityWatch.Connected || !got.NativeForeground.Connected || got.Comparison == nil || !got.Comparison.Comparable || !got.Comparison.ForegroundMatch || !got.Comparison.AFKMatch || !got.Comparison.SessionMatch {
		t.Fatalf("healthy comparison=%#v", got)
	}
}
