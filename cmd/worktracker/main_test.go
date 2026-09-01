package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"worktracker/internal/config"
	"worktracker/internal/model"
	"worktracker/internal/reporting"
	"worktracker/internal/workpolicy"
)

func TestCommandFlagsAfterCommand(t *testing.T) {
	for _, args := range [][]string{{"status", "--config", "x.json", "--json"}, {"today", "--config", "x.json"}, {"week", "--json", "--config", "x.json"}, {"doctor", "--config", "x.json", "--json"}, {"run", "--config", "x.json"}, {"run", "--tray", "--config", "x.json"}, {"install", "--config", "x.json"}, {"uninstall", "--config", "x.json"}} {
		cmd, opt, err := parseCommand(args, io.Discard)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if cmd != args[0] || opt.configPath != "x.json" {
			t.Fatalf("%v parsed as %q %#v", args, cmd, opt)
		}
		if args[0] == "run" && len(args) > 1 && args[1] == "--tray" && !opt.tray {
			t.Fatalf("%v did not enable the tray", args)
		}
	}
}

func TestHumanReportsUseApplicationsWithoutSensitiveTitles(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	secret := "https://example.test/account?token=do-not-print"
	evaluation := workpolicy.Evaluation{Band: workpolicy.Standard, StandardTargetSeconds: 8 * 3600, StandardTargetRemainingSeconds: 3600}
	finish := now.Add(time.Hour)
	day := model.DayReport{
		Date: "2026-08-31", ReportEnd: now, Live: true, CurrentState: model.Working,
		Totals: model.Totals{WorkingSeconds: 7 * 3600}, WorkEvaluation: evaluation,
		WorkBand: workpolicy.Standard, StandardTargetSeconds: 8 * 3600, RemainingTargetSeconds: 3600,
		EstimatedFinish: &finish,
		Timeline:        []model.Segment{{Start: now.Add(-time.Hour), End: now, State: model.Working, Location: model.Remote, LocationEvidence: model.Confirmed, ForegroundApp: "firefox.exe", ForegroundTitle: secret, Health: model.Healthy}},
		Usage:           []model.Usage{{Executable: "firefox.exe", Title: secret, Durations: model.UsageDurations{Working: 3600}}},
	}
	cfg := config.Config{Timezone: "UTC"}

	for name, print := range map[string]func(*bytes.Buffer){
		"status": func(out *bytes.Buffer) { printStatus(out, cfg, day) },
		"today":  func(out *bytes.Buffer) { printToday(out, day, cfg) },
		"week": func(out *bytes.Buffer) {
			printWeek(out, model.WeekReport{PeriodStart: now, PeriodEnd: now, Days: []model.DayReport{day}, Totals: day.Totals, AverageDenominator: 1, AverageWorkingSeconds: day.Totals.WorkingSeconds, WorkEvaluation: evaluation}, cfg)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			print(&out)
			text := out.String()
			if strings.Contains(text, secret) || strings.Contains(text, "token=") {
				t.Fatalf("sensitive title leaked in human output: %s", text)
			}
			if !strings.Contains(text, "firefox.exe") {
				t.Fatalf("application missing from human output: %s", text)
			}
		})
	}
}

func TestTodayHumanOutputContainsStageFiveSummary(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first, last, finish := now.Add(-4*time.Hour), now.Add(-time.Hour), now.Add(2*time.Hour)
	r := model.DayReport{
		Date: "2026-09-01", FirstWorkAt: &first, LastWorkAt: &last, ReportEnd: now,
		Live: true, CurrentState: model.Break, WorkBand: workpolicy.Standard,
		StandardTargetSeconds: 8 * 3600, RemainingTargetSeconds: 2 * 3600,
		OvertimeSeconds: 0, EstimatedFinish: &finish,
	}
	var out bytes.Buffer
	printToday(&out, r, config.Config{Timezone: "UTC"})
	text := out.String()
	for _, label := range []string{"First work:", "Last work:", "Report end:", "Target:", "Remaining:", "Overtime:", "Evaluation:", "Finish est:", "Current:"} {
		if !strings.Contains(text, label) {
			t.Fatalf("today output lacks %q: %s", label, text)
		}
	}
}

