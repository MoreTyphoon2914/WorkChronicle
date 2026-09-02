package hostagent

import (
	"fmt"
	"strings"
	"time"

	"worktracker/internal/coreprotocol"
	"worktracker/internal/nativewatcher"
)

func compareSources(windows []coreprotocol.WindowObservation, afk []coreprotocol.AFKObservation, native nativewatcher.Result, awAFKObservedAt *time.Time, at time.Time, tolerance, afkThreshold time.Duration, lockApps, lockTitles []string) *coreprotocol.ParityComparison {
	comparison := &coreprotocol.ParityComparison{ComparedAt: at, ToleranceSeconds: tolerance.Seconds()}
	awWindow := latestWindow(windows, at, tolerance)
	awAFK := latestAFK(afk, at, tolerance)
	populateAFKTiming(comparison, awAFK, native.AFK, awAFKObservedAt, afkThreshold)
	comparison.Comparable = awWindow != nil && awAFK != nil && native.Window != nil && native.AFK != nil && native.Session != nil
	if !comparison.Comparable {
		comparison.Summary = "waiting for comparable fresh observations"
		return comparison
	}
	comparison.ForegroundMatch = strings.EqualFold(awWindow.Executable, native.Window.Executable)
	comparison.AFKMatch = equivalentAFK(awAFK.Status, native.AFK.Status)
	if comparison.AFKMatch && comparison.AFKSemanticDeltaSeconds != nil {
		comparison.AFKMatch = *comparison.AFKSemanticDeltaSeconds <= tolerance.Seconds()
	}
	comparison.SessionMatch = activityWatchLocked(*awWindow, lockApps, lockTitles) == native.Session.Locked
	if comparison.ForegroundMatch && comparison.AFKMatch && comparison.SessionMatch {
		comparison.Summary = "normalized sources agree"
	} else {
		comparison.Summary = fmt.Sprintf("mismatch: foreground=%t afk=%t session=%t", comparison.ForegroundMatch, comparison.AFKMatch, comparison.SessionMatch)
	}
	return comparison
}

func populateAFKTiming(comparison *coreprotocol.ParityComparison, aw, native *coreprotocol.AFKObservation, awObservedAt *time.Time, threshold time.Duration) {
	if aw == nil || native == nil || !strings.EqualFold(strings.TrimSpace(aw.Status), "afk") || !strings.EqualFold(strings.TrimSpace(native.Status), "afk") {
		return
	}
	eventStart := aw.Start.UTC()
	inferred := eventStart.Add(threshold)
	nativeTransition := native.Start.UTC()
	semanticDelta := nativeTransition.Sub(inferred).Seconds()
	if semanticDelta < 0 {
		semanticDelta = -semanticDelta
	}
	comparison.ActivityWatchAFKEventStart = &eventStart
	comparison.ActivityWatchAFKInferredAt = &inferred
	comparison.NativeAFKTransitionAt = &nativeTransition
	comparison.AFKSemanticDeltaSeconds = &semanticDelta
	if awObservedAt != nil {
		observed := awObservedAt.UTC()
		publicationDelay := observed.Sub(inferred).Seconds()
		comparison.ActivityWatchAFKFirstObservedAt = &observed
		comparison.ActivityWatchPublicationDelaySec = &publicationDelay
	}
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
