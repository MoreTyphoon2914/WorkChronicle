// Package tray provides a presentation-only system-tray controller. It consumes
// current status through Provider and has no access to collectors, detectors,
// ActivityWatch, classification, or work-evaluation logic.
package tray

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"worktracker/internal/model"
)

const RefreshInterval = 5 * time.Second

type IconState string

const (
	IconWorking   IconState = "WORKING"
	IconBreak     IconState = "BREAK"
	IconUntracked IconState = "UNTRACKED"
)

// Provider is the only application-facing dependency of the tray.
type Provider interface {
	Status(context.Context) (model.StatusReport, error)
}

type ProviderFunc func(context.Context) (model.StatusReport, error)

func (f ProviderFunc) Status(ctx context.Context) (model.StatusReport, error) { return f(ctx) }

type NativeOptions struct {
	ReportsDir string
	LogsDir    string
}

// ViewModel contains only display-ready values for a native tray UI.
type ViewModel struct {
	Icon            IconState
	State           string
	Location        string
	Working         string
	Evaluation      string
	Remaining       string
	Foreground      string
	PassiveEvidence string
	Health          string
	ProviderError   string
}

func MapStatus(status model.StatusReport) ViewModel {
	icon := IconUntracked
	switch status.WorkState {
	case model.Working:
		icon = IconWorking
	case model.Break:
		icon = IconBreak
	}
	state := string(status.WorkState)
	if state == "" {
		state = string(model.Untracked)
	}
	location := "unavailable"
	if status.Location != "" {
		location = string(status.Location)
		if status.LocationEvidence != "" {
			location += " (" + string(status.LocationEvidence) + ")"
		}
	}
	foreground := strings.TrimSpace(status.Foreground.Executable)
	if foreground == "" {
		foreground = "unavailable"
	} else if title := strings.TrimSpace(status.Foreground.Title); title != "" {
		foreground += " — " + title
	}
	health := string(status.TrackerHealth)
	if health == "" {
		health = string(model.Degraded) + " (health evidence unavailable)"
	}
	evaluation := "unavailable"
	if status.WorkEvaluation.Band != "" {
		evaluation = string(status.WorkEvaluation.Band)
	}
	return ViewModel{
		Icon: icon, State: state, Location: location,
		Working:         formatDuration(status.Totals.WorkingSeconds),
		Evaluation:      evaluation,
		Remaining:       formatDuration(status.RemainingSeconds),
		Foreground:      foreground,
		PassiveEvidence: summarizePassive(status.PassiveDetectorEvidence),
		Health:          health,
	}
}

func FailureView(err error) ViewModel {
	message := "status provider failed"
	if err != nil {
		message = err.Error()
	}
	return ViewModel{
		Icon: IconUntracked, State: string(model.Untracked), Location: "unavailable",
		Working: "unavailable", Evaluation: "unavailable", Remaining: "unavailable",
		Foreground: "unavailable", PassiveEvidence: "unavailable",
		Health: string(model.Failed), ProviderError: message,
	}
}

func (v ViewModel) PopupText() string {
	lines := []string{
		"State: " + v.State,
		"Location: " + v.Location,
		"Working today: " + v.Working,
		"Work evaluation: " + v.Evaluation,
		"Remaining target: " + v.Remaining,
		"Foreground: " + v.Foreground,
		"Passive evidence: " + v.PassiveEvidence,
		"Health: " + v.Health,
	}
	if v.ProviderError != "" {
		lines = append(lines, "Status error: "+v.ProviderError)
	}
	return strings.Join(lines, "\r\n")
}

func summarizePassive(evidence map[string]model.PassiveEvidence) string {
	if len(evidence) == 0 {
		return "none available"
	}
	ids := make([]string, 0, len(evidence))
	for id := range evidence {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		e := evidence[id]
		parts = append(parts, fmt.Sprintf("%s: %s (available=%t, passive-work=%t)", id, e.State, e.Available, e.PassiveWork))
	}
	return strings.Join(parts, "; ")
}

func formatDuration(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	d := time.Duration(seconds * float64(time.Second)).Round(time.Minute)
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}
