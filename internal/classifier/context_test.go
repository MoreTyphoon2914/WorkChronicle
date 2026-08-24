package classifier

import (
	"testing"
	"time"

	"worktracker/internal/model"
)

func TestLegacyAndV1ContextCompatibility(t *testing.T) {
	start := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	mid := start.Add(time.Hour)
	end := mid.Add(time.Hour)
	legacy := ParseContext(start, mid, map[string]any{"location": "OFFICE", "vlc_state": "paused"})
	v1 := ParseContext(mid, end, map[string]any{"schema_version": float64(1), "location": "REMOTE", "location_evidence": "confirmed", "app_states": map[string]any{"vlc": map[string]any{"state": "playing", "available": true, "passive_work": true}}})
	if legacy.SchemaVersion != 0 || legacy.Location != model.Office || legacy.LocationEvidence != model.Confirmed || legacy.PassiveEvidence["vlc"].Available != true || legacy.PassiveEvidence["vlc"].PassiveWork {
		t.Fatalf("legacy=%#v", legacy)
	}
	if v1.SchemaVersion != 1 || v1.Location != model.Remote || !v1.PassiveEvidence["vlc"].PassiveWork {
		t.Fatalf("v1=%#v", v1)
	}
	segments := Build([]model.WindowEvent{{Start: start, End: end, App: "firefox.exe"}}, []model.AFKEvent{{Start: start, End: end, Status: "afk"}}, []model.ContextEvent{legacy, v1}, start, end, options())
	if segments[0].State != model.Working || segments[len(segments)-1].State != model.Working {
		t.Fatalf("mixed schema states=%v", states(segments))
	}
}

func TestInvalidLegacyAndFutureSchemaDegradeSafely(t *testing.T) {
	start := time.Now().UTC()
	end := start.Add(time.Minute)
	for _, data := range []map[string]any{{"location": "INVALID", "vlc_state": "unknown"}, {"schema_version": float64(99), "location": "OFFICE"}} {
		c := ParseContext(start, end, data)
		if c.Location != model.Remote || c.LocationEvidence != model.Fallback || c.Health != model.Degraded {
			t.Fatalf("unsafe normalization: %#v", c)
		}
		if o, ok := c.PassiveEvidence["vlc"]; ok && o.PassiveWork {
			t.Fatalf("fabricated passive work: %#v", o)
		}
	}
}
