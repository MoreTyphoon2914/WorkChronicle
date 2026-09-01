package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"worktracker/internal/appstate"
	"worktracker/internal/config"
	"worktracker/internal/health"
	"worktracker/internal/location"
	wtlog "worktracker/internal/logging"
	"worktracker/internal/model"
	"worktracker/internal/reporting"
	"worktracker/internal/tracker"
	"worktracker/internal/tray"
	wtwindows "worktracker/internal/windows"
)

type commandOptions struct {
	configPath string
	json       bool
	jsonV2     bool
	tray       bool
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func parseCommand(args []string, stderr io.Writer) (string, commandOptions, error) {
	if len(args) == 0 {
		return "", commandOptions{}, fmt.Errorf("command required: run, status, today, week, doctor, install, uninstall")
	}
	cmd := args[0]
	allowed := map[string]bool{"run": true, "status": true, "today": true, "week": true, "doctor": true, "install": true, "uninstall": true}
	if !allowed[cmd] {
		return "", commandOptions{}, fmt.Errorf("unknown command %q", cmd)
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o commandOptions
	fs.StringVar(&o.configPath, "config", "config.json", "configuration path")
	if cmd == "run" {
		fs.BoolVar(&o.tray, "tray", false, "show the Windows system tray UI")
	}
	if cmd == "status" || cmd == "today" || cmd == "week" || cmd == "doctor" {
		fs.BoolVar(&o.json, "json", false, "emit JSON")
	}
	if cmd == "week" {
		fs.BoolVar(&o.jsonV2, "json-v2", false, "emit the versioned weekly summary JSON object")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return "", o, err
	}
	if fs.NArg() != 0 {
		return "", o, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if o.json && o.jsonV2 {
		return "", o, fmt.Errorf("--json and --json-v2 are mutually exclusive")
	}
	return cmd, o, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd, opt, err := parseCommand(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "Error:", err)
		return 2
	}
	cfg, err := config.Load(opt.configPath)
	if err != nil {
		fmt.Fprintln(stderr, "Configuration error:", err)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch cmd {
	case "run":
		return runCollector(ctx, cfg, opt.tray, stdout, stderr)
	case "status", "today":
		return runDay(ctx, cfg, cmd, opt.json, stdout, stderr)
	case "week":
		return runWeek(ctx, cfg, opt.json, opt.jsonV2, stdout, stderr)
	case "doctor":
		return runDoctor(ctx, cfg, opt.json, stdout, stderr)
	case "install":
		exe, e := os.Executable()
		if e == nil {
			e = wtwindows.InstallTask(ctx, cfg.TaskName, exe, cfg.ConfigPath)
		}
		if e != nil {
			fmt.Fprintln(stderr, "Install failed:", e)
			return 2
		}
		fmt.Fprintln(stdout, "Scheduled task installed and started:", cfg.TaskName)
		return 0
	case "uninstall":
		if e := wtwindows.UninstallTask(ctx, cfg.TaskName); e != nil {
			fmt.Fprintln(stderr, "Uninstall failed:", e)
			return 2
		}
		fmt.Fprintln(stdout, "Scheduled task removed; config, logs, and ActivityWatch data were preserved.")
		return 0
	}
	return 2
}

func runCollector(ctx context.Context, cfg config.Config, withTray bool, stdout, stderr io.Writer) int {
	m, err := wtwindows.AcquireMutex(cfg.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer m.Close()
	logger, closer, err := wtlog.New(cfg.Logging, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "Logging error:", err)
		return 2
	}
	defer closer.Close()
	fmt.Fprintln(stdout, "WorkChronicle collector running. Press Ctrl+C to stop.")
	collector := tracker.NewCollector(cfg, logger)
	if !withTray {
		err = collector.Run(ctx)
		if err != nil && err != context.Canceled {
			fmt.Fprintln(stderr, "Collector stopped:", err)
			return 2
		}
		return 0
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	collectorDone := make(chan error, 1)
	go func() {
		collectorDone <- collector.Run(runCtx)
		cancel()
	}()
	service := reporting.New(cfg)
	provider := tray.ProviderFunc(func(ctx context.Context) (model.StatusReport, error) {
		return service.Status(ctx, time.Now())
	})
	ui := tray.NewNative(tray.NativeOptions{
		ReportsDir: filepath.Join(filepath.Dir(cfg.ConfigPath), "reports"),
		LogsDir:    filepath.Dir(cfg.Logging.File),
	})
	fmt.Fprintln(stdout, "WorkChronicle tray enabled. Click the icon for current status.")
	trayErr := (tray.Controller{Provider: provider, UI: ui}).Run(runCtx, cancel)
	cancel()
	err = <-collectorDone
	if trayErr != nil && trayErr != context.Canceled {
		fmt.Fprintln(stderr, "Tray stopped:", trayErr)
		return 2
	}
	if err != nil && err != context.Canceled {
		fmt.Fprintln(stderr, "Collector stopped:", err)
		return 2
	}
	return 0
}

func runDay(ctx context.Context, cfg config.Config, cmd string, asJSON bool, stdout, stderr io.Writer) int {
	r, err := reporting.New(cfg).Today(ctx, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "ActivityWatch report failed:", err)
		return 2
	}
	if cmd == "status" {
		status := reporting.StatusFromDay(cfg, r, time.Now())
		if asJSON {
			return writeJSON(stdout, status)
		}
		printStatus(stdout, cfg, r)
	} else {
		if asJSON {
			return writeJSON(stdout, r)
		}
		printToday(stdout, r, cfg)
	}
	return 0
}

func printStatus(w io.Writer, cfg config.Config, r model.DayReport) {
	status := reporting.StatusFromDay(cfg, r, time.Now())
	fmt.Fprintln(w, "WORKCHRONICLE STATUS")
	fmt.Fprintln(w, "==================")
	fmt.Fprintln(w, "State:     ", status.WorkState)
	fmt.Fprintf(w, "Location:   %s (%s)\n", status.Location, status.LocationEvidence)
	fmt.Fprintln(w, "Foreground:", empty(status.Foreground.Executable, "unavailable"))
	fmt.Fprintln(w, "Passive evidence:")
	if len(status.PassiveDetectorEvidence) == 0 {
		fmt.Fprintln(w, "  none available")
	} else {
		ids := make([]string, 0, len(status.PassiveDetectorEvidence))
		for id := range status.PassiveDetectorEvidence {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			o := status.PassiveDetectorEvidence[id]
			fmt.Fprintf(w, "  %s — %s (available=%t, passive-work=%t)\n", id, o.State, o.Available, o.PassiveWork)
		}
	}
	fmt.Fprintln(w, "Health:     ", status.TrackerHealth)
	printTotals(w, r.Totals)
	fmt.Fprintln(w, "Evaluation: ", status.WorkEvaluation.Band)
	fmt.Fprintln(w, "Remaining:  ", duration(status.RemainingSeconds))
	if status.EstimatedFinish != nil {
		fmt.Fprintf(w, "Finish est: %s (assumes no additional breaks)\n", status.EstimatedFinish.In(mustLocation(cfg)).Format("15:04"))
	} else {
		fmt.Fprintln(w, "Finish est: —")
	}
}
func printToday(w io.Writer, r model.DayReport, cfg config.Config) {
	fmt.Fprintln(w, "TODAY", r.Date)
	fmt.Fprintln(w, "First work: ", reportTime(r.FirstWorkAt, cfg))
	fmt.Fprintln(w, "Last work:  ", reportTime(r.LastWorkAt, cfg))
	fmt.Fprintln(w, "Report end: ", r.ReportEnd.In(mustLocation(cfg)).Format("15:04"))
	printTotals(w, r.Totals)
	fmt.Fprintln(w, "Evaluation: ", r.WorkBand)
	fmt.Fprintln(w, "Target:     ", duration(r.StandardTargetSeconds))
	fmt.Fprintln(w, "Remaining:  ", duration(r.RemainingTargetSeconds))
	fmt.Fprintln(w, "Overtime:   ", duration(r.OvertimeSeconds))
	if r.Live {
		fmt.Fprintln(w, "Current:    ", r.CurrentState)
		if r.EstimatedFinish != nil {
			fmt.Fprintf(w, "Finish est: %s (assumes no additional breaks)\n", r.EstimatedFinish.In(mustLocation(cfg)).Format("15:04"))
		} else {
			fmt.Fprintln(w, "Finish est: —")
		}
	} else {
		fmt.Fprintln(w, "Final:      ", r.FinalState)
	}
	if r.AutoEnded {
		fmt.Fprintln(w, "Auto-ended: ", r.End.In(mustLocation(cfg)).Format("15:04"))
	}
	fmt.Fprintln(w, "\nTIMELINE")
	for _, s := range r.Timeline {
		fmt.Fprintf(w, "%s-%s  %-9s %-7s %-18s", s.Start.In(mustLocation(cfg)).Format("15:04"), s.End.In(mustLocation(cfg)).Format("15:04"), s.State, s.Location, empty(s.ForegroundApp, "—"))
		if o, ok := s.PassiveEvidence["vlc"]; ok {
			fmt.Fprintf(w, "  VLC:%s", o.State)
		}
		fmt.Fprintln(w)
	}
}

func reportTime(value *time.Time, cfg config.Config) string {
	if value == nil {
		return "—"
	}
	return value.In(mustLocation(cfg)).Format("15:04")
}
func printTotals(w io.Writer, t model.Totals) {
	fmt.Fprintln(w, "Working:    ", duration(t.WorkingSeconds))
	fmt.Fprintln(w, "Break:      ", duration(t.BreakSeconds))
	fmt.Fprintln(w, "Untracked:  ", duration(t.UntrackedSeconds))
}

func runWeek(ctx context.Context, cfg config.Config, asJSON, asJSONV2 bool, stdout, stderr io.Writer) int {
	r, err := reporting.New(cfg).WeekReport(ctx, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "Weekly report failed:", err)
		return 2
	}
	if asJSON {
		// Preserve the established top-level JSON array contract. The richer
		// WeekReport model drives human presentation and future versioned APIs.
		return writeJSON(stdout, r.Days)
	}
	if asJSONV2 {
		return writeJSON(stdout, r)
	}
	printWeek(stdout, r, cfg)
	return 0
}

func printWeek(stdout io.Writer, r model.WeekReport, cfg config.Config) {
	fmt.Fprintf(stdout, "WEEK %s to %s\n", r.PeriodStart.In(mustLocation(cfg)).Format("2006-01-02"), r.PeriodEnd.In(mustLocation(cfg)).Format("2006-01-02"))
	for _, day := range r.Days {
		fmt.Fprintf(stdout, "%s  work %s  break %s  untracked %s\n", day.Date, duration(day.Totals.WorkingSeconds), duration(day.Totals.BreakSeconds), duration(day.Totals.UntrackedSeconds))
		for _, u := range usageByExecutable(day.Usage) {
			fmt.Fprintf(stdout, "  %-18s W:%s B:%s U:%s\n", u.Executable, duration(u.Durations.Working), duration(u.Durations.Break), duration(u.Durations.Untracked))
		}
	}
	fmt.Fprintln(stdout, "Totals:")
	printTotals(stdout, r.Totals)
	fmt.Fprintf(stdout, "Average work: %s across %d elapsed workday(s)\n", duration(r.AverageWorkingSeconds), r.AverageDenominator)
	fmt.Fprintln(stdout, "Evaluation:  ", r.WorkEvaluation.Band)
}

func usageByExecutable(usage []model.Usage) []model.Usage {
	byExecutable := make(map[string]*model.Usage)
	for _, entry := range usage {
		key := strings.ToLower(strings.TrimSpace(entry.Executable))
		if key == "" {
			continue
		}
		total := byExecutable[key]
		if total == nil {
			total = &model.Usage{Executable: entry.Executable}
			byExecutable[key] = total
		}
		total.Durations.Working += entry.Durations.Working
		total.Durations.Break += entry.Durations.Break
		total.Durations.Untracked += entry.Durations.Untracked
	}
	out := make([]model.Usage, 0, len(byExecutable))
	for _, entry := range byExecutable {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Executable) < strings.ToLower(out[j].Executable)
	})
	return out
}

