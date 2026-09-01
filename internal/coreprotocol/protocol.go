package coreprotocol

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"worktracker/internal/browsercontext"
	"worktracker/internal/model"
)

const SchemaVersion = 1

const (
	SourceActivityWatch = "activitywatch"
	SourceNativeWindows = "native_windows"
)

// WindowObservation and AFKObservation are normalized host facts. They carry
// no WorkChronicle classification decision.
type WindowObservation struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Executable string    `json:"executable"`
	Title      string    `json:"title,omitempty"`
	Source     string    `json:"source,omitempty"`
	Locked     bool      `json:"locked,omitempty"`
}

type AFKObservation struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Status string    `json:"status"`
	Source string    `json:"source,omitempty"`
}

type SessionObservation struct {
	ObservedAt time.Time `json:"observed_at"`
	Locked     bool      `json:"locked"`
	Source     string    `json:"source"`
}

type SourceHealth struct {
	Enabled         bool       `json:"enabled"`
	Connected       bool       `json:"connected"`
	LastObservation *time.Time `json:"last_observation,omitempty"`
	Message         string     `json:"message,omitempty"`
}

type ParityComparison struct {
	ComparedAt       time.Time `json:"compared_at"`
	ToleranceSeconds float64   `json:"tolerance_seconds"`
	ForegroundMatch  bool      `json:"foreground_match"`
	AFKMatch         bool      `json:"afk_match"`
	SessionMatch     bool      `json:"session_match"`
	Comparable       bool      `json:"comparable"`
	Summary          string    `json:"summary,omitempty"`
}

type AcquisitionDiagnostics struct {
	Mode             string            `json:"mode"`
	ActivityWatch    SourceHealth      `json:"activitywatch"`
	NativeForeground SourceHealth      `json:"native_foreground"`
	NativeAFK        SourceHealth      `json:"native_afk"`
	NativeSession    SourceHealth      `json:"native_session"`
	Comparison       *ParityComparison `json:"comparison,omitempty"`
}

// StoredContextObservation preserves existing ActivityWatch V0/V1 payloads so
// schema migration and evidence normalization remain Core responsibilities.
type StoredContextObservation struct {
	Start time.Time      `json:"start"`
	End   time.Time      `json:"end"`
	Data  map[string]any `json:"data"`
}

type AppObservation struct {
	SourceID   string    `json:"source_id"`
	State      string    `json:"state"`
	Available  bool      `json:"available"`
	ObservedAt time.Time `json:"observed_at"`
}

// HostContextObservation contains acquisition results only. In particular,
// app observations do not contain passive_work or a work-state decision.
type HostContextObservation struct {
	Start            time.Time                 `json:"start"`
	End              time.Time                 `json:"end"`
	Location         model.Location            `json:"location"`
	LocationEvidence model.LocationEvidence    `json:"location_evidence"`
	Health           model.HealthLevel         `json:"health"`
	Apps             map[string]AppObservation `json:"apps,omitempty"`
}

type Batch struct {
	SchemaVersion int                          `json:"schema_version"`
	AgentID       string                       `json:"agent_id"`
	SentAt        time.Time                    `json:"sent_at"`
	Windows       []WindowObservation          `json:"windows,omitempty"`
	AFK           []AFKObservation             `json:"afk,omitempty"`
	StoredContext []StoredContextObservation   `json:"stored_context,omitempty"`
	HostContext   []HostContextObservation     `json:"host_context,omitempty"`
	Browser       []browsercontext.Observation `json:"browser,omitempty"`
	ShadowWindows []WindowObservation          `json:"shadow_windows,omitempty"`
	ShadowAFK     []AFKObservation             `json:"shadow_afk,omitempty"`
	Sessions      []SessionObservation         `json:"sessions,omitempty"`
	Acquisition   *AcquisitionDiagnostics      `json:"acquisition,omitempty"`
}

func (b *Batch) NormalizeAndValidate() error {
	if b.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", b.SchemaVersion)
	}
	b.AgentID = strings.TrimSpace(b.AgentID)
	if b.AgentID == "" || len(b.AgentID) > 128 {
		return errors.New("agent_id must be 1-128 characters")
	}
	if b.SentAt.IsZero() {
		return errors.New("sent_at is required")
	}
	for i := range b.Windows {
		item := &b.Windows[i]
		if item.Source == "" {
			item.Source = SourceActivityWatch
		}
		if item.Start.IsZero() || item.End.Before(item.Start) || strings.TrimSpace(item.Executable) == "" || !validSource(item.Source) {
			return errors.New("window observations require valid times and executable")
		}
	}
	for i := range b.AFK {
		item := &b.AFK[i]
		if item.Source == "" {
			item.Source = SourceActivityWatch
		}
		if item.Start.IsZero() || item.End.Before(item.Start) || strings.TrimSpace(item.Status) == "" || !validSource(item.Source) {
			return errors.New("AFK observations require valid times and status")
		}
	}
	for i := range b.ShadowWindows {
		item := &b.ShadowWindows[i]
		if item.Source == "" {
			item.Source = SourceNativeWindows
		}
		if item.Start.IsZero() || item.End.Before(item.Start) || strings.TrimSpace(item.Executable) == "" || item.Source != SourceNativeWindows {
			return errors.New("shadow window observations require valid times, executable, and source")
		}
	}
	for i := range b.ShadowAFK {
		item := &b.ShadowAFK[i]
		if item.Source == "" {
			item.Source = SourceNativeWindows
		}
		if item.Start.IsZero() || item.End.Before(item.Start) || strings.TrimSpace(item.Status) == "" || item.Source != SourceNativeWindows {
			return errors.New("shadow AFK observations require valid times, status, and source")
		}
	}
	for _, item := range b.Sessions {
		if item.ObservedAt.IsZero() || item.Source != SourceNativeWindows {
			return errors.New("session observations require observed_at and source")
		}
	}
	if b.Acquisition != nil {
		mode := strings.ToLower(strings.TrimSpace(b.Acquisition.Mode))
		if mode != "activitywatch" && mode != "shadow" && mode != "native" {
			return errors.New("acquisition mode must be activitywatch, shadow, or native")
		}
	}
	for _, item := range b.StoredContext {
		if item.Start.IsZero() || item.End.Before(item.Start) || item.Data == nil {
			return errors.New("stored context observations require valid times and data")
		}
	}
	for _, item := range b.HostContext {
		if item.Start.IsZero() || item.End.Before(item.Start) {
			return errors.New("host context observations require valid times")
		}
		if item.Location != model.Office && item.Location != model.Remote {
			return errors.New("host context location must be OFFICE or REMOTE")
		}
		for id, app := range item.Apps {
			if strings.TrimSpace(id) == "" || app.ObservedAt.IsZero() || strings.TrimSpace(app.State) == "" {
				return errors.New("app observations require source, state, and observed_at")
			}
		}
	}
	for i := range b.Browser {
		if err := b.Browser[i].NormalizeAndValidate(); err != nil {
			return fmt.Errorf("browser observation %d: %w", i, err)
		}
	}
	return nil
}

func validSource(source string) bool {
	return source == SourceActivityWatch || source == SourceNativeWindows
}
