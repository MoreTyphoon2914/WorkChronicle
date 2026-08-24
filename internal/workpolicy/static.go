package workpolicy

import (
	"fmt"
	"time"
)

type StaticTargets struct {
	StandardMinimum time.Duration
	StandardTarget  time.Duration
	OvertimeRule    OvertimeRule
}

// StaticEvaluator evaluates fixed configured expectations. It makes no claim
// about a user's historical or learned baseline.
type StaticEvaluator struct {
	targets StaticTargets
}

var _ Evaluator = StaticEvaluator{}

func NewStaticEvaluator(targets StaticTargets) (StaticEvaluator, error) {
	if targets.StandardMinimum <= 0 {
		return StaticEvaluator{}, fmt.Errorf("standard minimum must be positive")
	}
	if targets.StandardTarget <= 0 {
		return StaticEvaluator{}, fmt.Errorf("standard target must be positive")
	}
	if targets.StandardMinimum > targets.StandardTarget {
		return StaticEvaluator{}, fmt.Errorf("standard minimum cannot exceed standard target")
	}
	if targets.OvertimeRule == "" {
		targets.OvertimeRule = OvertimeBeyondStandardTarget
	}
	if targets.OvertimeRule != OvertimeBeyondStandardTarget {
		return StaticEvaluator{}, fmt.Errorf("unsupported overtime rule %q", targets.OvertimeRule)
	}
	return StaticEvaluator{targets: targets}, nil
}

func (s StaticEvaluator) Targets() StaticTargets { return s.targets }

func (s StaticEvaluator) Evaluate(summary WorkSummary) Evaluation {
	working := summary.Working
	if working < 0 {
		working = 0
	}
	result := Evaluation{
		Band:                            BelowStandardMinimum,
		WorkingSeconds:                  working.Seconds(),
		StandardMinimumSeconds:          s.targets.StandardMinimum.Seconds(),
		StandardMinimumRemainingSeconds: remaining(s.targets.StandardMinimum, working).Seconds(),
		StandardTargetSeconds:           s.targets.StandardTarget.Seconds(),
		StandardTargetRemainingSeconds:  remaining(s.targets.StandardTarget, working).Seconds(),
		OvertimeRule:                    s.targets.OvertimeRule,
	}
	result.StandardMinimumReached = working >= s.targets.StandardMinimum
	if result.StandardMinimumReached {
		result.Band = Standard
	}
	if s.targets.OvertimeRule == OvertimeBeyondStandardTarget && working > s.targets.StandardTarget {
		result.Band = Overtime
		result.OvertimeSeconds = (working - s.targets.StandardTarget).Seconds()
	}

	// Populate deprecated aliases with identical values for JSON compatibility.
	result.FirstThresholdSeconds = result.StandardMinimumSeconds
	result.FirstThresholdReached = result.StandardMinimumReached
	result.FirstThresholdRemainingSeconds = result.StandardMinimumRemainingSeconds
	result.TargetSeconds = result.StandardTargetSeconds
	result.TargetRemainingSeconds = result.StandardTargetRemainingSeconds
	return result
}

func remaining(target, worked time.Duration) time.Duration {
	if worked >= target {
		return 0
	}
	return target - worked
}