type doctorReport struct {
	Overall model.HealthLevel       `json:"overall"`
	Checks  []model.ComponentHealth `json:"checks"`
}

func runDoctor(ctx context.Context, cfg config.Config, asJSON bool, stdout, stderr io.Writer) int {
	now := time.Now().UTC()
	checks := []model.ComponentHealth{{Name: "config", Level: model.Healthy, Message: "valid"}}
	svc := reporting.New(cfg)
	b, err := svc.ResolveBuckets(ctx)
	if err != nil {
		checks = append(checks, model.ComponentHealth{Name: "activitywatch", Level: model.Failed, Message: err.Error()})
	} else {
		checks = append(checks, model.ComponentHealth{Name: "activitywatch", Level: model.Healthy, Message: "window=" + b.Window + ", afk=" + b.AFK})
		level := model.Healthy
		msg := "context bucket present"
		if b.Context == "" {
			level = model.Degraded
			msg = "context bucket missing or collector has not run"
		}
		checks = append(checks, model.ComponentHealth{Name: "context", Level: level, Message: msg})
		for _, source := range []struct{ name, id string }{{"window_freshness", b.Window}, {"afk_freshness", b.AFK}, {"context_freshness", b.Context}} {
			if source.id == "" {
				continue
			}
			event, e := svc.AW.Latest(ctx, source.id)
			if e != nil {
				checks = append(checks, model.ComponentHealth{Name: source.name, Level: model.Failed, Message: e.Error()})
				continue
			}
			eventEnd := event.Timestamp.Add(time.Duration(event.Duration * float64(time.Second)))
			age := now.Sub(eventEnd)
			level := model.Healthy
			if !model.EvidenceFreshAt(eventEnd, now, cfg.StatusStale()) {
				level = model.Degraded
			}
			checks = append(checks, model.ComponentHealth{Name: source.name, Level: level, Message: fmt.Sprintf("last evidence %.0fs ago", max(0, age.Seconds())), LastSuccess: &eventEnd})
		}
	}
	nd := location.New(location.PowerShellRunner{}, cfg.OfficeGatewayMACs, cfg.HomeGatewayMACs, cfg.NetworkStale())
	nr, nerr := nd.Observe(ctx, now)
	nl := nr.Health
	msg := string(nr.Location) + " (" + string(nr.Evidence) + ")"
	if nerr != nil {
		msg += "; " + nerr.Error()
	}
	checks = append(checks, model.ComponentHealth{Name: "network", Level: nl, Message: msg})
	vd := &appstate.VLCDetector{URL: cfg.VLC.URL, Password: cfg.VLC.Password, Client: &http.Client{Timeout: cfg.HTTPTimeout()}}
	vo, verr := vd.Observe(ctx)
	vl := model.Healthy
	vmsg := vo.State
	if verr != nil {
		vl = model.Degraded
		vmsg = verr.Error()
	}
	checks = append(checks, model.ComponentHealth{Name: "vlc", Level: vl, Message: vmsg})
	exists, terr := wtwindows.TaskExists(ctx, cfg.TaskName)
	tl := model.Healthy
	tmsg := "installed"
	if terr != nil {
		tl = model.Degraded
		tmsg = terr.Error()
	} else if !exists {
		tl = model.Degraded
		tmsg = "not installed"
	}
	checks = append(checks, model.ComponentHealth{Name: "scheduled_task", Level: tl, Message: tmsg})
	logLevel, logMsg := checkLogDir(cfg.Logging.File)
	checks = append(checks, model.ComponentHealth{Name: "logging", Level: logLevel, Message: logMsg})
	r := doctorReport{Overall: health.Aggregate(checks), Checks: checks}
	if asJSON {
		writeJSON(stdout, r)
	} else {
		for _, c := range checks {
			label := "PASS"
			if c.Level == model.Degraded {
				label = "WARN"
			} else if c.Level == model.Failed {
				label = "FAIL"
			}
			fmt.Fprintf(stdout, "%-4s %-16s %s\n", label, c.Name, c.Message)
		}
		fmt.Fprintln(stdout, "Overall:", r.Overall)
	}
	if r.Overall == model.Failed {
		return 2
	}
	if r.Overall == model.Degraded {
		return 1
	}
	return 0
}
func checkLogDir(path string) (model.HealthLevel, string) {
	dir := filepath.Dir(path)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return model.Degraded, "log directory does not exist yet: " + dir
	}
	f, err := os.CreateTemp(dir, ".worktracker-doctor-*")
	if err != nil {
		return model.Failed, "log directory is not writable"
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return model.Healthy, "writable"
}
func writeJSON(w io.Writer, v any) int {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		return 2
	}
	return 0
}
func duration(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	d := time.Duration(seconds * float64(time.Second)).Round(time.Minute)
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}
func empty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
func mustLocation(c config.Config) *time.Location {
	l, e := c.Location()
	if e != nil {
		return time.Local
	}
	return l
}
