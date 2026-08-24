package model

import (
	"testing"
	"time"
)

func TestEvidenceFreshAtInclusiveExpiry(t *testing.T) {
	end := time.Date(2026, 8, 19, 14, 8, 0, 0, time.UTC)
	freshness := 15 * time.Second
	if !EvidenceFreshAt(end, end.Add(freshness), freshness) {
		t.Fatal("evidence must remain fresh at the exact expiry boundary")
	}
	if EvidenceFreshAt(end, end.Add(freshness+time.Nanosecond), freshness) {
		t.Fatal("evidence must become stale after the expiry boundary")
	}
}
