package browsercontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"worktracker/internal/appstate"
	"worktracker/internal/model"
)

const SchemaVersion = 1

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type Media struct {
	Present bool   `json:"present"`
	State   string `json:"state"`
	Type    string `json:"type"`
	Audible bool   `json:"audible"`
}

// Observation is the raw browser payload persisted in the dedicated browser
// stream. It is deliberately distinct from model.PassiveEvidence.
type Observation struct {
	SchemaVersion int       `json:"schema_version"`
	Browser       string    `json:"browser"`
	TabID         string    `json:"tab_id"`
	Active        bool      `json:"active"`
	Visible       bool      `json:"visible"`
	URL           string    `json:"url"`
	Domain        string    `json:"domain"`
	Title         string    `json:"title"`
	Media         Media     `json:"media"`
	ObservedAt    time.Time `json:"observed_at"`
}

// PlayingMediaPolicy deliberately ignores active/visible tab flags: a
// background tab with actively playing media remains valid passive evidence.
type PlayingMediaPolicy struct{}

func (PlayingMediaPolicy) Qualifies(o appstate.Observation) bool {
	return o.Available && o.State == "playing"
}

func (o *Observation) NormalizeAndValidate() error {
	if o.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", o.SchemaVersion)
	}
	o.Browser = strings.ToLower(strings.TrimSpace(o.Browser))
	o.TabID = strings.TrimSpace(o.TabID)
	if !identifierPattern.MatchString(o.Browser) {
		return errors.New("browser must be a 1-128 character identifier")
	}
	if !identifierPattern.MatchString(o.TabID) {
		return errors.New("tab_id must be a 1-128 character identifier")
	}
	if o.ObservedAt.IsZero() {
		return errors.New("observed_at is required")
	}
	if len(o.URL) > 8192 || len(o.Title) > 4096 || len(o.Domain) > 253 {
		return errors.New("url, title, or domain exceeds its size limit")
	}
	o.Domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(o.Domain)), ".")
	if o.URL != "" {
		parsed, err := url.Parse(o.URL)
		if err != nil || parsed.Scheme == "" {
			return errors.New("url must be absolute when present")
		}
		if o.Domain == "" && parsed.Hostname() != "" {
			o.Domain = strings.ToLower(parsed.Hostname())
		}
	}
	if !o.Media.Present {
		o.Media.State, o.Media.Type, o.Media.Audible = "none", "none", false
		return nil
	}
	o.Media.State = strings.ToLower(strings.TrimSpace(o.Media.State))
	o.Media.Type = strings.ToLower(strings.TrimSpace(o.Media.Type))
	if o.Media.State != "playing" && o.Media.State != "paused" && o.Media.State != "stopped" && o.Media.State != "unknown" {
		return errors.New("media.state must be playing, paused, stopped, or unknown")
	}
	if o.Media.Type != "audio" && o.Media.Type != "video" && o.Media.Type != "unknown" {
		return errors.New("media.type must be audio, video, or unknown")
	}
	return nil
}

func (o Observation) SourceID() string { return "browser:" + o.Browser + ":" + o.TabID }

func (o Observation) PassiveEvent(start, end time.Time) model.PassiveEvidenceEvent {
	state := o.Media.State
	if !o.Media.Present {
		state = "none"
	}
	raw := appstate.Observation{Detector: o.SourceID(), State: state, Available: true, ObservedAt: o.ObservedAt}
	evidence := appstate.DeriveEvidence(raw, PlayingMediaPolicy{})
	return model.PassiveEvidenceEvent{Start: start, End: end, Evidence: map[string]model.PassiveEvidence{o.SourceID(): evidence}}
}

func DecodeStored(data map[string]any) (Observation, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return Observation{}, err
	}
	var o Observation
	if err := json.Unmarshal(b, &o); err != nil {
		return Observation{}, err
	}
	return o, o.NormalizeAndValidate()
}
