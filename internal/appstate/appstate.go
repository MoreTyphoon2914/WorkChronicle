package appstate

import (
	"context"
	"time"

	"worktracker/internal/model"
)

// Observation is raw, normalized detector output. Detectors report source
// state and availability only; they never classify work time.
type Observation struct {
	Detector   string
	State      string
	Available  bool
	ObservedAt time.Time
}

type Detector interface {
	ID() string
	Observe(context.Context) (Observation, error)
}

// Policy derives generic passive-work evidence from an observation without
// deciding WORKING/BREAK/UNTRACKED.
type Policy interface {
	Qualifies(Observation) bool
}

type StatePolicy map[string]struct{}

func PassiveWhen(states ...string) StatePolicy {
	p := make(StatePolicy, len(states))
	for _, state := range states {
		p[state] = struct{}{}
	}
	return p
}

func (p StatePolicy) Qualifies(o Observation) bool {
	_, ok := p[o.State]
	return o.Available && ok
}

type Source struct {
	Detector Detector
	Policy   Policy
}

func NewSource(detector Detector, policy Policy) Source {
	return Source{Detector: detector, Policy: policy}
}

type Snapshot struct {
	Observations map[string]Observation
	Evidence     map[string]model.PassiveEvidence
	Failures     map[string]error
}

type Registry struct{ sources []Source }

func New(sources ...Source) *Registry { return &Registry{sources: sources} }

func DeriveEvidence(obs Observation, policy Policy) model.PassiveEvidence {
	qualifies := policy != nil && policy.Qualifies(obs)
	return model.PassiveEvidence{Detector: obs.Detector, State: obs.State, Available: obs.Available, PassiveWork: qualifies, ObservedAt: obs.ObservedAt}
}

func (r *Registry) Observe(ctx context.Context) Snapshot {
	snapshot := Snapshot{Observations: make(map[string]Observation, len(r.sources)), Evidence: make(map[string]model.PassiveEvidence, len(r.sources)), Failures: map[string]error{}}
	for _, source := range r.sources {
		d := source.Detector
		obs, err := d.Observe(ctx)
		if err != nil {
			obs = Observation{Detector: d.ID(), State: "unavailable", ObservedAt: time.Now().UTC()}
			snapshot.Failures[d.ID()] = err
		}
		obs.Detector = d.ID()
		if obs.ObservedAt.IsZero() {
			obs.ObservedAt = time.Now().UTC()
		}
		snapshot.Observations[d.ID()] = obs
		snapshot.Evidence[d.ID()] = DeriveEvidence(obs, source.Policy)
	}
	return snapshot
}
