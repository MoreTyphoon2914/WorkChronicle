package nativewatcher

import (
	"context"
	"fmt"
	"strings"
	"time"

	"worktracker/internal/coreprotocol"
)

type Foreground struct {
	Executable string
	Title      string
}

type Reader interface {
	Foreground(context.Context) (Foreground, error)
	IdleDuration(context.Context) (time.Duration, error)
	SessionLocked(context.Context) (bool, error)
}

type ComponentResult struct {
	Connected       bool
	LastObservation *time.Time
	Error           error
}

type Result struct {
	Window     *coreprotocol.WindowObservation
	AFK        *coreprotocol.AFKObservation
	Session    *coreprotocol.SessionObservation
	Foreground ComponentResult
	Input      ComponentResult
	SessionAPI ComponentResult
}

type Watcher struct {
	reader       Reader
	afkThreshold time.Duration
	maxGap       time.Duration
	lastPoll     time.Time
	window       intervalState
	afk          intervalState
}

type intervalState struct {
	key   string
	start time.Time
}

func New(reader Reader, afkThreshold, maxGap time.Duration) (*Watcher, error) {
	if reader == nil {
		return nil, fmt.Errorf("native reader is required")
	}
	if afkThreshold <= 0 || maxGap <= 0 {
		return nil, fmt.Errorf("native AFK threshold and maximum gap must be positive")
	}
	return &Watcher{reader: reader, afkThreshold: afkThreshold, maxGap: maxGap}, nil
}

func (w *Watcher) Observe(ctx context.Context, at time.Time) Result {
	at = at.UTC()
	if !w.lastPoll.IsZero() && (at.Before(w.lastPoll) || at.Sub(w.lastPoll) > w.maxGap) {
		w.window = intervalState{}
		w.afk = intervalState{}
	}
	w.lastPoll = at

	locked, sessionErr := w.reader.SessionLocked(ctx)
	result := Result{SessionAPI: component(at, sessionErr)}
	if sessionErr == nil {
		result.Session = &coreprotocol.SessionObservation{ObservedAt: at, Locked: locked, Source: coreprotocol.SourceNativeWindows}
	}

	foreground, foregroundErr := w.reader.Foreground(ctx)
	if sessionErr == nil && locked {
		foreground = Foreground{Executable: "LockApp.exe"}
		foregroundErr = nil
	}
	result.Foreground = component(at, foregroundErr)
	if foregroundErr == nil && strings.TrimSpace(foreground.Executable) != "" {
		key := strings.ToLower(foreground.Executable) + "\x00" + foreground.Title + fmt.Sprintf("\x00%t", locked)
		start := w.window.update(key, at)
		result.Window = &coreprotocol.WindowObservation{Start: start, End: at, Executable: foreground.Executable, Title: foreground.Title, Source: coreprotocol.SourceNativeWindows, Locked: locked}
	} else {
		w.window = intervalState{}
	}

	idle, inputErr := w.reader.IdleDuration(ctx)
	result.Input = component(at, inputErr)
	if inputErr == nil {
		status := "not-afk"
		if idle >= w.afkThreshold {
			status = "afk"
		}
		start := w.afk.update(status, at)
		result.AFK = &coreprotocol.AFKObservation{Start: start, End: at, Status: status, Source: coreprotocol.SourceNativeWindows}
	} else {
		w.afk = intervalState{}
	}
	return result
}

func (s *intervalState) update(key string, at time.Time) time.Time {
	if s.key != key || s.start.IsZero() {
		s.key = key
		s.start = at
	}
	return s.start
}

func component(at time.Time, err error) ComponentResult {
	if err != nil {
		return ComponentResult{Error: err}
	}
	value := at
	return ComponentResult{Connected: true, LastObservation: &value}
}
