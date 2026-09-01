package classifier

import (
	"testing"
	"time"

	"worktracker/internal/model"
)

func tm(h, m int) time.Time { return time.Date(2026, 8, 17, h, m, 0, 0, time.UTC) }
func window(start, end time.Time, app string) model.WindowEvent {
	return model.WindowEvent{Start: start, End: end, App: app, Title: "title"}
}
func afk(start, end time.Time, status string) model.AFKEvent {
	return model.AFKEvent{Start: start, End: end, Status: status}
}
func contextEvent(start, end time.Time, loc model.Location, e model.LocationEvidence, vlc string) model.ContextEvent {
	apps := map[string]model.PassiveEvidence{}
	if vlc != "" {
		available := vlc == "playing" || vlc == "paused" || vlc == "stopped"
		apps["vlc"] = model.PassiveEvidence{Detector: "vlc", State: vlc, Available: available, PassiveWork: vlc == "playing", ObservedAt: start}
	}
	return model.ContextEvent{Start: start, End: end, SchemaVersion: 1, Location: loc, LocationEvidence: e, Health: model.Healthy, PassiveEvidence: apps}
}
func options() Options {
	return Options{AFKGrace: 2 * time.Minute, AutoEnd: 90 * time.Minute, LockApps: []string{"lockapp.exe"}, LockTitleContains: []string{"windows default lock screen"}}
}

func freshOptions() Options {
	o := options()
	o.EvidenceFreshness = 15 * time.Second
	return o
}

