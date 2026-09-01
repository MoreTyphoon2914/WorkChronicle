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
		native, now, 5*time.Second, []string{"lockapp.exe"}, []string{"windows default lock screen"},
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
		native, now, 5*time.Second, []string{"lockapp.exe"}, nil,
	)
	if stale.Comparable || stale.Summary != "waiting for comparable fresh observations" {
		t.Fatalf("stale observations should not compare: %#v", stale)
	}
	if usesActivityWatch("native") || !usesActivityWatch("activitywatch") || !usesActivityWatch("shadow") {
		t.Fatal("source selection modes are incorrect")
	}
}
