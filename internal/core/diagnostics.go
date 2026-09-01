package core

import (
	"strings"
	"time"

	"worktracker/internal/browsercontext"
	"worktracker/internal/coreprotocol"
)

var knownBrowserFamilies = []string{"firefox", "chrome", "edge"}

type BrowserSourceHealth struct {
	Active       bool       `json:"active"`
	LastSeen     *time.Time `json:"last_seen"`
	Observations int        `json:"observations"`
}

func acquisitionHealth(input *coreprotocol.AcquisitionDiagnostics, now time.Time, freshness time.Duration) *coreprotocol.AcquisitionDiagnostics {
	if input == nil {
		return nil
	}
	result := *input
	markStale := func(source *coreprotocol.SourceHealth) {
		if !source.Enabled || source.LastObservation == nil || !source.Connected {
			return
		}
		age := now.UTC().Sub(source.LastObservation.UTC())
		if age > freshness || age < 0 {
			source.Connected = false
			if source.Message == "" {
				source.Message = "last observation is stale"
			}
		}
	}
	markStale(&result.ActivityWatch)
	markStale(&result.NativeForeground)
	markStale(&result.NativeAFK)
	markStale(&result.NativeSession)
	return &result
}

func acquisitionHealthy(value *coreprotocol.AcquisitionDiagnostics) bool {
	if value == nil {
		return true // Backward-compatible agents predate component diagnostics.
	}
	for _, source := range []coreprotocol.SourceHealth{value.ActivityWatch, value.NativeForeground, value.NativeAFK, value.NativeSession} {
		if source.Enabled && !source.Connected {
			return false
		}
	}
	return true
}

type BrowserHealth struct {
	ActiveCount int                            `json:"active_count"`
	Sources     map[string]BrowserSourceHealth `json:"sources"`
}

// browserHealth reports recently observed extension integrations. Active does
// not mean the browser process is open; it means an observation or heartbeat
// from that browser family arrived within freshness.
func browserHealth(observations []browsercontext.Observation, now time.Time, freshness time.Duration) BrowserHealth {
	result := BrowserHealth{Sources: make(map[string]BrowserSourceHealth, len(knownBrowserFamilies))}
	for _, family := range knownBrowserFamilies {
		result.Sources[family] = BrowserSourceHealth{}
	}
	for _, observation := range observations {
		family := strings.ToLower(strings.TrimSpace(observation.Browser))
		current, known := result.Sources[family]
		if !known {
			continue
		}
		current.Observations++
		observedAt := observation.ObservedAt.UTC()
		if current.LastSeen == nil || observedAt.After(*current.LastSeen) {
			current.LastSeen = &observedAt
		}
		result.Sources[family] = current
	}
	for _, family := range knownBrowserFamilies {
		current := result.Sources[family]
		if current.LastSeen != nil {
			age := now.UTC().Sub(current.LastSeen.UTC())
			current.Active = age >= 0 && age <= freshness
			if current.Active {
				result.ActiveCount++
			}
		}
		result.Sources[family] = current
	}
	return result
}
