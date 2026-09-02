package nativewatcher

import (
	"sync"
	"time"
)

// SessionState is an acquisition fact. It deliberately contains no work-time
// classification or business-policy meaning.
type SessionState uint8

const (
	SessionUnknown SessionState = iota
	SessionLocked
	SessionUnlocked
)

func (s SessionState) String() string {
	switch s {
	case SessionLocked:
		return "LOCKED"
	case SessionUnlocked:
		return "UNLOCKED"
	default:
		return "UNKNOWN"
	}
}

type SessionTransition struct {
	State      SessionState
	ObservedAt time.Time
}

// sessionState gives WTS notifications permanent precedence over the desktop
// fallback. A conservative fallback may establish LOCKED at startup, but it
// may never infer UNLOCKED and may not overwrite confirmed WTS state.
type sessionState struct {
	mu          sync.RWMutex
	state       SessionState
	observedAt  time.Time
	wtsObserved bool
}

func (s *sessionState) notification(state SessionState, at time.Time) bool {
	if state != SessionLocked && state != SessionUnlocked {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.state != state || !s.wtsObserved
	s.state = state
	s.observedAt = at.UTC()
	s.wtsObserved = true
	return changed
}

func (s *sessionState) fallbackLocked(at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wtsObserved || s.state == SessionLocked {
		return false
	}
	s.state = SessionLocked
	s.observedAt = at.UTC()
	return true
}

func (s *sessionState) snapshot() (SessionState, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state, s.observedAt, s.wtsObserved
}
