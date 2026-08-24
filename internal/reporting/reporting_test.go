package reporting

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"worktracker/internal/activitywatch"
	"worktracker/internal/model"
	"worktracker/internal/workpolicy"
)

func TestUsageRetainsDurationsByState(t *testing.T) {
	s := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	segments := []model.Segment{{Start: s, End: s.Add(time.Hour), State: model.Working, ForegroundApp: "firefox.exe", ForegroundTitle: "  Project   Board "}, {Start: s.Add(time.Hour), End: s.Add(90 * time.Minute), State: model.Break, ForegroundApp: "firefox.exe", ForegroundTitle: "Project Board"}, {Start: s.Add(90 * time.Minute), End: s.Add(2 * time.Hour), State: model.Untracked, ForegroundApp: "firefox.exe", ForegroundTitle: "Project Board"}, {Start: s, End: s.Add(time.Hour), State: model.Working}}
	u := Usage(segments, s, s.Add(2*time.Hour))
	if len(u) != 1 || u[0].Title != "Project Board" || u[0].Durations.Working != 3600 || u[0].Durations.Break != 1800 || u[0].Durations.Untracked != 1800 {
		t.Fatalf("usage=%#v", u)
	}
	b, _ := json.Marshal(u[0])
	text := string(b)
	for _, key := range []string{`"WORKING":3600`, `"BREAK":1800`, `"UNTRACKED":1800`} {
		if !strings.Contains(text, key) {
			t.Fatalf("JSON %s lacks %s", text, key)
		}
	}
}

func TestEvaluationUsesOnlyWorkingTotals(t *testing.T) {
	evaluator, err := workpolicy.NewStaticEvaluator(workpolicy.StaticTargets{StandardMinimum: 6 * time.Hour, StandardTarget: 8 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	report := model.DayReport{Totals: model.Totals{
		WorkingSeconds:   (6 * time.Hour).Seconds(),
		BreakSeconds:     (100 * time.Hour).Seconds(),
		UntrackedSeconds: (100 * time.Hour).Seconds(),
	}}
	report.WorkEvaluation = evaluateWork(evaluator, report.Totals.WorkingSeconds)
	if report.WorkEvaluation.Band != workpolicy.Standard || report.WorkEvaluation.OvertimeSeconds != 0 || report.WorkEvaluation.WorkingSeconds != (6*time.Hour).Seconds() {
		t.Fatalf("non-working totals affected evaluation: %#v", report.WorkEvaluation)
	}
}

func TestWeekToDateEvaluationAndJSONCompatibility(t *testing.T) {
	weekly, err := workpolicy.NewStaticEvaluator(workpolicy.StaticTargets{StandardMinimum: 30 * time.Hour, StandardTarget: 40 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	reports := []model.DayReport{
		{Totals: model.Totals{WorkingSeconds: (20 * time.Hour).Seconds(), BreakSeconds: 999999}},
		{Totals: model.Totals{WorkingSeconds: (20 * time.Hour).Seconds(), UntrackedSeconds: 999999}},
		{Totals: model.Totals{WorkingSeconds: 1}},
	}
	applyWeekToDateEvaluation(reports, weekly)
	if reports[0].WeekToDateEvaluation.Band != workpolicy.BelowStandardMinimum || reports[1].WeekToDateEvaluation.Band != workpolicy.Standard || reports[2].WeekToDateEvaluation.Band != workpolicy.Overtime || reports[2].WeekToDateEvaluation.OvertimeSeconds != 1 {
		t.Fatalf("week-to-date evaluations=%#v", reports)
	}
	reports[0].WorkEvaluation = workpolicy.Evaluation{Band: workpolicy.Standard}
	b, err := json.Marshal(reports)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, field := range []string{`"work_evaluation"`, `"week_to_date_evaluation"`, `"work_policy"`, `"week_to_date_policy"`} {
		if !strings.Contains(text, field) {
			t.Fatalf("week JSON lacks %s compatibility field: %s", field, text)
		}
	}
}

func TestNormalizeBrowserEventsKeepsRawAndEvidenceSeparated(t *testing.T) {
	now := time.Now().UTC()
	raw := activitywatch.Event{Timestamp: now, Data: map[string]any{
		"schema_version": float64(1), "browser": "firefox", "tab_id": "tab-7",
		"active": false, "visible": false, "url": "https://example.com/training",
		"domain": "example.com", "title": "Training", "observed_at": now.Format(time.RFC3339Nano),
		"media": map[string]any{"present": true, "state": "playing", "type": "video", "audible": false},
	}}
	events := normalizeBrowserEvents([]activitywatch.Event{raw})
	if len(events) != 1 || !events[0].Evidence["browser:firefox:tab-7"].PassiveWork {
		t.Fatalf("events=%#v", events)
	}
	if _, containsRawURL := any(events[0].Evidence["browser:firefox:tab-7"]).(map[string]any); containsRawURL {
		t.Fatal("raw browser payload leaked into classification evidence")
	}
}
