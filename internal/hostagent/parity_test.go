package hostagent

import (
	"testing"
	"time"

	"worktracker/internal/coreprotocol"
	"worktracker/internal/nativewatcher"
)

func TestShadowParityUsesFreshNormalizedFactsWithinTolerance(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	native := nativewatcher.Result{
		Window:  &coreprotocol.WindowObservation{Start: now.Add(-time.Second), End: now, Executable: "Code.exe", Source: coreprotocol.SourceNativeWindows},
		AFK:     &coreprotocol.AFKObservation{Start: now.Add(-time.Minute), End: now, Status: "not-afk", Source: coreprotocol.SourceNativeWindows},
		Session: &coreprotocol.SessionObservation{ObservedAt: now, Locked: false, Source: coreprotocol.SourceNativeWindows},
	}
	comparison := compareSources(
		[]coreprotocol.WindowObservation{{Start: now.Add(-2 * time.Second), End: now.Add(-time.Second), Executable: "code.EXE"}},
		[]coreprotocol.AFKObservation{{Start: now.Add(-time.Hour), End: now.Add(-time.Second), Status: "active"}},
		native, nil, now, 5*time.Second, 3*time.Minute, []string{"lockapp.exe"}, []string{"windows default lock screen"},
	)
	if !comparison.Comparable || !comparison.ForegroundMatch || !comparison.AFKMatch || !comparison.SessionMatch {
		t.Fatalf("normalized sources should agree: %#v", comparison)
	}
}

func TestShadowParityRejectsStaleAndReportsLockMismatchWithoutTitles(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	native := nativewatcher.Result{
		Window:  &coreprotocol.WindowObservation{Start: now, End: now, Executable: "LockApp.exe", Locked: true},
		AFK:     &coreprotocol.AFKObservation{Start: now, End: now, Status: "afk"},
		Session: &coreprotocol.SessionObservation{ObservedAt: now, Locked: true, Source: coreprotocol.SourceNativeWindows},
	}
	stale := compareSources(
		[]coreprotocol.WindowObservation{{Start: now.Add(-10 * time.Second), End: now.Add(-10 * time.Second), Executable: "browser.exe", Title: "https://example.test/?session_id=opaque-fixture"}},
		[]coreprotocol.AFKObservation{{Start: now.Add(-10 * time.Second), End: now.Add(-10 * time.Second), Status: "afk"}},
		native, nil, now, 5*time.Second, 3*time.Minute, []string{"lockapp.exe"}, nil,
	)
	if stale.Comparable || stale.Summary != "waiting for comparable fresh observations" {
		t.Fatalf("stale observations should not compare: %#v", stale)
	}
	if usesActivityWatch("native") || !usesActivityWatch("activitywatch") || !usesActivityWatch("shadow") {
		t.Fatal("source selection modes are incorrect")
	}
}

func TestAFKParityUsesSemanticTransitionDespiteDelayedPublication(t *testing.T) {
	eventStart := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	inferred := eventStart.Add(3 * time.Minute)
	published := inferred.Add(8*time.Second + 500*time.Millisecond)
	nativeTransition := inferred.Add(time.Second + 200*time.Millisecond)
	comparison := compareSources(
		[]coreprotocol.WindowObservation{{Start: published.Add(-time.Second), End: published, Executable: "Code.exe"}},
		[]coreprotocol.AFKObservation{{Start: eventStart, End: published, Status: "afk"}},
		nativewatcher.Result{
			Window:  &coreprotocol.WindowObservation{Start: published.Add(-time.Second), End: published, Executable: "Code.exe"},
			AFK:     &coreprotocol.AFKObservation{Start: nativeTransition, End: published, Status: "afk"},
			Session: &coreprotocol.SessionObservation{ObservedAt: published},
		},
		&published, published, 5*time.Second, 3*time.Minute, nil, nil,
	)
	if !comparison.Comparable || !comparison.AFKMatch {
		t.Fatalf("delayed publication should retain semantic parity: %#v", comparison)
	}
	if comparison.ActivityWatchAFKEventStart == nil || !comparison.ActivityWatchAFKEventStart.Equal(eventStart) ||
		comparison.ActivityWatchAFKInferredAt == nil || !comparison.ActivityWatchAFKInferredAt.Equal(inferred) ||
		comparison.NativeAFKTransitionAt == nil || !comparison.NativeAFKTransitionAt.Equal(nativeTransition) {
		t.Fatalf("transition timestamps=%#v", comparison)
	}
	if comparison.AFKSemanticDeltaSeconds == nil || *comparison.AFKSemanticDeltaSeconds != 1.2 {
		t.Fatalf("semantic delta=%v want 1.2", comparison.AFKSemanticDeltaSeconds)
	}
	if comparison.ActivityWatchPublicationDelaySec == nil || *comparison.ActivityWatchPublicationDelaySec != 8.5 {
		t.Fatalf("publication delay=%v want 8.5", comparison.ActivityWatchPublicationDelaySec)
	}
}

