package appstate

import (
	"strings"
	"time"

	"worktracker/internal/model"
)

// DecodeV1Evidence normalizes schema-V1 app_states without embedding any
// application-specific logic in the classifier.
func DecodeV1Evidence(rawApps map[string]any, observedAt time.Time) map[string]model.PassiveEvidence {
	out := make(map[string]model.PassiveEvidence, len(rawApps))
	for id, raw := range rawApps {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		state, _ := m["state"].(string)
		available, _ := m["available"].(bool)
		passive, _ := m["passive_work"].(bool)
		out[id] = model.PassiveEvidence{Detector: id, State: strings.ToLower(state), Available: available, PassiveWork: available && passive, ObservedAt: observedAt}
	}
	return out
}

// DecodeLegacyEvidence is the compatibility adapter for Python schema-V0
// context. New sources are never added to this legacy format.
func DecodeLegacyEvidence(data map[string]any, observedAt time.Time) map[string]model.PassiveEvidence {
	state, _ := data["vlc_state"].(string)
	state = strings.ToLower(state)
	available := state == "playing" || state == "paused" || state == "stopped"
	if !available {
		state = "unavailable"
	}
	return map[string]model.PassiveEvidence{"vlc": {Detector: "vlc", State: state, Available: available, PassiveWork: state == "playing", ObservedAt: observedAt}}
}
