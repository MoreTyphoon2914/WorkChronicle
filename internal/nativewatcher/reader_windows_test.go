//go:build windows

package nativewatcher

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"worktracker/internal/coreprotocol"
)

func TestWindowsSessionReceiverCleanShutdownAndRestart(t *testing.T) {
	for attempt := 0; attempt < 2; attempt++ {
		reader, err := NewWindowsReader(nil)
		if err != nil {
			t.Fatalf("start receiver %d: %v", attempt, err)
		}
		done := make(chan error, 1)
		go func() { done <- reader.Close() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("close receiver %d: %v", attempt, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("receiver %d did not shut down cleanly", attempt)
		}
	}
}

func TestWindowsReaderUsesConfirmedWTSStateWithoutDesktopOverride(t *testing.T) {
	state := &sessionState{}
	reader := WindowsReader{session: state}
	t0 := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	state.notification(SessionLocked, t0)
	locked, err := reader.SessionLocked(context.Background())
	if err != nil || !locked {
		t.Fatalf("confirmed WTS lock: locked=%t err=%v", locked, err)
	}
	state.notification(SessionUnlocked, t0.Add(time.Minute))
	locked, err = reader.SessionLocked(context.Background())
	if err != nil || locked {
		t.Fatalf("confirmed WTS unlock: locked=%t err=%v", locked, err)
	}
}

func TestNativeSessionFactContainsNoWorkClassification(t *testing.T) {
	fact := coreprotocol.SessionObservation{ObservedAt: time.Now().UTC(), Locked: true, Source: coreprotocol.SourceNativeWindows}
	payload, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	upper := strings.ToUpper(string(payload))
	for _, policy := range []string{"WORKING", "BREAK", "UNTRACKED"} {
		if strings.Contains(upper, policy) {
			t.Fatalf("session acquisition leaked classification %q: %s", policy, payload)
		}
	}
}