func TestAFKParityRejectsSemanticDisagreementBeyondTolerance(t *testing.T) {
	eventStart := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	inferred := eventStart.Add(3 * time.Minute)
	at := inferred.Add(10 * time.Second)
	comparison := compareSources(
		[]coreprotocol.WindowObservation{{Start: at, End: at, Executable: "Code.exe"}},
		[]coreprotocol.AFKObservation{{Start: eventStart, End: at, Status: "afk"}},
		nativewatcher.Result{
			Window:  &coreprotocol.WindowObservation{Start: at, End: at, Executable: "Code.exe"},
			AFK:     &coreprotocol.AFKObservation{Start: inferred.Add(6 * time.Second), End: at, Status: "afk"},
			Session: &coreprotocol.SessionObservation{ObservedAt: at},
		},
		&at, at, 5*time.Second, 3*time.Minute, nil, nil,
	)
	if !comparison.Comparable || comparison.AFKMatch || comparison.AFKSemanticDeltaSeconds == nil || *comparison.AFKSemanticDeltaSeconds != 6 {
		t.Fatalf("semantic disagreement should fail parity: %#v", comparison)
	}
}

func TestAcquisitionTracksFirstPublicationSeparately(t *testing.T) {
	eventStart := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	firstObserved := eventStart.Add(3*time.Minute + 7*time.Second)
	state := newAcquisitionState("shadow", 5*time.Second, 3*time.Minute, nil, nil)
	window := []coreprotocol.WindowObservation{{Start: firstObserved, End: firstObserved, Executable: "Code.exe"}}
	afk := []coreprotocol.AFKObservation{{Start: eventStart, End: firstObserved, Status: "afk"}}
	state.updateActivityWatch(window, afk, nil, firstObserved)
	later := firstObserved.Add(4 * time.Second)
	afk[0].End = later
	state.updateActivityWatch([]coreprotocol.WindowObservation{{Start: later, End: later, Executable: "Code.exe"}}, afk, nil, later)
	state.updateNative(nativewatcher.Result{
		Window:     &coreprotocol.WindowObservation{Start: later, End: later, Executable: "Code.exe"},
		AFK:        &coreprotocol.AFKObservation{Start: eventStart.Add(3 * time.Minute), End: later, Status: "afk"},
		Session:    &coreprotocol.SessionObservation{ObservedAt: later},
		Foreground: nativewatcher.ComponentResult{Connected: true, LastObservation: &later},
		Input:      nativewatcher.ComponentResult{Connected: true, LastObservation: &later},
		SessionAPI: nativewatcher.ComponentResult{Connected: true, LastObservation: &later},
	}, later)
	comparison := state.snapshot().Comparison
	if comparison == nil || comparison.ActivityWatchAFKFirstObservedAt == nil || !comparison.ActivityWatchAFKFirstObservedAt.Equal(firstObserved) {
		t.Fatalf("first publication was not retained: %#v", comparison)
	}
	if comparison.ActivityWatchPublicationDelaySec == nil || *comparison.ActivityWatchPublicationDelaySec != 7 {
		t.Fatalf("publication delay=%v want 7", comparison.ActivityWatchPublicationDelaySec)
	}
}
