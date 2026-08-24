package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestExampleConfigIsSafeAndValid(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "REPLACE_IN_LOCAL_IGNORED_CONFIG") {
		t.Fatal("example must use an explicit password placeholder")
	}
	if regexp.MustCompile(`(?i)([0-9a-f]{2}[-:]){5}[0-9a-f]{2}`).MatchString(text) {
		t.Fatal("example contains a concrete MAC address")
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("example invalid: %v", err)
	}
	var shape map[string]any
	if err := json.Unmarshal(b, &shape); err != nil {
		t.Fatal(err)
	}
	if _, exists := shape["target_hours"]; exists {
		t.Fatal("example writes deprecated target_hours")
	}
	if _, exists := shape["work_policy"]; exists {
		t.Fatal("example writes deprecated work_policy")
	}
	if _, exists := shape["work_targets"]; !exists {
		t.Fatal("example lacks canonical work_targets")
	}
}
func TestCurrentShapeGetsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"server":"http://localhost:5600","timezone":"Africa/Cairo","day_boundary":"04:00","target_hours":8,"afk_grace_minutes":2,"auto_end_after_minutes":90,"vlc":{"url":"http://127.0.0.1:8080/requests/status.json"}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ContextQueueSize == 0 || c.NetworkStaleSeconds == 0 || c.TaskName == "" || !c.BrowserIngest.Enabled || c.BrowserIngest.Port != 5601 {
		t.Fatalf("defaults missing: %#v", c)
	}
	if c.WorkTargets.DailyStandardMinimumHours != 6 || c.WorkTargets.DailyTargetHours != 8 || c.WorkTargets.WorkdaysPerWeek != 5 {
		t.Fatalf("work-target defaults missing: %#v", c.WorkTargets)
	}
}

func TestWorkTargetsConfigDerivesWeeklyValuesFromDailySettings(t *testing.T) {
	c := defaults()
	daily, err := c.DailyStaticEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	weekly, err := c.WeeklyStaticEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	if daily.Targets().StandardMinimum != 6*time.Hour || daily.Targets().StandardTarget != 8*time.Hour {
		t.Fatalf("daily=%#v", daily.Targets())
	}
	if weekly.Targets().StandardMinimum != 30*time.Hour || weekly.Targets().StandardTarget != 40*time.Hour {
		t.Fatalf("weekly=%#v", weekly.Targets())
	}

	c.WorkTargets.DailyStandardMinimumHours = 7
	c.WorkTargets.WorkdaysPerWeek = 4
	c.WorkTargets.DailyTargetHours = 9
	weekly, err = c.WeeklyStaticEvaluator()
	if err != nil || weekly.Targets().StandardMinimum != 28*time.Hour || weekly.Targets().StandardTarget != 36*time.Hour {
		t.Fatalf("custom weekly=%#v err=%v", weekly.Targets(), err)
	}
}

func TestWorkTargetsConfigValidation(t *testing.T) {
	c := defaults()
	c.VLC.URL = "http://127.0.0.1/vlc"
	c.WorkTargets.DailyStandardMinimumHours = c.WorkTargets.DailyTargetHours + 1
	if err := c.Validate(); err == nil {
		t.Fatal("accepted daily threshold above target")
	}
	c = defaults()
	c.VLC.URL = "http://127.0.0.1/vlc"
	c.WorkTargets.WorkdaysPerWeek = 0
	if err := c.Validate(); err == nil {
		t.Fatal("accepted zero workdays per week")
	}
}

func TestLegacyWorkPolicyKeysMigrateToWorkTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"server":"http://localhost:5600","timezone":"Africa/Cairo","day_boundary":"04:00","target_hours":9,"work_policy":{"daily_threshold_hours":7,"workdays_per_week":4},"vlc":{"url":"http://127.0.0.1/vlc"}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.WorkTargets.DailyTargetHours != 9 || c.WorkTargets.DailyStandardMinimumHours != 7 || c.WorkTargets.WorkdaysPerWeek != 4 {
		t.Fatalf("legacy aliases were not migrated: %#v", c.WorkTargets)
	}
}

func TestNewWorkTargetsTakePrecedenceOverLegacyAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"server":"http://localhost:5600","timezone":"Africa/Cairo","day_boundary":"04:00","target_hours":99,"work_policy":{"daily_threshold_hours":98,"workdays_per_week":3},"work_targets":{"daily_target_hours":8,"daily_standard_minimum_hours":6,"workdays_per_week":5,"overtime_rule":"WORKING_ABOVE_STANDARD_TARGET"},"vlc":{"url":"http://127.0.0.1/vlc"}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.WorkTargets.DailyTargetHours != 8 || c.WorkTargets.DailyStandardMinimumHours != 6 || c.WorkTargets.WorkdaysPerWeek != 5 {
		t.Fatalf("new work targets did not take precedence: %#v", c.WorkTargets)
	}
}

func TestBrowserIngestConfigValidation(t *testing.T) {
	c := defaults()
	c.VLC.URL = "http://127.0.0.1/vlc"
	c.BrowserIngest.Port = 0
	if err := c.Validate(); err == nil {
		t.Fatal("enabled browser ingestion accepted port zero")
	}
	c.BrowserIngest.Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled ingestion should not require a port: %v", err)
	}
}

func TestReportingPeriodUsesConfiguredBoundary(t *testing.T) {
	c := defaults()
	c.Timezone = "Africa/Cairo"
	c.DayBoundary = "04:00"
	loc, _ := time.LoadLocation(c.Timezone)
	now := time.Date(2026, 8, 17, 3, 30, 0, 0, loc)
	start, end, err := c.ReportingPeriod(now)
	if err != nil || !end.Equal(now) || start.Day() != 16 || start.Hour() != 4 {
		t.Fatalf("start=%s end=%s err=%v", start, end, err)
	}
}
