package main

import (
	"io"
	"testing"
	"time"

	"worktracker/internal/config"
	"worktracker/internal/model"
	"worktracker/internal/reporting"
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

func TestTrayRejectedOutsideRun(t *testing.T) {
	if _, _, err := parseCommand([]string{"status", "--tray"}, io.Discard); err == nil {
		t.Fatal("status accepted --tray")
	}
}
