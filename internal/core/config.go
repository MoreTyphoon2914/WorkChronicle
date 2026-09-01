package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"worktracker/internal/workpolicy"
)

type Config struct {
	ListenAddress     string
	DataDir           string
	AgentTokenFile    string
	Timezone          string
	DayBoundary       string
	AFKGrace          time.Duration
	AutoEnd           time.Duration
	EvidenceFreshness time.Duration
	Retention         time.Duration
	AgentStale        time.Duration
	BrowserFreshness  time.Duration
	DailyMinimum      time.Duration
	DailyTarget       time.Duration
	WorkdaysPerWeek   int
	LockApps          []string
	LockTitleContains []string
}

func ConfigFromEnv() (Config, error) {
	c := Config{
		ListenAddress: "0.0.0.0:8080", DataDir: "/data", AgentTokenFile: "/run/secrets/agent_token",
		Timezone: "Local", DayBoundary: "04:00", AFKGrace: 2 * time.Minute,
		AutoEnd: 90 * time.Minute, EvidenceFreshness: 15 * time.Second,
		Retention: 35 * 24 * time.Hour, AgentStale: 30 * time.Second,
		BrowserFreshness: 30 * time.Second,
		DailyMinimum:     6 * time.Hour, DailyTarget: 8 * time.Hour, WorkdaysPerWeek: 5,
		LockApps: []string{"lockapp.exe"}, LockTitleContains: []string{"windows default lock screen"},
	}
	stringEnv(&c.ListenAddress, "WORKCHRONICLE_LISTEN_ADDR")
	stringEnv(&c.DataDir, "WORKCHRONICLE_DATA_DIR")
	stringEnv(&c.AgentTokenFile, "WORKCHRONICLE_AGENT_TOKEN_FILE")
	stringEnv(&c.Timezone, "WORKCHRONICLE_TIMEZONE")
	stringEnv(&c.DayBoundary, "WORKCHRONICLE_DAY_BOUNDARY")
	var err error
	if c.AFKGrace, err = durationEnv("WORKCHRONICLE_AFK_GRACE", c.AFKGrace); err != nil {
		return Config{}, err
	}
	if c.AutoEnd, err = durationEnv("WORKCHRONICLE_AUTO_END", c.AutoEnd); err != nil {
		return Config{}, err
	}
	if c.EvidenceFreshness, err = durationEnv("WORKCHRONICLE_EVIDENCE_FRESHNESS", c.EvidenceFreshness); err != nil {
		return Config{}, err
	}
	if c.Retention, err = durationEnv("WORKCHRONICLE_RETENTION", c.Retention); err != nil {
		return Config{}, err
	}
	if c.AgentStale, err = durationEnv("WORKCHRONICLE_AGENT_STALE", c.AgentStale); err != nil {
		return Config{}, err
	}
	if c.BrowserFreshness, err = durationEnv("WORKCHRONICLE_BROWSER_ACTIVE_FRESHNESS", c.BrowserFreshness); err != nil {
		return Config{}, err
	}
	if c.DailyMinimum, err = durationEnv("WORKCHRONICLE_DAILY_MINIMUM", c.DailyMinimum); err != nil {
		return Config{}, err
	}
	if c.DailyTarget, err = durationEnv("WORKCHRONICLE_DAILY_TARGET", c.DailyTarget); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(os.Getenv("WORKCHRONICLE_WORKDAYS_PER_WEEK")); raw != "" {
		c.WorkdaysPerWeek, err = strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("WORKCHRONICLE_WORKDAYS_PER_WEEK: %w", err)
		}
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return Config{}, fmt.Errorf("timezone: %w", err)
	}
	if _, err := time.Parse("15:04", c.DayBoundary); err != nil {
		return Config{}, fmt.Errorf("day boundary must be HH:MM")
	}
	if c.DailyMinimum <= 0 || c.DailyTarget <= 0 || c.DailyMinimum > c.DailyTarget {
		return Config{}, fmt.Errorf("daily work targets are invalid")
	}
	if c.WorkdaysPerWeek < 1 || c.WorkdaysPerWeek > 7 {
		return Config{}, fmt.Errorf("workdays per week must be between 1 and 7")
	}
	return c, nil
}

func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.Local
	}
	return loc
}

func (c Config) ReportingPeriod(now time.Time) (time.Time, time.Time) {
	loc := c.Location()
	now = now.In(loc)
	clock, _ := time.Parse("15:04", c.DayBoundary)
	start := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	if now.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return start, now
}

func (c Config) DailyEvaluator() (workpolicy.StaticEvaluator, error) {
	return workpolicy.NewStaticEvaluator(workpolicy.StaticTargets{StandardMinimum: c.DailyMinimum, StandardTarget: c.DailyTarget})
}

func (c Config) WeeklyEvaluator() (workpolicy.StaticEvaluator, error) {
	days := time.Duration(c.WorkdaysPerWeek)
	return workpolicy.NewStaticEvaluator(workpolicy.StaticTargets{StandardMinimum: c.DailyMinimum * days, StandardTarget: c.DailyTarget * days})
}

func stringEnv(target *string, name string) {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		*target = value
	}
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}
