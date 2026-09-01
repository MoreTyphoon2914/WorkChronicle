package coreprotocol

import (
	"testing"
	"time"

	"worktracker/internal/model"
)

func TestBatchContainsFactsWithoutClassification(t *testing.T) {
	now := time.Now().UTC()
	batch := Batch{
		SchemaVersion: 1, AgentID: "windows-host", SentAt: now,
		Windows: []WindowObservation{{Start: now, End: now, Executable: "Code.exe"}},
		AFK:     []AFKObservation{{Start: now, End: now, Status: "not-afk"}},
		HostContext: []HostContextObservation{{
			Start: now, End: now, Location: model.Office, LocationEvidence: model.Confirmed, Health: model.Healthy,
			Apps: map[string]AppObservation{"vlc": {SourceID: "vlc", State: "paused", Available: true, ObservedAt: now}},
		}},
	}
	if err := batch.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if batch.HostContext[0].Apps["vlc"].State != "paused" {
		t.Fatal("raw app state changed during protocol validation")
	}
}

func TestBatchRejectsInvalidSchemaAndTimes(t *testing.T) {
	if err := (&Batch{SchemaVersion: 2, AgentID: "host", SentAt: time.Now()}).NormalizeAndValidate(); err == nil {
		t.Fatal("unsupported schema accepted")
	}
	now := time.Now().UTC()
	batch := Batch{SchemaVersion: 1, AgentID: "host", SentAt: now, Windows: []WindowObservation{{Start: now, End: now.Add(-time.Second), Executable: "x.exe"}}}
	if err := batch.NormalizeAndValidate(); err == nil {
		t.Fatal("backwards observation interval accepted")
	}
}
