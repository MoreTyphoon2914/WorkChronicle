package hostagent

import (
	"fmt"
	"strings"
	"time"

	"worktracker/internal/coreprotocol"
	"worktracker/internal/nativewatcher"
)

func compareSources(windows []coreprotocol.WindowObservation, afk []coreprotocol.AFKObservation, native nativewatcher.Result, at time.Time, tolerance time.Duration, lockApps, lockTitles []string) *coreprotocol.ParityComparison {
	comparison := &coreprotocol.ParityComparison{ComparedAt: at, ToleranceSeconds: tolerance.Seconds()}
	awWindow := latestWindow(windows, at, tolerance)
	awAFK := latestAFK(afk, at, tolerance)
	comparison.Comparable = awWindow != nil && awAFK != nil && native.Window != nil && native.AFK != nil && native.Session != nil
	if !comparison.Comparable {
		comparison.Summary = "waiting for comparable fresh observations"
		return comparison
	}
	comparison.ForegroundMatch = strings.EqualFold(awWindow.Executable, native.Window.Executable)
	comparison.AFKMatch = equivalentAFK(awAFK.Status, native.AFK.Status)
	comparison.SessionMatch = activityWatchLocked(*awWindow, lockApps, lockTitles) == native.Session.Locked
	if comparison.ForegroundMatch && comparison.AFKMatch && comparison.SessionMatch {
		comparison.Summary = "normalized sources agree"
	} else {
		comparison.Summary = fmt.Sprintf("mismatch: foreground=%t afk=%t session=%t", comparison.ForegroundMatch, comparison.AFKMatch, comparison.SessionMatch)
	}
	return comparison
}

func latestWindow(items []coreprotocol.WindowObservation, at time.Time, tolerance time.Duration) *coreprotocol.WindowObservation {
	var latest *coreprotocol.WindowObservation
	for i := range items {
		item := &items[i]
		if item.Start.After(at) || at.After(item.End.Add(tolerance)) {
			continue
		}
		if latest == nil || item.Start.After(latest.Start) || (item.Start.Equal(latest.Start) && item.End.After(latest.End)) {
			latest = item
		}
	}
	return latest
}

func latestAFK(items []coreprotocol.AFKObservation, at time.Time, tolerance time.Duration) *coreprotocol.AFKObservation {
	var latest *coreprotocol.AFKObservation
	for i := range items {
		item := &items[i]
		if item.Start.After(at) || at.After(item.End.Add(tolerance)) {
			continue
		}
		if latest == nil || item.Start.After(latest.Start) || (item.Start.Equal(latest.Start) && item.End.After(latest.End)) {
			latest = item
		}
	}
	return latest
}

func equivalentAFK(left, right string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "active" {
			return "not-afk"
		}
		return value
	}
	return normalize(left) == normalize(right)
}

func activityWatchLocked(window coreprotocol.WindowObservation, lockApps, lockTitles []string) bool {
	for _, app := range lockApps {
		if strings.EqualFold(strings.TrimSpace(app), window.Executable) {
			return true
		}
	}
	for _, marker := range lockTitles {
		if marker != "" && strings.Contains(strings.ToLower(window.Title), strings.ToLower(marker)) {
			return true
		}
	}
	return false
}
