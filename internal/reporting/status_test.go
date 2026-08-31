package reporting

import (
	"testing"
	"time"

	"worktracker/internal/config"
	"worktracker/internal/model"
	"worktracker/internal/workpolicy"
)

func TestStatusFromDayCarriesEvaluationAndCurrentEvidence(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	evaluation := workpolicy.Evaluation{Band: workpolicy.Standard, StandardTargetRemainingSeconds: 3600}
	report := model.DayReport{
		CurrentState:   model.Working,
		Totals:         model.Totals{WorkingSeconds: 7 * 3600},
		WorkEvaluation: evaluation,
		Timeline: []model.Segment{{
			Start: now.Add(-time.Minute), End: now, State: model.Working,
			Location: model.Office, LocationEvidence: model.Confirmed,
			ForegroundApp: "Code.exe", ForegroundTitle: "WorkChronicle",
			PassiveEvidence: map[string]model.PassiveEvidence{"vlc": {Detector: "vlc", State: "paused", Available: true}},
			Health:          model.Healthy,
		}},
	}
	status := StatusFromDay(config.Config{WorkTargets: config.WorkTargets{DailyTargetHours: 8}}, report, now)
	if status.WorkEvaluation.Band != workpolicy.Standard || status.RemainingSeconds != 3600 {
		t.Fatalf("evaluation not carried into status: %#v", status)
	}
	if status.Foreground.Executable != "Code.exe" || status.PassiveDetectorEvidence["vlc"].State != "paused" {
		t.Fatalf("current evidence not projected: %#v", status)
	}
}
