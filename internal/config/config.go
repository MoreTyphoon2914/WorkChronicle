package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"worktracker/internal/workpolicy"
)

type VLC struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

type Logging struct {
	Level      string `json:"level"`
	File       string `json:"file"`
	MaxBytes   int64  `json:"max_bytes"`
	MaxBackups int    `json:"max_backups"`
}

type BrowserIngest struct {
	Enabled      bool  `json:"enabled"`
	Port         int   `json:"port"`
	MaxBodyBytes int64 `json:"max_body_bytes"`
}

type WorkTargets struct {
	DailyTargetHours          float64                 `json:"daily_target_hours"`
	DailyStandardMinimumHours float64                 `json:"daily_standard_minimum_hours"`
	WorkdaysPerWeek           int                     `json:"workdays_per_week"`
	OvertimeRule              workpolicy.OvertimeRule `json:"overtime_rule"`
}

type Config struct {
	Server                string        `json:"server"`
	WindowBucket          string        `json:"window_bucket"`
	AFKBucket             string        `json:"afk_bucket"`
	Timezone              string        `json:"timezone"`
	DayBoundary           string        `json:"day_boundary"`
	AFKGraceMinutes       float64       `json:"afk_grace_minutes"`
	AutoEndAfterMinutes   float64       `json:"auto_end_after_minutes"`
	StatusStaleSeconds    float64       `json:"status_stale_seconds"`
	ContextPollSeconds    float64       `json:"context_poll_seconds"`
	NetworkRefreshSeconds float64       `json:"network_refresh_seconds"`
	NetworkStaleSeconds   float64       `json:"network_stale_seconds"`
	HTTPTimeoutSeconds    float64       `json:"http_timeout_seconds"`
	RetryMaxSeconds       float64       `json:"retry_max_seconds"`
	ContextQueueSize      int           `json:"context_queue_size"`
	TaskName              string        `json:"task_name"`
	LockApps              []string      `json:"lock_apps"`
	LockTitleContains     []string      `json:"lock_title_contains"`
	HomeGatewayMACs       []string      `json:"home_gateway_macs"`
	OfficeGatewayMACs     []string      `json:"office_gateway_macs"`
	VLC                   VLC           `json:"vlc"`
	Logging               Logging       `json:"logging"`
	BrowserIngest         BrowserIngest `json:"browser_ingest"`
	WorkTargets           WorkTargets   `json:"work_targets"`
	ConfigPath            string        `json:"-"`
}

func defaults() Config {
	return Config{
		Server: "http://localhost:5600", Timezone: "Local", DayBoundary: "04:00",
		AFKGraceMinutes: 2, AutoEndAfterMinutes: 90,
		StatusStaleSeconds: 15, ContextPollSeconds: 2, NetworkRefreshSeconds: 60,
		NetworkStaleSeconds: 180, HTTPTimeoutSeconds: 10, RetryMaxSeconds: 30,
		ContextQueueSize: 10000, TaskName: "WorkTracker",
		LockApps: []string{"lockapp.exe"}, LockTitleContains: []string{"windows default lock screen"},
		Logging:       Logging{Level: "info", File: "logs/worktracker.log", MaxBytes: 5 << 20, MaxBackups: 3},
		BrowserIngest: BrowserIngest{Enabled: true, Port: 5601, MaxBodyBytes: 64 << 10},
		WorkTargets: WorkTargets{
			DailyTargetHours: 8, DailyStandardMinimumHours: 6, WorkdaysPerWeek: 5,
			OvertimeRule: workpolicy.OvertimeBeyondStandardTarget,
		},
	}
}