func TestClassificationScenarios(t *testing.T) {
	start, end := tm(9, 0), tm(9, 10)
	tests := []struct {
		name     string
		windows  []model.WindowEvent
		afks     []model.AFKEvent
		contexts []model.ContextEvent
		want     []model.WorkState
	}{
		{"remote active", []model.WindowEvent{window(start, end, "firefox.exe")}, []model.AFKEvent{afk(start, end, "not-afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "")}, []model.WorkState{model.Working}},
		{"remote afk grace then break", []model.WindowEvent{window(start, end, "firefox.exe")}, []model.AFKEvent{afk(start, end, "afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "")}, []model.WorkState{model.Working, model.Break}},
		{"remote vlc playing", []model.WindowEvent{window(start, end, "firefox.exe")}, []model.AFKEvent{afk(start, end, "afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "playing")}, []model.WorkState{model.Working}},
		{"remote vlc paused", []model.WindowEvent{window(start, end, "vlc.exe")}, []model.AFKEvent{afk(start, end, "afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "paused")}, []model.WorkState{model.Working, model.Break}},
		{"office afk unlocked", []model.WindowEvent{window(start, end, "excel.exe")}, nil, []model.ContextEvent{contextEvent(start, end, model.Office, model.Confirmed, "")}, []model.WorkState{model.Working}},
		{"office locked", []model.WindowEvent{{Start: start, End: end, App: "LockApp.exe", Title: "Windows Default Lock Screen"}}, nil, []model.ContextEvent{contextEvent(start, end, model.Office, model.Confirmed, "")}, []model.WorkState{model.Break}},
		{"missing window", nil, []model.AFKEvent{afk(start, end, "not-afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "")}, []model.WorkState{model.Untracked}},
		{"missing remote afk", []model.WindowEvent{window(start, end, "firefox.exe")}, nil, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "")}, []model.WorkState{model.Untracked}},
		{"stale office remains office", []model.WindowEvent{window(start, end, "excel.exe")}, nil, []model.ContextEvent{contextEvent(start, end, model.Office, model.Stale, "")}, []model.WorkState{model.Working}},
		{"fallback remote", []model.WindowEvent{window(start, end, "excel.exe")}, []model.AFKEvent{afk(start, end, "afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Fallback, "")}, []model.WorkState{model.Working, model.Break}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.windows, tt.afks, tt.contexts, start, end, options())
			if len(got) != len(tt.want) {
				t.Fatalf("states=%v; want %v", states(got), tt.want)
			}
			for i := range got {
				if got[i].State != tt.want[i] {
					t.Fatalf("states=%v; want %v", states(got), tt.want)
				}
			}
		})
	}
}

func TestForegroundSeparateFromBackgroundVLC(t *testing.T) {
	start, end := tm(9, 0), tm(9, 10)
	got := Build([]model.WindowEvent{window(start, end, "firefox.exe")}, []model.AFKEvent{afk(start, end, "afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "playing")}, start, end, options())
	if got[0].State != model.Working || got[0].ForegroundApp != "firefox.exe" || got[0].PassiveEvidence["vlc"].State != "playing" {
		t.Fatalf("unexpected segment: %#v", got[0])
	}
}

func TestExplicitNativeLockEvidenceUsesExistingLockRule(t *testing.T) {
	start, end := tm(9, 0), tm(9, 10)
	w := window(start, end, "unrelated.exe")
	w.Locked = true
	got := Build([]model.WindowEvent{w}, []model.AFKEvent{afk(start, end, "not-afk")}, []model.ContextEvent{contextEvent(start, end, model.Office, model.Confirmed, "")}, start, end, options())
	if len(got) != 1 || got[0].State != model.Break {
		t.Fatalf("explicit lock did not remain authoritative: %#v", got)
	}
}

func TestClassifierConsumesGenericMultiplePassiveEvidence(t *testing.T) {
	start, end := tm(9, 0), tm(9, 10)
	context := model.ContextEvent{
		Start: start, End: end, SchemaVersion: 1,
		Location: model.Remote, LocationEvidence: model.Confirmed, Health: model.Degraded,
		PassiveEvidence: map[string]model.PassiveEvidence{
			"failed-source": {Detector: "failed-source", State: "unavailable", Available: false},
			"training":      {Detector: "training", State: "presenting", Available: true, PassiveWork: true},
		},
	}
	got := Build([]model.WindowEvent{window(start, end, "firefox.exe")}, []model.AFKEvent{afk(start, end, "afk")}, []model.ContextEvent{context}, start, end, options())
	if got[len(got)-1].State != model.Working || !got[len(got)-1].PassiveEvidence["training"].PassiveWork || got[len(got)-1].PassiveEvidence["failed-source"].PassiveWork {
		t.Fatalf("generic evidence not consumed correctly: %#v", got)
	}
}

func TestMultipleBrowserEvidenceAndSharedFreshness(t *testing.T) {
	reportTime := tm(14, 8)
	start := reportTime.Add(-10 * time.Minute)
	w := []model.WindowEvent{{Start: reportTime.Add(-2 * time.Second), End: reportTime.Add(-2 * time.Second), App: "Code.exe"}}
	a := []model.AFKEvent{{Start: start, End: reportTime, Status: "afk"}}
	c := []model.ContextEvent{contextEvent(start, reportTime, model.Remote, model.Confirmed, "")}
	playing := model.PassiveEvidenceEvent{Start: reportTime.Add(-3 * time.Second), End: reportTime.Add(-3 * time.Second), Evidence: map[string]model.PassiveEvidence{"browser:firefox:1": {Detector: "browser:firefox:1", State: "playing", Available: true, PassiveWork: true}}}
	paused := model.PassiveEvidenceEvent{Start: reportTime.Add(-2 * time.Second), End: reportTime.Add(-2 * time.Second), Evidence: map[string]model.PassiveEvidence{"browser:chrome:2": {Detector: "browser:chrome:2", State: "paused", Available: true}}}

	segments := BuildWithPassive(w, a, c, []model.PassiveEvidenceEvent{paused, playing}, start, reportTime, freshOptions())
	current := segments[len(segments)-1]
	if current.State != model.Working || len(current.PassiveEvidence) != 2 || !current.PassiveEvidence["browser:firefox:1"].PassiveWork || current.PassiveEvidence["browser:chrome:2"].PassiveWork {
		t.Fatalf("simultaneous browser evidence incorrect: %#v", current)
	}

	playing.Start, playing.End = reportTime.Add(-16*time.Second), reportTime.Add(-16*time.Second)
	segments = BuildWithPassive(w, a, c, []model.PassiveEvidenceEvent{paused, playing}, start, reportTime, freshOptions())
	current = segments[len(segments)-1]
	_, playingStillPresent := current.PassiveEvidence["browser:firefox:1"]
	if current.State != model.Break || playingStillPresent {
		t.Fatalf("stale playing evidence remained active: %#v", current)
	}
}

func TestNewerTabObservationDoesNotSuppressOtherBrowser(t *testing.T) {
	reportTime := tm(14, 8)
	start := reportTime.Add(-10 * time.Minute)
	w := []model.WindowEvent{{Start: reportTime.Add(-time.Second), End: reportTime.Add(-time.Second), App: "Code.exe"}}
	a := []model.AFKEvent{{Start: start, End: reportTime, Status: "afk"}}
	c := []model.ContextEvent{contextEvent(start, reportTime, model.Remote, model.Confirmed, "")}
	events := []model.PassiveEvidenceEvent{
		{Start: reportTime.Add(-5 * time.Second), End: reportTime.Add(-5 * time.Second), Evidence: map[string]model.PassiveEvidence{"browser:firefox:1": {Detector: "browser:firefox:1", State: "playing", Available: true, PassiveWork: true}}},
		{Start: reportTime.Add(-2 * time.Second), End: reportTime.Add(-2 * time.Second), Evidence: map[string]model.PassiveEvidence{"browser:firefox:1": {Detector: "browser:firefox:1", State: "paused", Available: true}}},
		{Start: reportTime.Add(-3 * time.Second), End: reportTime.Add(-3 * time.Second), Evidence: map[string]model.PassiveEvidence{"browser:chrome:9": {Detector: "browser:chrome:9", State: "playing", Available: true, PassiveWork: true}}},
	}
	segments := BuildWithPassive(w, a, c, events, start, reportTime, freshOptions())
	current := segments[len(segments)-1]
	if current.State != model.Working || current.PassiveEvidence["browser:firefox:1"].State != "paused" || !current.PassiveEvidence["browser:chrome:9"].PassiveWork {
		t.Fatalf("independent browser/tab selection failed: %#v", current)
	}
}

func TestNetworkSwitch(t *testing.T) {
	start, end := tm(9, 0), tm(9, 20)
	mid := tm(9, 10)
	got := Build([]model.WindowEvent{window(start, end, "excel.exe")}, []model.AFKEvent{afk(start, end, "afk")}, []model.ContextEvent{contextEvent(start, mid, model.Office, model.Confirmed, ""), contextEvent(mid, end, model.Remote, model.Confirmed, "")}, start, end, options())
	if states(got)[0] != model.Working || states(got)[len(got)-1] != model.Break {
		t.Fatalf("states=%v", states(got))
	}
}

func TestFragmentedAFKDoesNotResetGrace(t *testing.T) {
	start, end := tm(9, 0), tm(9, 10)
	got := Build([]model.WindowEvent{window(start, end, "x.exe")}, []model.AFKEvent{afk(start, tm(9, 1), "afk"), afk(tm(9, 1), end, "afk")}, []model.ContextEvent{contextEvent(start, end, model.Remote, model.Confirmed, "")}, start, end, options())
	if len(got) != 2 || got[0].End != tm(9, 2) || got[1].State != model.Break {
		t.Fatalf("segments=%#v", got)
	}
}

func TestAutoEndOnlyTrailing(t *testing.T) {
	segments := []model.Segment{{Start: tm(9, 0), End: tm(10, 0), State: model.Working}, {Start: tm(10, 0), End: tm(12, 0), State: model.Untracked}, {Start: tm(12, 0), End: tm(13, 0), State: model.Working}}
	r := Calculate(segments, tm(13, 0), 90*time.Minute)
	if r.AutoEnded || r.End != tm(13, 0) || r.Totals.WorkingSeconds != 7200 || r.Totals.UntrackedSeconds != 7200 {
		t.Fatalf("midday work discarded: %#v", r)
	}
	segments = append(segments, model.Segment{Start: tm(13, 0), End: tm(15, 0), State: model.Break})
	r = Calculate(segments, tm(15, 0), 90*time.Minute)
	if !r.AutoEnded || r.End != tm(13, 0) || r.Totals.WorkingSeconds != 7200 {
		t.Fatalf("trailing auto-end failed: %#v", r)
	}
}

func states(xs []model.Segment) []model.WorkState {
	out := make([]model.WorkState, len(xs))
	for i := range xs {
		out[i] = xs[i].State
	}
	return out
}

func TestZeroDurationWindowHeartbeatFreshness(t *testing.T) {
	reportTime := tm(14, 8)
	start := reportTime.Add(-time.Minute)
	context := contextEvent(start, reportTime.Add(-3*time.Second), model.Office, model.Confirmed, "")

	freshHeartbeat := model.WindowEvent{Start: reportTime.Add(-3 * time.Second), End: reportTime.Add(-3 * time.Second), App: "Code.exe", Title: "editor"}
	segments := Build([]model.WindowEvent{freshHeartbeat}, nil, []model.ContextEvent{context}, start, reportTime, freshOptions())
	current := segments[len(segments)-1]
	if current.State != model.Working || current.ForegroundApp != "Code.exe" || current.Location != model.Office || current.LocationEvidence != model.Confirmed {
		t.Fatalf("fresh zero-duration heartbeat lost: %#v", current)
	}

	staleHeartbeat := model.WindowEvent{Start: reportTime.Add(-16 * time.Second), End: reportTime.Add(-16 * time.Second), App: "Code.exe"}
	segments = Build([]model.WindowEvent{staleHeartbeat}, nil, []model.ContextEvent{context}, start, reportTime, freshOptions())
	current = segments[len(segments)-1]
	if current.State != model.Untracked || current.ForegroundApp != "" {
		t.Fatalf("stale heartbeat remained available: %#v", current)
	}
}

func TestContextAndDetectorFreshness(t *testing.T) {
	reportTime := tm(14, 8)
	start := reportTime.Add(-10 * time.Minute)
	window := model.WindowEvent{Start: reportTime.Add(-3 * time.Second), End: reportTime.Add(-3 * time.Second), App: "Code.exe"}
	afks := []model.AFKEvent{{Start: start, End: reportTime.Add(-3 * time.Second), Status: "afk"}}

	freshPaused := contextEvent(start, reportTime.Add(-3*time.Second), model.Remote, model.Confirmed, "paused")
	segments := Build([]model.WindowEvent{window}, afks, []model.ContextEvent{freshPaused}, start, reportTime, freshOptions())
	current := segments[len(segments)-1]
	paused := current.PassiveEvidence["vlc"]
	if current.State != model.Break || current.Location != model.Remote || paused.State != "paused" || !paused.Available || paused.PassiveWork {
		t.Fatalf("fresh paused evidence incorrect: %#v", current)
	}

	freshPlaying := contextEvent(start, reportTime.Add(-3*time.Second), model.Remote, model.Confirmed, "playing")
	segments = Build([]model.WindowEvent{window}, afks, []model.ContextEvent{freshPlaying}, start, reportTime, freshOptions())
	current = segments[len(segments)-1]
	if current.State != model.Working || !current.PassiveEvidence["vlc"].PassiveWork {
		t.Fatalf("fresh playing evidence did not qualify: %#v", current)
	}

	stalePlaying := contextEvent(start, reportTime.Add(-16*time.Second), model.Office, model.Confirmed, "playing")
	segments = Build([]model.WindowEvent{window}, afks, []model.ContextEvent{stalePlaying}, start, reportTime, freshOptions())
	current = segments[len(segments)-1]
	if current.Location != model.Remote || current.LocationEvidence != model.Fallback || len(current.PassiveEvidence) != 0 || current.State != model.Break {
		t.Fatalf("stale context supplied evidence: %#v", current)
	}
}

func TestOldStartLongAFKRemainsFresh(t *testing.T) {
	reportTime := tm(14, 8)
	start := tm(13, 0)
	w := model.WindowEvent{Start: reportTime.Add(-4 * time.Second), End: reportTime.Add(-3 * time.Second), App: "Code.exe"}
	a := model.AFKEvent{Start: tm(13, 20), End: reportTime.Add(-2 * time.Second), Status: "not-afk"}
	c := contextEvent(start, reportTime.Add(-2*time.Second), model.Remote, model.Confirmed, "paused")
	segments := Build([]model.WindowEvent{w}, []model.AFKEvent{a}, []model.ContextEvent{c}, start, reportTime, freshOptions())
	current := segments[len(segments)-1]
	if current.State != model.Working || current.ForegroundApp != "Code.exe" {
		t.Fatalf("long AFK event treated by old start instead of end: %#v", current)
	}
}

func TestOlderLongEventSurvivesNewerStaleEventsWithIndexedLookup(t *testing.T) {
	reportTime := tm(14, 8)
	start := tm(13, 0)
	windows := []model.WindowEvent{
		{Start: tm(13, 20), End: reportTime, App: "long-running.exe"},
		{Start: tm(14, 0), End: tm(14, 0), App: "stale.exe"},
	}
	contexts := []model.ContextEvent{
		contextEvent(tm(13, 20), reportTime, model.Office, model.Confirmed, "paused"),
		contextEvent(tm(14, 0), tm(14, 0), model.Remote, model.Confirmed, "playing"),
	}
	segments := Build(windows, nil, contexts, start, reportTime, freshOptions())
	current := segments[len(segments)-1]
	if current.State != model.Working || current.ForegroundApp != "long-running.exe" || current.Location != model.Office {
		t.Fatalf("older long-duration evidence was lost behind stale newer events: %#v", current)
	}
}

func TestRapidForegroundSwitchingSelectsLatestFreshEvidence(t *testing.T) {
	reportTime := tm(14, 8)
	start := reportTime.Add(-time.Minute)
	windows := []model.WindowEvent{
		{Start: tm(14, 7).Add(47 * time.Second), End: tm(14, 7).Add(48*time.Second + 40*time.Millisecond), App: "Code.exe"},
		{Start: tm(14, 7).Add(40 * time.Second), End: tm(14, 7).Add(40 * time.Second), App: "Code.exe"},
		{Start: tm(14, 7).Add(28 * time.Second), End: tm(14, 7).Add(39*time.Second + 360*time.Millisecond), App: "firefox.exe"},
		{Start: tm(14, 7).Add(46 * time.Second), End: tm(14, 7).Add(46 * time.Second), App: "firefox.exe"},
		{Start: tm(14, 7).Add(41 * time.Second), End: tm(14, 7).Add(45*time.Second + 160*time.Millisecond), App: "Code.exe"},
	}
	c := contextEvent(start, reportTime, model.Office, model.Confirmed, "")
	segments := Build(windows, nil, []model.ContextEvent{c}, start, reportTime, freshOptions())
	current := segments[len(segments)-1]
	if current.State != model.Working || current.ForegroundApp != "Code.exe" {
		t.Fatalf("latest foreground not selected: %#v", current)
	}
}

func TestCapturedLiveOfficeCase(t *testing.T) {
	reportTime := tm(14, 8)
	start := tm(13, 0)
	windows := []model.WindowEvent{
		{Start: tm(14, 7).Add(47 * time.Second), End: tm(14, 7).Add(48*time.Second + 40*time.Millisecond), App: "Code.exe", Title: "WorkChronicle"},
		{Start: tm(14, 7).Add(46 * time.Second), End: tm(14, 7).Add(46 * time.Second), App: "firefox.exe"},
		{Start: tm(14, 7).Add(41 * time.Second), End: tm(14, 7).Add(45*time.Second + 160*time.Millisecond), App: "Code.exe"},
		{Start: tm(14, 7).Add(40 * time.Second), End: tm(14, 7).Add(40 * time.Second), App: "Code.exe"},
		{Start: tm(14, 7).Add(28 * time.Second), End: tm(14, 7).Add(39*time.Second + 360*time.Millisecond), App: "firefox.exe"},
	}
	afks := []model.AFKEvent{{Start: tm(13, 20).Add(24 * time.Second), End: tm(14, 7).Add(54*time.Second + 630*time.Millisecond), Status: "not-afk"}}
	contexts := []model.ContextEvent{contextEvent(tm(14, 7).Add(27*time.Second), tm(14, 8).Add(7*time.Second), model.Office, model.Confirmed, "paused")}
	segments := Build(windows, afks, contexts, start, reportTime, freshOptions())
	report := Calculate(segments, reportTime, 90*time.Minute)
	current := segments[len(segments)-1]
	vlc := current.PassiveEvidence["vlc"]
	if report.CurrentState != model.Working || current.Location != model.Office || current.LocationEvidence != model.Confirmed || current.ForegroundApp != "Code.exe" || vlc.State != "paused" || !vlc.Available || vlc.PassiveWork {
		t.Fatalf("captured live case misclassified: report=%#v current=%#v", report, current)
	}
}
