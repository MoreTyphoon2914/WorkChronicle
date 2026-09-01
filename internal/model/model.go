package model

import (
	"encoding/json"
	"time"

	"worktracker/internal/workpolicy"
)

// EvidenceFreshAt reports whether evidence ending at eventEnd is still usable at
// the observation time. Evidence remains fresh through the inclusive expiry
// boundary and becomes stale only after eventEnd+freshness.
func EvidenceFreshAt(eventEnd, observationTime time.Time, freshness time.Duration) bool {
	return !observationTime.After(eventEnd.Add(freshness))
}

type WorkState string

const (
	Working   WorkState = "WORKING"
	Break     WorkState = "BREAK"
	Untracked WorkState = "UNTRACKED"
)

type Location string

const (
	Office Location = "OFFICE"
	Remote Location = "REMOTE"
)

type LocationEvidence string

const (
	Confirmed LocationEvidence = "confirmed"
	Stale     LocationEvidence = "stale"
	Fallback  LocationEvidence = "fallback"
)

type HealthLevel string

const (
	Healthy  HealthLevel = "healthy"
	Degraded HealthLevel = "degraded"
	Failed   HealthLevel = "failed"
)

type ComponentHealth struct {
	Name        string      `json:"name"`
	Level       HealthLevel `json:"level"`
	Message     string      `json:"message,omitempty"`
	LastSuccess *time.Time  `json:"last_success,omitempty"`
}

// PassiveEvidence is normalized classification input derived from a detector
// observation. It intentionally carries no WORKING/BREAK/UNTRACKED decision.
type PassiveEvidence struct {
	Detector    string    `json:"detector"`
	State       string    `json:"state"`
	Available   bool      `json:"available"`
	PassiveWork bool      `json:"passive_work"`
	ObservedAt  time.Time `json:"observed_at"`
}

type WindowEvent struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	App   string    `json:"app"`
	Title string    `json:"title"`
}

type AFKEvent struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Status string    `json:"status"`
}

type ContextEvent struct {
	Start            time.Time                  `json:"start"`
	End              time.Time                  `json:"end"`
	SchemaVersion    int                        `json:"schema_version"`
	Location         Location                   `json:"location"`
	LocationEvidence LocationEvidence           `json:"location_evidence"`
	Health           HealthLevel                `json:"health"`
	PassiveEvidence  map[string]PassiveEvidence `json:"app_states"`
}

// PassiveEvidenceEvent is a source-independent historical evidence interval.
// Browser context uses this separate stream rather than altering workcontext V1.
type PassiveEvidenceEvent struct {
	Start    time.Time                  `json:"start"`
	End      time.Time                  `json:"end"`
	Evidence map[string]PassiveEvidence `json:"evidence"`
}

type Segment struct {
	Start            time.Time                  `json:"start"`
	End              time.Time                  `json:"end"`
	State            WorkState                  `json:"state"`
	Location         Location                   `json:"location"`
	LocationEvidence LocationEvidence           `json:"location_evidence"`
	ForegroundApp    string                     `json:"foreground_app,omitempty"`
	ForegroundTitle  string                     `json:"foreground_title,omitempty"`
	PassiveEvidence  map[string]PassiveEvidence `json:"app_evidence,omitempty"`
	Health           HealthLevel                `json:"health"`
}

func (s Segment) Duration() time.Duration { return s.End.Sub(s.Start) }

type Totals struct {
	WorkingSeconds   float64 `json:"working_seconds"`
	BreakSeconds     float64 `json:"break_seconds"`
	UntrackedSeconds float64 `json:"untracked_seconds"`
}

// ForegroundStatus is the current foreground application projection used by
// presentation clients. It remains separate from passive detector evidence.
type ForegroundStatus struct {
	Executable string `json:"executable,omitempty"`
	Title      string `json:"title,omitempty"`
}

