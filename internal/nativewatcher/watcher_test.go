package nativewatcher

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeReader struct {
	foreground Foreground
	idle       time.Duration
	locked     bool
	fgErr      error
	idleErr    error
	lockErr    error
}

func (f *fakeReader) Foreground(context.Context) (Foreground, error)      { return f.foreground, f.fgErr }
func (f *fakeReader) IdleDuration(context.Context) (time.Duration, error) { return f.idle, f.idleErr }
func (f *fakeReader) SessionLocked(context.Context) (bool, error)         { return f.locked, f.lockErr }

func TestWatcherBuildsForegroundAndAFKIntervalsAcrossTransitions(t *testing.T) {
	reader := &fakeReader{foreground: Foreground{Executable: "Code.exe", Title: "private title"}}
	watcher, _ := New(reader, 3*time.Minute, 10*time.Second)
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	first := watcher.Observe(context.Background(), t0)
	second := watcher.Observe(context.Background(), t0.Add(2*time.Second))
	if first.Window.Start != t0 || second.Window.Start != t0 || second.Window.End != t0.Add(2*time.Second) || second.AFK.Status != "not-afk" {
		t.Fatalf("active intervals were not extended: first=%#v second=%#v", first, second)
	}

	reader.foreground = Foreground{Executable: "firefox.exe", Title: "other"}
	reader.idle = 3 * time.Minute
	idle := watcher.Observe(context.Background(), t0.Add(4*time.Second))
	if idle.Window.Start != t0.Add(4*time.Second) || idle.AFK.Status != "afk" || idle.AFK.Start != t0.Add(4*time.Second) {
		t.Fatalf("transitions were not normalized: %#v", idle)
	}

	reader.idle = 0
	active := watcher.Observe(context.Background(), t0.Add(6*time.Second))
	if active.AFK.Status != "not-afk" || active.AFK.Start != t0.Add(6*time.Second) {
		t.Fatalf("return from idle was not a new interval: %#v", active.AFK)
	}
}

func TestWatcherHandlesRapidForegroundSwitchingWithoutOverlaps(t *testing.T) {
	reader := &fakeReader{foreground: Foreground{Executable: "Code.exe"}}
	watcher, _ := New(reader, time.Minute, 10*time.Second)
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	apps := []string{"Code.exe", "firefox.exe", "Code.exe", "explorer.exe"}
	for index, app := range apps {
		reader.foreground.Executable = app
		at := t0.Add(time.Duration(index) * 100 * time.Millisecond)
		result := watcher.Observe(context.Background(), at)
		if result.Window == nil || result.Window.Start != at || result.Window.End != at || result.Window.Executable != app {
			t.Fatalf("rapid switch %d was lost or overlapped: %#v", index, result.Window)
		}
	}
}

func TestWatcherNormalizesLockUnlockAndStartupLocked(t *testing.T) {
	reader := &fakeReader{foreground: Foreground{Executable: "Code.exe"}, locked: true}
	watcher, _ := New(reader, time.Minute, 10*time.Second)
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	locked := watcher.Observe(context.Background(), t0)
	if locked.Session == nil || !locked.Session.Locked || locked.Window == nil || !locked.Window.Locked || locked.Window.Executable != "LockApp.exe" {
		t.Fatalf("startup lock was not explicit: %#v", locked)
	}
	reader.locked = false
	unlocked := watcher.Observe(context.Background(), t0.Add(2*time.Second))
	if unlocked.Session.Locked || unlocked.Window.Locked || unlocked.Window.Executable != "Code.exe" || unlocked.Window.Start != t0.Add(2*time.Second) {
		t.Fatalf("unlock was not a new foreground interval: %#v", unlocked)
	}
}

func TestWatcherDoesNotBridgeRestartOrResumeGap(t *testing.T) {
	reader := &fakeReader{foreground: Foreground{Executable: "Code.exe"}}
	watcher, _ := New(reader, time.Minute, 5*time.Second)
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	watcher.Observe(context.Background(), t0)
	afterGap := watcher.Observe(context.Background(), t0.Add(30*time.Second))
	if afterGap.Window.Start != afterGap.Window.End || afterGap.AFK.Start != afterGap.AFK.End {
		t.Fatalf("gap was fabricated as observed time: %#v", afterGap)
	}
	newWatcher, _ := New(reader, time.Minute, 5*time.Second)
	afterRestart := newWatcher.Observe(context.Background(), t0.Add(time.Minute))
	if afterRestart.Window.Start != afterRestart.Window.End {
		t.Fatalf("restart should begin with a point observation: %#v", afterRestart.Window)
	}
	outOfOrder := watcher.Observe(context.Background(), t0.Add(-time.Second))
	if outOfOrder.Window.Start != outOfOrder.Window.End || outOfOrder.Window.End != t0.Add(-time.Second) {
		t.Fatalf("out-of-order clock sample produced an invalid interval: %#v", outOfOrder.Window)
	}
}

func TestWatcherComponentFailuresAreIndependent(t *testing.T) {
	reader := &fakeReader{foreground: Foreground{Executable: "Code.exe"}, idleErr: errors.New("input unavailable")}
	watcher, _ := New(reader, time.Minute, 5*time.Second)
	result := watcher.Observe(context.Background(), time.Now())
	if result.Window == nil || !result.Foreground.Connected || result.AFK != nil || result.Input.Connected || result.Session == nil || !result.SessionAPI.Connected {
		t.Fatalf("input failure affected independent components: %#v", result)
	}

	reader.idleErr = nil
	reader.fgErr = errors.New("foreground unavailable")
	result = watcher.Observe(context.Background(), time.Now().Add(time.Second))
	if result.Window != nil || result.AFK == nil || result.Session == nil {
		t.Fatalf("foreground failure affected independent components: %#v", result)
	}
}
