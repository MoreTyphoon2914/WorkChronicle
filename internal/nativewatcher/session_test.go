package nativewatcher

import (
	"testing"
	"time"
)

func TestSessionStateWTSNotificationsAreAuthoritative(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	var state sessionState
	if got, _, confirmed := state.snapshot(); got != SessionUnknown || confirmed {
		t.Fatalf("startup state=%s confirmed=%t", got, confirmed)
	}
	if !state.notification(SessionLocked, t0) {
		t.Fatal("first LOCK notification was not recorded")
	}
	// Repeated polling that sees an accessible desktop must not alter state;
	// only positive fallback lock evidence is accepted by this state machine.
	for i := 0; i < 5; i++ {
		state.fallbackLocked(t0.Add(time.Duration(i+1) * time.Second))
	}
	if got, at, confirmed := state.snapshot(); got != SessionLocked || at != t0 || !confirmed {
		t.Fatalf("confirmed lock overwritten: state=%s at=%s confirmed=%t", got, at, confirmed)
	}
	if !state.notification(SessionUnlocked, t0.Add(10*time.Second)) {
		t.Fatal("UNLOCK notification was not recorded")
	}
	if got, _, confirmed := state.snapshot(); got != SessionUnlocked || !confirmed {
		t.Fatalf("unlock state=%s confirmed=%t", got, confirmed)
	}
}

func TestSessionStateDuplicateNotificationsAndFallback(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	var state sessionState
	if !state.fallbackLocked(t0) {
		t.Fatal("positive startup fallback was ignored")
	}
	if state.fallbackLocked(t0.Add(time.Second)) {
		t.Fatal("duplicate fallback changed state")
	}
	if !state.notification(SessionLocked, t0.Add(2*time.Second)) {
		t.Fatal("WTS confirmation must supersede fallback provenance")
	}
	if state.notification(SessionLocked, t0.Add(3*time.Second)) {
		t.Fatal("duplicate LOCK event reported a state change")
	}
	if !state.notification(SessionUnlocked, t0.Add(4*time.Second)) {
		t.Fatal("LOCK -> UNLOCK transition was ignored")
	}
	if state.notification(SessionUnlocked, t0.Add(5*time.Second)) {
		t.Fatal("duplicate UNLOCK event reported a state change")
	}
	// Stale or contradictory diagnostic polling cannot override WTS unlock.
	state.fallbackLocked(t0.Add(-time.Hour))
	if got, _, confirmed := state.snapshot(); got != SessionUnlocked || !confirmed {
		t.Fatalf("diagnostic polling overrode WTS: %s confirmed=%t", got, confirmed)
	}
	// A restarted receiver begins conservatively at UNKNOWN.
	var restarted sessionState
	if got, _, confirmed := restarted.snapshot(); got != SessionUnknown || confirmed {
		t.Fatalf("restart state=%s confirmed=%t", got, confirmed)
	}
}
