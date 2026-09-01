package reporting

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"worktracker/internal/activitywatch"
	"worktracker/internal/classifier"
	"worktracker/internal/model"
	"worktracker/internal/workpolicy"
)

func testEvaluator(t *testing.T, minimum, target time.Duration) workpolicy.StaticEvaluator {
	t.Helper()
	evaluator, err := workpolicy.NewStaticEvaluator(workpolicy.StaticTargets{StandardMinimum: minimum, StandardTarget: target})
	if err != nil {
		t.Fatal(err)
	}
	return evaluator
}

func testDay(t *testing.T, segments []model.Segment, reportEnd time.Time, live bool) model.DayReport {
	t.Helper()
	r := classifier.Calculate(segments, reportEnd, 90*time.Minute)
	r.WorkEvaluation = evaluateWork(testEvaluator(t, 6*time.Hour, 8*time.Hour), r.Totals.WorkingSeconds)
	finalizeDay(&r, reportEnd, live)
	return r
}

func TestDailyReportEmptyAndAllUntrackedAccounting(t *testing.T) {
	end := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	empty := testDay(t, nil, end, true)
	if empty.Totals != (model.Totals{}) || empty.FirstWorkAt != nil || empty.LastWorkAt != nil || !empty.ReportEnd.Equal(end) {
		t.Fatalf("empty day=%#v", empty)
	}

	start := end.Add(-2 * time.Hour)
	untracked := testDay(t, []model.Segment{{Start: start, End: end, State: model.Untracked}}, end, true)
	if untracked.Totals != (model.Totals{}) {
		t.Fatalf("no-work coverage became a work session: %#v", untracked.Totals)
	}
	if untracked.ClassifiedCoverageTotals.UntrackedSeconds != end.Sub(start).Seconds() {
		t.Fatalf("represented coverage was lost: %#v", untracked.ClassifiedCoverageTotals)
	}
}

func TestDailyReportLiveWorkTimesAndEstimate(t *testing.T) {
	start := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Hour)
	segments := []model.Segment{
		{Start: start, End: start.Add(time.Hour), State: model.Untracked},
		{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour), State: model.Working},
		{Start: start.Add(2 * time.Hour), End: start.Add(3 * time.Hour), State: model.Break},
		{Start: start.Add(3 * time.Hour), End: start.Add(210 * time.Minute), State: model.Working},
		{Start: start.Add(210 * time.Minute), End: end, State: model.Break},
	}
	r := testDay(t, segments, end, true)
	if r.FirstWorkAt == nil || !r.FirstWorkAt.Equal(start.Add(time.Hour)) {
		t.Fatalf("first_work_at=%v", r.FirstWorkAt)
	}
	if r.LastWorkAt == nil || !r.LastWorkAt.Equal(start.Add(210*time.Minute)) || r.LastWorkAt.Equal(r.ReportEnd) {
		t.Fatalf("last_work_at=%v", r.LastWorkAt)
	}
	if r.CurrentState != model.Break || r.FinalState != "" || !r.Live {
		t.Fatalf("live state=%#v", r)
	}
	if r.RemainingTargetSeconds != (13*time.Hour/2).Seconds() || r.EstimatedFinish == nil || !r.EstimatedFinish.Equal(end.Add(13*time.Hour/2)) {
		t.Fatalf("live estimate=%#v", r)
	}
	if r.Totals.WorkingSeconds != (90*time.Minute).Seconds() || r.Totals.BreakSeconds != (90*time.Minute).Seconds() || r.Totals.UntrackedSeconds != 0 {
		t.Fatalf("pre-work coverage leaked into session totals: %#v", r.Totals)
	}
	if r.ClassifiedCoverageTotals.UntrackedSeconds != time.Hour.Seconds() {
		t.Fatalf("pre-work classified coverage was not retained separately: %#v", r.ClassifiedCoverageTotals)
	}
}

func TestDailyReportHistoricalTargetAndOvertime(t *testing.T) {
	start := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		worked   time.Duration
		band     workpolicy.Band
		overtime time.Duration
	}{
		{name: "target reached", worked: 8 * time.Hour, band: workpolicy.Standard},
		{name: "overtime", worked: 9 * time.Hour, band: workpolicy.Overtime, overtime: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			end := start.Add(tc.worked)
			r := testDay(t, []model.Segment{{Start: start, End: end, State: model.Working}}, end, false)
			if r.Live || r.FinalState != model.Working || r.EstimatedFinish != nil {
				t.Fatalf("historical state/estimate=%#v", r)
			}
			if r.WorkBand != tc.band || r.RemainingTargetSeconds != 0 || r.OvertimeSeconds != tc.overtime.Seconds() {
				t.Fatalf("evaluation projection=%#v", r)
			}
		})
	}
}

