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
	if err := fs.Parse(args[1:]); err != nil {
		return "", o, err
	}
	if fs.NArg() != 0 {
		return "", o, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
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
		return runWeek(ctx, cfg, opt.json, stdout, stderr)
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
	fmt.Fprintln(stdout, "WorkTracker collector running. Press Ctrl+C to stop.")
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
	fmt.Fprintln(stdout, "WorkTracker tray enabled. Click the icon for current status.")
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

func currentSegment(r model.DayReport) *model.Segment {
	if len(r.Timeline) > 0 {
		return &r.Timeline[len(r.Timeline)-1]
	}
	return nil
}

func printStatus(w io.Writer, cfg config.Config, r model.DayReport) {
	status := reporting.StatusFromDay(cfg, r, time.Now())
	fmt.Fprintln(w, "WORKTRACKER STATUS")
	fmt.Fprintln(w, "==================")
	fmt.Fprintln(w, "State:     ", r.CurrentState)
	if s := currentSegment(r); s != nil {
		fmt.Fprintf(w, "Location:   %s (%s)\n", s.Location, s.LocationEvidence)
		fmt.Fprintf(w, "Foreground: %s", empty(s.ForegroundApp, "unavailable"))
		if s.ForegroundTitle != "" {
			fmt.Fprintf(w, " — %s", s.ForegroundTitle)
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Passive evidence:")
		if len(s.PassiveEvidence) == 0 {
			fmt.Fprintln(w, "  none available")
		} else {
			ids := make([]string, 0, len(s.PassiveEvidence))
			for id := range s.PassiveEvidence {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				o := s.PassiveEvidence[id]
				fmt.Fprintf(w, "  %s — %s (available=%t, passive-work=%t)\n", id, o.State, o.Available, o.PassiveWork)
			}
			fmt.Fprintln(w, "Health:     ", s.Health)
		}
	}
	printTotals(w, r.Totals)
	remaining := status.RemainingSeconds
	fmt.Fprintln(w, "Remaining:  ", duration(remaining))
	fmt.Fprintf(w, "Finish est: %s (excludes future breaks)\n", time.Now().Add(time.Duration(remaining*float64(time.Second))).Format("15:04"))
}
func printToday(w io.Writer, r model.DayReport, cfg config.Config) {
	fmt.Fprintln(w, "TODAY", r.Date)
	printTotals(w, r.Totals)
	if r.AutoEnded {
		fmt.Fprintln(w, "Auto-ended: ", r.End.In(mustLocation(cfg)).Format("15:04"))
	}
	fmt.Fprintln(w, "\nTIMELINE")
	for _, s := range r.Timeline {
		if !r.Start.IsZero() && s.End.After(r.Start) {
			fmt.Fprintf(w, "%s-%s  %-9s %-7s %-18s", s.Start.In(mustLocation(cfg)).Format("15:04"), s.End.In(mustLocation(cfg)).Format("15:04"), s.State, s.Location, empty(s.ForegroundApp, "—"))
			if o, ok := s.PassiveEvidence["vlc"]; ok {
				fmt.Fprintf(w, "  VLC:%s", o.State)
			}
			fmt.Fprintln(w)
		}
	}
}
func printTotals(w io.Writer, t model.Totals) {
	fmt.Fprintln(w, "Working:    ", duration(t.WorkingSeconds))
	fmt.Fprintln(w, "Break:      ", duration(t.BreakSeconds))
	fmt.Fprintln(w, "Untracked:  ", duration(t.UntrackedSeconds))
}

func runWeek(ctx context.Context, cfg config.Config, asJSON bool, stdout, stderr io.Writer) int {
	rs, err := reporting.New(cfg).Week(ctx, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "Weekly report failed:", err)
		return 2
	}
	if asJSON {
		return writeJSON(stdout, rs)
	}
	fmt.Fprintln(stdout, "WEEK")
	for _, r := range rs {
		fmt.Fprintf(stdout, "%s  work %s  break %s  untracked %s\n", r.Date, duration(r.Totals.WorkingSeconds), duration(r.Totals.BreakSeconds), duration(r.Totals.UntrackedSeconds))
		for _, u := range r.Usage {
			fmt.Fprintf(stdout, "  %-18s %-30s W:%s B:%s U:%s\n", u.Executable, u.Title, duration(u.Durations.Working), duration(u.Durations.Break), duration(u.Durations.Untracked))
		}
	}
	return 0
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