// StatusReport is the presentation-neutral current-status projection. It is
// produced by reporting and consumed by CLI, tray, and future presentation
// layers without exposing ActivityWatch or classifier internals.
type StatusReport struct {
	WorkState               WorkState                  `json:"work_state"`
	Location                Location                   `json:"location"`
	LocationEvidence        LocationEvidence           `json:"location_evidence"`
	Foreground              ForegroundStatus           `json:"foreground"`
	PassiveDetectorEvidence map[string]PassiveEvidence `json:"passive_detector_evidence"`
	TrackerHealth           HealthLevel                `json:"tracker_health"`
	Totals                  Totals                     `json:"totals"`
	WorkEvaluation          workpolicy.Evaluation      `json:"work_evaluation"`
	RemainingSeconds        float64                    `json:"remaining_seconds"`
	EstimatedFinish         *time.Time                 `json:"estimated_finish,omitempty"`
	EstimateNote            string                     `json:"estimate_note"`
}

type UsageDurations struct {
	Working   float64 `json:"WORKING"`
	Break     float64 `json:"BREAK"`
	Untracked float64 `json:"UNTRACKED"`
}

type Usage struct {
	Executable string         `json:"executable"`
	Title      string         `json:"title"`
	Durations  UsageDurations `json:"durations_seconds"`
}

type DayReport struct {
	Date                     string                 `json:"date"`
	FirstWorkAt              *time.Time             `json:"first_work_at,omitempty"`
	LastWorkAt               *time.Time             `json:"last_work_at,omitempty"`
	ReportEnd                time.Time              `json:"report_end"`
	Live                     bool                   `json:"live"`
	Start                    time.Time              `json:"start,omitempty"`
	End                      time.Time              `json:"end,omitempty"`
	Totals                   Totals                 `json:"totals"`
	ClassifiedCoverageTotals Totals                 `json:"classified_coverage_totals"`
	WorkEvaluation           workpolicy.Evaluation  `json:"work_evaluation"`
	WorkBand                 workpolicy.Band        `json:"work_band"`
	StandardTargetSeconds    float64                `json:"standard_target_seconds"`
	RemainingTargetSeconds   float64                `json:"remaining_target_seconds"`
	OvertimeSeconds          float64                `json:"overtime_seconds"`
	EstimatedFinish          *time.Time             `json:"estimated_finish,omitempty"`
	WeekToDateEvaluation     *workpolicy.Evaluation `json:"week_to_date_evaluation,omitempty"`
	Timeline                 []Segment              `json:"timeline,omitempty"`
	Usage                    []Usage                `json:"usage,omitempty"`
	AutoEnded                bool                   `json:"auto_ended"`
	CurrentState             WorkState              `json:"current_state,omitempty"`
	FinalState               WorkState              `json:"final_state,omitempty"`
}

// WeekReport is the presentation-neutral weekly summary. The legacy week JSON
// command continues to serialize Days directly so existing array consumers do
// not break; presentation layers can consume this richer model.
type WeekReport struct {
	SchemaVersion            int                   `json:"schema_version"`
	PeriodStart              time.Time             `json:"period_start"`
	PeriodEnd                time.Time             `json:"period_end"`
	Days                     []DayReport           `json:"days"`
	Totals                   Totals                `json:"totals"`
	ClassifiedCoverageTotals Totals                `json:"classified_coverage_totals"`
	AverageWorkingSeconds    float64               `json:"average_working_seconds"`
	AverageDenominator       int                   `json:"average_denominator"`
	WorkEvaluation           workpolicy.Evaluation `json:"work_evaluation"`
}

// MarshalJSON emits canonical evaluation names and temporary aliases for the
// work_policy fields introduced before the evaluator abstraction.
func (r DayReport) MarshalJSON() ([]byte, error) {
	type reportAlias DayReport
	return json.Marshal(struct {
		reportAlias
		WorkPolicy       workpolicy.Evaluation  `json:"work_policy"`
		WeekToDatePolicy *workpolicy.Evaluation `json:"week_to_date_policy,omitempty"`
	}{
		reportAlias:      reportAlias(r),
		WorkPolicy:       r.WorkEvaluation,
		WeekToDatePolicy: r.WeekToDateEvaluation,
	})
}
