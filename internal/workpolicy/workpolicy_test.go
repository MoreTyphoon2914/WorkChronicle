package workpolicy

import (
	"testing"
	"time"
)

func TestDailyBoundaries(t *testing.T) {
	evaluator, err := NewStaticEvaluator(StaticTargets{StandardMinimum: 6 * time.Hour, StandardTarget: 8 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		worked     time.Duration
		band       Band
		firstLeft  time.Duration
		targetLeft time.Duration
		overtime   time.Duration
	}{
		{"zero", 0, BelowStandardMinimum, 6 * time.Hour, 8 * time.Hour, 0},
		{"one second below first threshold", 6*time.Hour - time.Second, BelowStandardMinimum, time.Second, 2*time.Hour + time.Second, 0},
		{"exactly first threshold", 6 * time.Hour, Standard, 0, 2 * time.Hour, 0},
		{"seven hours", 7 * time.Hour, Standard, 0, time.Hour, 0},
		{"exactly target", 8 * time.Hour, Standard, 0, 0, 0},
		{"one second overtime", 8*time.Hour + time.Second, Overtime, 0, 0, time.Second},
		{"ten hours", 10 * time.Hour, Overtime, 0, 0, 2 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluator.Evaluate(WorkSummary{Working: tt.worked})
			if got.Band != tt.band || got.StandardMinimumRemainingSeconds != tt.firstLeft.Seconds() || got.StandardTargetRemainingSeconds != tt.targetLeft.Seconds() || got.OvertimeSeconds != tt.overtime.Seconds() {
				t.Fatalf("evaluation=%#v", got)
			}
			if got.StandardMinimumReached != (tt.worked >= 6*time.Hour) {
				t.Fatalf("standard minimum reached=%t", got.StandardMinimumReached)
			}
			if got.FirstThresholdSeconds != got.StandardMinimumSeconds || got.TargetSeconds != got.StandardTargetSeconds {
				t.Fatalf("deprecated JSON aliases diverged: %#v", got)
			}
		})
	}
}

func TestWeeklyBoundaries(t *testing.T) {
	evaluator, err := NewStaticEvaluator(StaticTargets{StandardMinimum: 30 * time.Hour, StandardTarget: 40 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		worked   time.Duration
		band     Band
		overtime time.Duration
	}{
		{30*time.Hour - time.Second, BelowStandardMinimum, 0},
		{30 * time.Hour, Standard, 0},
		{40 * time.Hour, Standard, 0},
		{40*time.Hour + time.Second, Overtime, time.Second},
	} {
		got := evaluator.Evaluate(WorkSummary{Working: tt.worked})
		if got.Band != tt.band || got.OvertimeSeconds != tt.overtime.Seconds() {
			t.Fatalf("worked=%s evaluation=%#v", tt.worked, got)
		}
	}
}

func TestChangingStaticTargetsRecalculatesHistoricalWork(t *testing.T) {
	worked := 7 * time.Hour
	standard, _ := NewStaticEvaluator(StaticTargets{StandardMinimum: 6 * time.Hour, StandardTarget: 8 * time.Hour})
	stricter, _ := NewStaticEvaluator(StaticTargets{StandardMinimum: 7*time.Hour + 30*time.Minute, StandardTarget: 9 * time.Hour})
	if standard.Evaluate(WorkSummary{Working: worked}).Band != Standard {
		t.Fatal("default targets should treat seven hours as standard")
	}
	if stricter.Evaluate(WorkSummary{Working: worked}).Band != BelowStandardMinimum {
		t.Fatal("changed targets did not recalculate the same work duration")
	}
}

func TestNewStaticEvaluatorRejectsInvalidTargets(t *testing.T) {
	for _, thresholds := range [][2]time.Duration{{0, time.Hour}, {time.Hour, 0}, {2 * time.Hour, time.Hour}} {
		if _, err := NewStaticEvaluator(StaticTargets{StandardMinimum: thresholds[0], StandardTarget: thresholds[1]}); err == nil {
			t.Fatalf("accepted invalid thresholds %v", thresholds)
		}
	}
}

type futureCompatibleEvaluator struct{}

func (futureCompatibleEvaluator) Evaluate(summary WorkSummary) Evaluation {
	return Evaluation{WorkingSeconds: summary.Working.Seconds()}
}

func TestEvaluatorInterfaceSupportsFutureImplementations(t *testing.T) {
	var static Evaluator = mustStatic(t, 6*time.Hour, 8*time.Hour)
	var future Evaluator = futureCompatibleEvaluator{}
	for _, evaluator := range []Evaluator{static, future} {
		if got := evaluator.Evaluate(WorkSummary{Working: time.Hour}); got.WorkingSeconds != time.Hour.Seconds() {
			t.Fatalf("evaluator did not consume WorkSummary: %#v", got)
		}
	}
}

func mustStatic(t *testing.T, minimum, target time.Duration) StaticEvaluator {
	t.Helper()
	evaluator, err := NewStaticEvaluator(StaticTargets{StandardMinimum: minimum, StandardTarget: target})
	if err != nil {
		t.Fatal(err)
	}
	return evaluator
}
