package workpolicy

import "time"

type Band string

const (
	BelowStandardMinimum Band = "BELOW_THRESHOLD"
	Standard             Band = "STANDARD"
	Overtime             Band = "OVERTIME"

	// Deprecated: use BelowStandardMinimum. The serialized value is unchanged.
	BelowThreshold = BelowStandardMinimum
)

type OvertimeRule string

const OvertimeBeyondStandardTarget OvertimeRule = "WORKING_ABOVE_STANDARD_TARGET"

// WorkSummary is the classified input to the evaluation layer. Only WORKING
// duration is represented; BREAK and UNTRACKED cannot affect target status.
type WorkSummary struct {
	Working time.Duration
}

type Evaluation struct {
	Band                            Band         `json:"band"`
	WorkingSeconds                  float64      `json:"working_seconds"`
	StandardMinimumSeconds          float64      `json:"standard_minimum_seconds"`
	StandardMinimumReached          bool         `json:"standard_minimum_reached"`
	StandardMinimumRemainingSeconds float64      `json:"standard_minimum_remaining_seconds"`
	StandardTargetSeconds           float64      `json:"standard_target_seconds"`
	StandardTargetRemainingSeconds  float64      `json:"standard_target_remaining_seconds"`
	OvertimeRule                    OvertimeRule `json:"overtime_rule"`
	OvertimeSeconds                 float64      `json:"overtime_seconds"`

	// Deprecated JSON aliases retained while existing report consumers migrate.
	FirstThresholdSeconds          float64 `json:"first_threshold_seconds"`
	FirstThresholdReached          bool    `json:"first_threshold_reached"`
	FirstThresholdRemainingSeconds float64 `json:"first_threshold_remaining_seconds"`
	TargetSeconds                  float64 `json:"target_seconds"`
	TargetRemainingSeconds         float64 `json:"target_remaining_seconds"`
}
