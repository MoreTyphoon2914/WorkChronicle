package browsercontext

import (
	"testing"
	"time"
)

func validObservation() Observation {
	return Observation{SchemaVersion: 1, Browser: "Firefox", TabID: "tab-1", Active: true, Visible: true, URL: "https://Example.com/video", Title: "Training", Domain: "", Media: Media{Present: true, State: "playing", Type: "video", Audible: true}, ObservedAt: time.Now().UTC()}
}

func TestObservationValidationAndNormalization(t *testing.T) {
	o := validObservation()
	if err := o.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if o.Browser != "firefox" || o.Domain != "example.com" || o.SourceID() != "browser:firefox:tab-1" {
		t.Fatalf("observation not normalized: %#v", o)
	}
	e := o.PassiveEvent(o.ObservedAt, o.ObservedAt)
	if !e.Evidence[o.SourceID()].PassiveWork {
		t.Fatalf("playing media did not derive passive evidence: %#v", e)
	}
}

func TestMediaStatesAndBackgroundPlayback(t *testing.T) {
	for _, tt := range []struct {
		state   string
		passive bool
	}{{"playing", true}, {"paused", false}, {"stopped", false}, {"unknown", false}} {
		t.Run(tt.state, func(t *testing.T) {
			o := validObservation()
			o.Active, o.Visible, o.Media.State = false, false, tt.state
			if err := o.NormalizeAndValidate(); err != nil {
				t.Fatal(err)
			}
			e := o.PassiveEvent(o.ObservedAt, o.ObservedAt).Evidence[o.SourceID()]
			if e.PassiveWork != tt.passive {
				t.Fatalf("state %s passive=%t", tt.state, e.PassiveWork)
			}
		})
	}
}

func TestInvalidObservationSchemas(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Observation)
	}{
		{"version", func(o *Observation) { o.SchemaVersion = 2 }},
		{"browser", func(o *Observation) { o.Browser = "bad browser" }},
		{"tab", func(o *Observation) { o.TabID = "" }},
		{"timestamp", func(o *Observation) { o.ObservedAt = time.Time{} }},
		{"media state", func(o *Observation) { o.Media.State = "buffering" }},
		{"media type", func(o *Observation) { o.Media.Type = "document" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := validObservation()
			tt.mutate(&o)
			if err := o.NormalizeAndValidate(); err == nil {
				t.Fatalf("invalid observation accepted: %#v", o)
			}
		})
	}
}

func TestMultipleBrowsersHaveIndependentSources(t *testing.T) {
	firefox := validObservation()
	chrome := validObservation()
	chrome.Browser, chrome.TabID, chrome.Media.State = "chrome", "tab-2", "paused"
	if err := firefox.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if err := chrome.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if firefox.SourceID() == chrome.SourceID() {
		t.Fatal("browser observations collided")
	}
	if !firefox.PassiveEvent(firefox.ObservedAt, firefox.ObservedAt).Evidence[firefox.SourceID()].PassiveWork {
		t.Fatal("firefox playing evidence lost")
	}
	if chrome.PassiveEvent(chrome.ObservedAt, chrome.ObservedAt).Evidence[chrome.SourceID()].PassiveWork {
		t.Fatal("chrome paused evidence qualified")
	}
}
