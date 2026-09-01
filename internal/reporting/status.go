package reporting

import (
	"context"
	"time"

	"worktracker/internal/config"
	"worktracker/internal/model"
)

// Status returns the current presentation-neutral status using the same
// historical reporting path as the status CLI. It does not start a collector.
func (s *Service) Status(ctx context.Context, now time.Time) (model.StatusReport, error) {
	report, err := s.Today(ctx, now)
	if err != nil {
		return model.StatusReport{}, err
	}
	return StatusFromDay(s.Config, report, now), nil
}

// StatusFromDay projects a classified daily report into current status.
func StatusFromDay(_ config.Config, report model.DayReport, now time.Time) model.StatusReport {
	remaining := max(0, report.WorkEvaluation.StandardTargetRemainingSeconds)
	status := model.StatusReport{
		WorkState:               report.CurrentState,
		Location:                model.Remote,
		LocationEvidence:        model.Fallback,
		PassiveDetectorEvidence: map[string]model.PassiveEvidence{},
		TrackerHealth:           model.Degraded,
		Totals:                  report.Totals,
		WorkEvaluation:          report.WorkEvaluation,
		RemainingSeconds:        remaining,
	}
	if remaining > 0 {
		finish := now.Add(time.Duration(remaining * float64(time.Second)))
		status.EstimatedFinish = &finish
		status.EstimateNote = "assumes no additional breaks"
	}
	if len(report.Timeline) == 0 {
		return status
	}
	segment := report.Timeline[len(report.Timeline)-1]
	status.Location = segment.Location
	status.LocationEvidence = segment.LocationEvidence
	status.Foreground = model.ForegroundStatus{Executable: segment.ForegroundApp, Title: segment.ForegroundTitle}
	if segment.PassiveEvidence != nil {
		status.PassiveDetectorEvidence = segment.PassiveEvidence
	}
	status.TrackerHealth = segment.Health
	return status
}
