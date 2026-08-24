package classifier

import (
	"fmt"
	"strings"
	"time"

	"worktracker/internal/appstate"
	"worktracker/internal/model"
)

func ParseContext(start, end time.Time, data map[string]any) model.ContextEvent {
	version := 0
	if raw, ok := data["schema_version"]; ok {
		switch n := raw.(type) {
		case float64:
			version = int(n)
		case int:
			version = n
		}
	}
	c := model.ContextEvent{Start: start, End: end, SchemaVersion: version, Location: model.Remote, LocationEvidence: model.Fallback, Health: model.Degraded, PassiveEvidence: map[string]model.PassiveEvidence{}}
	if version == 0 {
		parseLegacyContext(&c, data)
		return c
	}
	if version != 1 {
		return c
	}
	parseLocation(&c, data)
	if rawApps, ok := data["app_states"].(map[string]any); ok {
		c.PassiveEvidence = appstate.DecodeV1Evidence(rawApps, start)
		for _, evidence := range c.PassiveEvidence {
			if !evidence.Available {
				c.Health = model.Degraded
			}
		}
	}
	return c
}

func parseLegacyContext(c *model.ContextEvent, data map[string]any) {
	parseLocation(c, data)
	c.PassiveEvidence = appstate.DecodeLegacyEvidence(data, c.Start)
	for _, evidence := range c.PassiveEvidence {
		if !evidence.Available {
			c.Health = model.Degraded
		}
	}
}

func parseLocation(c *model.ContextEvent, data map[string]any) {
	s, _ := data["location"].(string)
	valid := true
	switch strings.ToUpper(s) {
	case "OFFICE":
		c.Location = model.Office
		c.LocationEvidence = model.Confirmed
		c.Health = model.Healthy
	case "REMOTE":
		c.Location = model.Remote
		c.LocationEvidence = model.Confirmed
		c.Health = model.Healthy
	default:
		valid = false
		c.Location = model.Remote
		c.LocationEvidence = model.Fallback
		c.Health = model.Degraded
	}
	if e, ok := data["location_evidence"].(string); ok && c.SchemaVersion == 1 && valid {
		switch model.LocationEvidence(e) {
		case model.Confirmed, model.Stale, model.Fallback:
			c.LocationEvidence = model.LocationEvidence(e)
		}
	}
	if c.LocationEvidence != model.Confirmed {
		c.Health = model.Degraded
	}
	if h, ok := data["network_health"].(string); ok && model.HealthLevel(h) != model.Healthy {
		c.Health = model.Degraded
	}
}

func contextVersionError(version int) error {
	return fmt.Errorf("unsupported context schema version %d", version)
}
