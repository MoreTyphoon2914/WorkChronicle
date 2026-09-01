package hostagent

import (
	"sync"
	"time"

	"worktracker/internal/coreprotocol"
	"worktracker/internal/nativewatcher"
)

type acquisitionState struct {
	mu                   sync.Mutex
	diagnostics          coreprotocol.AcquisitionDiagnostics
	tolerance            time.Duration
	lockApps, lockTitles []string
	window               *coreprotocol.WindowObservation
	afk                  *coreprotocol.AFKObservation
	native               nativewatcher.Result
}

func newAcquisitionState(mode string, tolerance time.Duration, lockApps, lockTitles []string) *acquisitionState {
	state := &acquisitionState{tolerance: tolerance, lockApps: append([]string(nil), lockApps...), lockTitles: append([]string(nil), lockTitles...)}
	state.diagnostics.Mode = mode
	state.diagnostics.ActivityWatch.Enabled = usesActivityWatch(mode)
	state.diagnostics.NativeForeground.Enabled = mode == "shadow" || mode == "native"
	state.diagnostics.NativeAFK.Enabled = state.diagnostics.NativeForeground.Enabled
	state.diagnostics.NativeSession.Enabled = state.diagnostics.NativeForeground.Enabled
	return state
}

func (s *acquisitionState) updateActivityWatch(windows []coreprotocol.WindowObservation, afk []coreprotocol.AFKObservation, err error, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.diagnostics.ActivityWatch.Connected = false
		s.diagnostics.ActivityWatch.Message = "unavailable: " + err.Error()
	} else {
		s.diagnostics.ActivityWatch.Connected = true
		s.diagnostics.ActivityWatch.Message = ""
		if last := latestAuthoritativeObservation(windows, afk); last != nil {
			s.diagnostics.ActivityWatch.LastObservation = last
		}
		if value := newestWindow(windows); value != nil {
			copy := *value
			s.window = &copy
		}
		if value := newestAFK(afk); value != nil {
			copy := *value
			s.afk = &copy
		}
	}
	s.compare(at)
}

func (s *acquisitionState) updateNative(result nativewatcher.Result, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.native = result
	applyComponent(&s.diagnostics.NativeForeground, result.Foreground)
	applyComponent(&s.diagnostics.NativeAFK, result.Input)
	applyComponent(&s.diagnostics.NativeSession, result.SessionAPI)
	s.compare(at)
}

func (s *acquisitionState) compare(at time.Time) {
	if s.diagnostics.Mode != "shadow" {
		s.diagnostics.Comparison = nil
		return
	}
	var windows []coreprotocol.WindowObservation
	var afk []coreprotocol.AFKObservation
	if s.window != nil {
		windows = append(windows, *s.window)
	}
	if s.afk != nil {
		afk = append(afk, *s.afk)
	}
	s.diagnostics.Comparison = compareSources(windows, afk, s.native, at, s.tolerance, s.lockApps, s.lockTitles)
}

func (s *acquisitionState) snapshot() *coreprotocol.AcquisitionDiagnostics {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := s.diagnostics
	if s.diagnostics.Comparison != nil {
		comparison := *s.diagnostics.Comparison
		copy.Comparison = &comparison
	}
	return &copy
}

func applyComponent(target *coreprotocol.SourceHealth, result nativewatcher.ComponentResult) {
	target.Connected = result.Connected
	if result.LastObservation != nil {
		target.LastObservation = result.LastObservation
	}
	target.Message = ""
	if result.Error != nil {
		target.Message = result.Error.Error()
	}
}

func newestWindow(items []coreprotocol.WindowObservation) *coreprotocol.WindowObservation {
	var result *coreprotocol.WindowObservation
	for i := range items {
		if result == nil || items[i].Start.After(result.Start) || (items[i].Start.Equal(result.Start) && items[i].End.After(result.End)) {
			result = &items[i]
		}
	}
	return result
}

func newestAFK(items []coreprotocol.AFKObservation) *coreprotocol.AFKObservation {
	var result *coreprotocol.AFKObservation
	for i := range items {
		if result == nil || items[i].Start.After(result.Start) || (items[i].Start.Equal(result.Start) && items[i].End.After(result.End)) {
			result = &items[i]
		}
	}
	return result
}