func TestWeekHumanUsageAggregatesTitlesByExecutable(t *testing.T) {
	usage := []model.Usage{
		{Executable: "msedge.exe", Title: "https://login.test/?token=one", Durations: model.UsageDurations{Working: 60, Untracked: 10}},
		{Executable: "MSEDGE.EXE", Title: "https://login.test/?credential=two", Durations: model.UsageDurations{Working: 120, Break: 30}},
	}
	aggregated := usageByExecutable(usage)
	if len(aggregated) != 1 || aggregated[0].Title != "" || aggregated[0].Durations.Working != 180 || aggregated[0].Durations.Break != 30 || aggregated[0].Durations.Untracked != 10 {
		t.Fatalf("human usage aggregation=%#v", aggregated)
	}
	var out bytes.Buffer
	printWeek(&out, model.WeekReport{PeriodStart: time.Now(), PeriodEnd: time.Now(), Days: []model.DayReport{{Date: "2026-09-01", Usage: usage}}, AverageDenominator: 1}, config.Config{Timezone: "UTC"})
	text := strings.ToLower(out.String())
	if strings.Count(text, "msedge.exe") != 1 || strings.Contains(text, "token=") || strings.Contains(text, "credential=") {
		t.Fatalf("week output did not safely aggregate titles: %s", out.String())
	}
}

func TestStatusAlwaysDisplaysHealthWithoutPassiveEvidence(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	r := model.DayReport{
		CurrentState:   model.Untracked,
		WorkEvaluation: workpolicy.Evaluation{StandardTargetRemainingSeconds: 8 * 3600},
		Timeline:       []model.Segment{{Start: now.Add(-time.Minute), End: now, State: model.Untracked, Location: model.Remote, LocationEvidence: model.Fallback, Health: model.Degraded}},
	}
	var out bytes.Buffer
	printStatus(&out, config.Config{Timezone: "UTC"}, r)
	if !strings.Contains(out.String(), "Health:      degraded") {
		t.Fatalf("health disappeared without passive evidence: %s", out.String())
	}
}

func TestStatusSeparatesForegroundAndPassiveEvidence(t *testing.T) {
	now := time.Now()
	r := model.DayReport{CurrentState: model.Working, Timeline: []model.Segment{{Start: now.Add(-time.Minute), End: now, State: model.Working, Location: model.Remote, LocationEvidence: model.Confirmed, ForegroundApp: "firefox.exe", PassiveEvidence: map[string]model.PassiveEvidence{"vlc": {Detector: "vlc", State: "playing", Available: true, PassiveWork: true}}, Health: model.Healthy}}}
	s := reporting.StatusFromDay(config.Config{WorkTargets: config.WorkTargets{DailyTargetHours: 8}}, r, now)
	if s.Foreground.Executable != "firefox.exe" || s.PassiveDetectorEvidence["vlc"].State != "playing" {
		t.Fatalf("status conflated evidence: %#v", s)
	}
}

func TestDoctorAndStatusShareFreshnessBoundary(t *testing.T) {
	end := time.Date(2026, 8, 19, 14, 8, 0, 0, time.UTC)
	threshold := 15 * time.Second
	if !model.EvidenceFreshAt(end, end.Add(threshold), threshold) {
		t.Fatal("doctor/status evidence should be fresh at expiry")
	}
	if model.EvidenceFreshAt(end, end.Add(threshold+time.Nanosecond), threshold) {
		t.Fatal("doctor/status evidence should be stale after expiry")
	}
}
func TestJSONRejectedWhereNotApplicable(t *testing.T) {
	if _, _, err := parseCommand([]string{"run", "--json"}, io.Discard); err == nil {
		t.Fatal("run accepted --json")
	}
}

func TestWeekVersionedJSONFlagAndMutualExclusion(t *testing.T) {
	cmd, options, err := parseCommand([]string{"week", "--config", "x.json", "--json-v2"}, io.Discard)
	if err != nil || cmd != "week" || !options.jsonV2 {
		t.Fatalf("versioned week JSON flag: cmd=%q options=%#v err=%v", cmd, options, err)
	}
	if _, _, err := parseCommand([]string{"week", "--json", "--json-v2"}, io.Discard); err == nil {
		t.Fatal("week accepted conflicting JSON formats")
	}

	var legacy, versioned bytes.Buffer
	day := model.DayReport{Date: "2026-08-31"}
	writeJSON(&legacy, []model.DayReport{day})
	writeJSON(&versioned, model.WeekReport{SchemaVersion: 2, Days: []model.DayReport{day}})
	if !strings.HasPrefix(strings.TrimSpace(legacy.String()), "[") {
		t.Fatalf("legacy week JSON is not an array: %s", legacy.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(versioned.String()), "{") || !strings.Contains(versioned.String(), `"schema_version": 2`) {
		t.Fatalf("versioned week JSON is not a V2 object: %s", versioned.String())
	}
}

func TestTrayRejectedOutsideRun(t *testing.T) {
	if _, _, err := parseCommand([]string{"status", "--tray"}, io.Discard); err == nil {
		t.Fatal("status accepted --tray")
	}
}