func Load(path string) (Config, error) {
	if path == "" {
		path = "config.json"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", abs, err)
	}
	c := defaults()
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := applyLegacyWorkTargetAliases(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse work targets: %w", err)
	}
	c.ConfigPath = abs
	if c.Logging.File != "" && !filepath.IsAbs(c.Logging.File) {
		c.Logging.File = filepath.Join(filepath.Dir(abs), c.Logging.File)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	var problems []string
	if c.Server == "" {
		problems = append(problems, "server is required")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		problems = append(problems, "invalid timezone")
	}
	if _, err := time.Parse("15:04", c.DayBoundary); err != nil {
		problems = append(problems, "day_boundary must be HH:MM")
	}
	if _, err := c.DailyStaticEvaluator(); err != nil {
		problems = append(problems, "work_targets: "+err.Error())
	}
	if c.WorkTargets.WorkdaysPerWeek <= 0 {
		problems = append(problems, "work_targets.workdays_per_week must be positive")
	}
	if c.AFKGraceMinutes < 0 || c.AutoEndAfterMinutes < 0 {
		problems = append(problems, "grace and auto-end cannot be negative")
	}
	if c.StatusStaleSeconds < 0 {
		problems = append(problems, "status_stale_seconds cannot be negative")
	}
	if c.ContextPollSeconds <= 0 || c.NetworkRefreshSeconds <= 0 || c.NetworkStaleSeconds < 0 {
		problems = append(problems, "poll/freshness values are invalid")
	}
	if c.HTTPTimeoutSeconds <= 0 || c.RetryMaxSeconds <= 0 || c.ContextQueueSize <= 0 {
		problems = append(problems, "retry/queue values are invalid")
	}
	if c.VLC.URL == "" {
		problems = append(problems, "vlc.url is required")
	}
	if c.BrowserIngest.Enabled && (c.BrowserIngest.Port < 1 || c.BrowserIngest.Port > 65535) {
		problems = append(problems, "browser_ingest.port must be between 1 and 65535")
	}
	if c.BrowserIngest.MaxBodyBytes <= 0 {
		problems = append(problems, "browser_ingest.max_body_bytes must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c Config) Location() (*time.Location, error) { return time.LoadLocation(c.Timezone) }
func (c Config) AFKGrace() time.Duration {
	return time.Duration(c.AFKGraceMinutes * float64(time.Minute))
}
func (c Config) AutoEnd() time.Duration {
	return time.Duration(c.AutoEndAfterMinutes * float64(time.Minute))
}
func (c Config) StatusStale() time.Duration {
	return time.Duration(c.StatusStaleSeconds * float64(time.Second))
}
func (c Config) PollInterval() time.Duration {
	return time.Duration(c.ContextPollSeconds * float64(time.Second))
}
func (c Config) NetworkRefresh() time.Duration {
	return time.Duration(c.NetworkRefreshSeconds * float64(time.Second))
}
func (c Config) NetworkStale() time.Duration {
	return time.Duration(c.NetworkStaleSeconds * float64(time.Second))
}
func (c Config) HTTPTimeout() time.Duration {
	return time.Duration(c.HTTPTimeoutSeconds * float64(time.Second))
}

func (c Config) DailyStaticEvaluator() (workpolicy.StaticEvaluator, error) {
	return workpolicy.NewStaticEvaluator(workpolicy.StaticTargets{
		StandardMinimum: hours(c.WorkTargets.DailyStandardMinimumHours),
		StandardTarget:  hours(c.WorkTargets.DailyTargetHours),
		OvertimeRule:    c.WorkTargets.OvertimeRule,
	})
}

func (c Config) WeeklyStaticEvaluator() (workpolicy.StaticEvaluator, error) {
	days := float64(c.WorkTargets.WorkdaysPerWeek)
	return workpolicy.NewStaticEvaluator(workpolicy.StaticTargets{
		StandardMinimum: hours(c.WorkTargets.DailyStandardMinimumHours * days),
		StandardTarget:  hours(c.WorkTargets.DailyTargetHours * days),
		OvertimeRule:    c.WorkTargets.OvertimeRule,
	})
}

func (c Config) DailyTarget() time.Duration { return hours(c.WorkTargets.DailyTargetHours) }

func applyLegacyWorkTargetAliases(data []byte, c *Config) error {
	var shape struct {
		WorkTargets json.RawMessage `json:"work_targets"`
		TargetHours *float64        `json:"target_hours"`
		WorkPolicy  *struct {
			DailyThresholdHours *float64 `json:"daily_threshold_hours"`
			WorkdaysPerWeek     *int     `json:"workdays_per_week"`
		} `json:"work_policy"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		return err
	}
	if len(shape.WorkTargets) != 0 && string(shape.WorkTargets) != "null" {
		return nil
	}
	if shape.TargetHours != nil {
		c.WorkTargets.DailyTargetHours = *shape.TargetHours
	}
	if shape.WorkPolicy != nil {
		if shape.WorkPolicy.DailyThresholdHours != nil {
			c.WorkTargets.DailyStandardMinimumHours = *shape.WorkPolicy.DailyThresholdHours
		}
		if shape.WorkPolicy.WorkdaysPerWeek != nil {
			c.WorkTargets.WorkdaysPerWeek = *shape.WorkPolicy.WorkdaysPerWeek
		}
	}
	return nil
}

func hours(value float64) time.Duration {
	return time.Duration(value * float64(time.Hour))
}

func (c Config) ReportingPeriod(now time.Time) (time.Time, time.Time, error) {
	loc, err := c.Location()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	now = now.In(loc)
	clock, _ := time.Parse("15:04", c.DayBoundary)
	start := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	if now.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return start, now, nil
}
