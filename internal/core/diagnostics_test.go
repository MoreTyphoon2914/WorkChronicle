package core

import (
	"testing"
	"time"

	"worktracker/internal/browsercontext"
)

func browserObservation(family, tab string, at time.Time) browsercontext.Observation {
	return browsercontext.Observation{SchemaVersion: 1, Browser: family, TabID: tab, ObservedAt: at}
}

func TestBrowserHealthCountsFamiliesNotTabs(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	health := browserHealth([]browsercontext.Observation{
		browserObservation("firefox", "tab-1", now.Add(-5*time.Second)),
		browserObservation("firefox", "tab-2", now.Add(-3*time.Second)),
		browserObservation("chrome", "tab-1", now.Add(-time.Second)),
	}, now, 30*time.Second)
	if health.ActiveCount != 2 {
		t.Fatalf("active_count=%d want 2", health.ActiveCount)
	}
	if !health.Sources["firefox"].Active || health.Sources["firefox"].Observations != 2 {
		t.Fatalf("firefox=%#v", health.Sources["firefox"])
	}
	if !health.Sources["chrome"].Active || health.Sources["chrome"].Observations != 1 {
		t.Fatalf("chrome=%#v", health.Sources["chrome"])
	}
	if health.Sources["edge"].Active || health.Sources["edge"].LastSeen != nil || health.Sources["edge"].Observations != 0 {
		t.Fatalf("unseen edge=%#v", health.Sources["edge"])
	}
}

func TestBrowserHealthMarksStaleFamilyInactiveAndRetainsTotals(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	health := browserHealth([]browsercontext.Observation{
		browserObservation("edge", "tab-1", now.Add(-31*time.Second)),
		browserObservation("edge", "tab-2", now.Add(-45*time.Second)),
	}, now, 30*time.Second)
	if health.ActiveCount != 0 || health.Sources["edge"].Active {
		t.Fatalf("stale edge should be inactive: %#v", health)
	}
	if health.Sources["edge"].Observations != 2 || health.Sources["edge"].LastSeen == nil || !health.Sources["edge"].LastSeen.Equal(now.Add(-31*time.Second)) {
		t.Fatalf("stale edge diagnostics lost: %#v", health.Sources["edge"])
	}
}