func TestDailyReportReconstructsAcrossCollectorRestart(t *testing.T) {
	start := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Hour)
	segments := []model.Segment{
		{Start: start, End: start.Add(time.Hour), State: model.Working},
		{Start: start.Add(time.Hour), End: start.Add(90 * time.Minute), State: model.Untracked},
		{Start: start.Add(90 * time.Minute), End: end, State: model.Working},
	}
	r := testDay(t, segments, end, true)
	if r.Totals.WorkingSeconds != (150*time.Minute).Seconds() || r.Totals.UntrackedSeconds != (30*time.Minute).Seconds() {
		t.Fatalf("restart reconstruction totals=%#v", r.Totals)
	}
}

func TestWeeklySummaryPartialWeekAndWeekend(t *testing.T) {
	weekly := testEvaluator(t, 30*time.Hour, 40*time.Hour)
	monday := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	day := func(hours float64) model.DayReport {
		return model.DayReport{
			Totals:                   model.Totals{WorkingSeconds: hours * 3600, BreakSeconds: 1800, UntrackedSeconds: 900},
			ClassifiedCoverageTotals: model.Totals{WorkingSeconds: hours * 3600, BreakSeconds: 1800, UntrackedSeconds: 4500},
		}
	}

	for _, tc := range []struct {
		name        string
		end         time.Time
		days        []model.DayReport
		denominator int
	}{
		{name: "Monday", end: monday.Add(8 * time.Hour), days: []model.DayReport{day(8)}, denominator: 1},
		{name: "midweek", end: monday.AddDate(0, 0, 2).Add(8 * time.Hour), days: []model.DayReport{day(8), day(7), day(6)}, denominator: 3},
		{name: "Friday", end: monday.AddDate(0, 0, 4).Add(8 * time.Hour), days: []model.DayReport{day(8), day(8), day(8), day(8), day(8)}, denominator: 5},
		{name: "weekend activity", end: monday.AddDate(0, 0, 5).Add(8 * time.Hour), days: []model.DayReport{day(8), day(8), day(8), day(8), day(8), day(2)}, denominator: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := SummarizeWeek(tc.days, monday, tc.end, 5, weekly)
			if r.SchemaVersion != 2 {
				t.Fatalf("weekly schema version=%d", r.SchemaVersion)
			}
			var expectedWorking float64
			for _, d := range tc.days {
				expectedWorking += d.Totals.WorkingSeconds
			}
			if r.AverageDenominator != tc.denominator || r.Totals.WorkingSeconds != expectedWorking || r.AverageWorkingSeconds != expectedWorking/float64(tc.denominator) {
				t.Fatalf("weekly summary=%#v", r)
			}
			if r.Totals.BreakSeconds != float64(len(tc.days))*1800 || r.Totals.UntrackedSeconds != float64(len(tc.days))*900 {
				t.Fatalf("non-working totals lost: %#v", r.Totals)
			}
			var summed model.Totals
			for _, day := range tc.days {
				summed.WorkingSeconds += day.Totals.WorkingSeconds
				summed.BreakSeconds += day.Totals.BreakSeconds
				summed.UntrackedSeconds += day.Totals.UntrackedSeconds
			}
			if r.Totals != summed {
				t.Fatalf("week totals %#v differ from summed days %#v", r.Totals, summed)
			}
			if r.ClassifiedCoverageTotals.UntrackedSeconds != float64(len(tc.days))*4500 {
				t.Fatalf("weekly coverage was not kept separate: %#v", r.ClassifiedCoverageTotals)
			}
			if tc.name == "Friday" && r.WorkEvaluation.Band != workpolicy.Standard {
				t.Fatalf("40-hour week evaluation=%#v", r.WorkEvaluation)
			}
			if tc.name == "weekend activity" && (r.WorkEvaluation.Band != workpolicy.Overtime || r.WorkEvaluation.OvertimeSeconds != 2*time.Hour.Seconds()) {
				t.Fatalf("weekend work was not retained as overtime: %#v", r.WorkEvaluation)
			}
		})
	}
}

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
	ApplyWeekToDateEvaluation(reports, weekly)
	if reports[0].WeekToDateEvaluation.Band != workpolicy.BelowStandardMinimum || reports[1].WeekToDateEvaluation.Band != workpolicy.Standard || reports[2].WeekToDateEvaluation.Band != workpolicy.Overtime || reports[2].WeekToDateEvaluation.OvertimeSeconds != 1 {
		t.Fatalf("week-to-date evaluations=%#v", reports)
	}
	reports[0].WorkEvaluation = workpolicy.Evaluation{Band: workpolicy.Standard}
	reports[0].ReportEnd = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	reports[0].WorkBand = workpolicy.Standard
	b, err := json.Marshal(reports)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.HasPrefix(text, "[") {
		t.Fatalf("legacy week JSON is no longer an array: %s", text)
	}
	for _, field := range []string{`"work_evaluation"`, `"week_to_date_evaluation"`, `"work_policy"`, `"week_to_date_policy"`, `"report_end"`, `"work_band"`} {
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
