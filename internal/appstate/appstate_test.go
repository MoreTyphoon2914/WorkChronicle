package appstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeDetector struct {
	id  string
	obs Observation
	err error
}

func (f fakeDetector) ID() string { return f.id }
func (f fakeDetector) Observe(context.Context) (Observation, error) {
	return f.obs, f.err
}

func TestRegistryDerivesEvidenceOutsideDetector(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		state     string
		available bool
		passive   bool
	}{
		{"playing", true, true},
		{"paused", true, false},
		{"stopped", true, false},
		{"unavailable", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			d := fakeDetector{id: "vlc", obs: Observation{State: tt.state, Available: tt.available, ObservedAt: now}}
			snapshot := New(NewSource(d, PassiveWhen("playing"))).Observe(context.Background())
			observation := snapshot.Observations["vlc"]
			evidence := snapshot.Evidence["vlc"]
			if observation.State != tt.state || observation.Available != tt.available {
				t.Fatalf("raw observation changed: %#v", observation)
			}
			if evidence.State != tt.state || evidence.Available != tt.available || evidence.PassiveWork != tt.passive {
				t.Fatalf("evidence=%#v", evidence)
			}
		})
	}
}

func TestMultipleDetectorsAndFailureIsolation(t *testing.T) {
	now := time.Now().UTC()
	broken := fakeDetector{id: "broken", err: errors.New("temporary failure")}
	training := fakeDetector{id: "training", obs: Observation{State: "presenting", Available: true, ObservedAt: now}}
	music := fakeDetector{id: "music", obs: Observation{State: "playing", Available: true, ObservedAt: now}}

	snapshot := New(
		NewSource(broken, PassiveWhen("active")),
		NewSource(training, PassiveWhen("presenting")),
		NewSource(music, PassiveWhen()),
	).Observe(context.Background())

	if len(snapshot.Observations) != 3 || len(snapshot.Evidence) != 3 || len(snapshot.Failures) != 1 {
		t.Fatalf("incomplete snapshot: %#v", snapshot)
	}
	if snapshot.Evidence["broken"].Available || snapshot.Evidence["broken"].PassiveWork || snapshot.Failures["broken"] == nil {
		t.Fatalf("failed source was not isolated: %#v", snapshot)
	}
	if !snapshot.Evidence["training"].PassiveWork {
		t.Fatalf("healthy qualifying source lost: %#v", snapshot.Evidence["training"])
	}
	if snapshot.Evidence["music"].PassiveWork {
		t.Fatalf("detector state bypassed its policy: %#v", snapshot.Evidence["music"])
	}
}

func TestContextEvidenceCompatibility(t *testing.T) {
	now := time.Now().UTC()
	v1 := DecodeV1Evidence(map[string]any{
		"vlc":   map[string]any{"state": "playing", "available": true, "passive_work": true},
		"other": map[string]any{"state": "ready", "available": true, "passive_work": false},
	}, now)
	if len(v1) != 2 || !v1["vlc"].PassiveWork || v1["other"].PassiveWork {
		t.Fatalf("v1 evidence=%#v", v1)
	}
	legacy := DecodeLegacyEvidence(map[string]any{"vlc_state": "paused"}, now)
	if !legacy["vlc"].Available || legacy["vlc"].PassiveWork || legacy["vlc"].State != "paused" {
		t.Fatalf("legacy evidence=%#v", legacy)
	}
}
