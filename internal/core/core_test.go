package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"worktracker/internal/browsercontext"
	"worktracker/internal/coreprotocol"
	"worktracker/internal/model"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "agent-token")
	if err := os.WriteFile(tokenFile, []byte("0123456789abcdef-test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Config{
		ListenAddress: "127.0.0.1:0", DataDir: dir, AgentTokenFile: tokenFile,
		Timezone: "UTC", DayBoundary: "04:00", AFKGrace: 2 * time.Minute,
		AutoEnd: 90 * time.Minute, EvidenceFreshness: 15 * time.Second,
		Retention: 35 * 24 * time.Hour, AgentStale: 30 * time.Second,
		BrowserFreshness: 30 * time.Second,
		DailyMinimum:     6 * time.Hour, DailyTarget: 8 * time.Hour, WorkdaysPerWeek: 5,
		LockApps: []string{"lockapp.exe"}, LockTitleContains: []string{"windows default lock screen"},
	}
}

func TestCoreOwnsRemotePassiveClassification(t *testing.T) {
	config := testConfig(t)
	store, err := OpenStore(config.DataDir, config.Retention)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	start := now.Add(-10 * time.Minute)
	batch := coreprotocol.Batch{
		SchemaVersion: 1, AgentID: "host", SentAt: now,
		Windows: []coreprotocol.WindowObservation{{Start: start, End: now, Executable: "firefox.exe"}},
		AFK:     []coreprotocol.AFKObservation{{Start: start, End: now, Status: "afk"}},
		HostContext: []coreprotocol.HostContextObservation{{
			Start: now.Add(-time.Second), End: now.Add(-time.Second), Location: model.Remote, LocationEvidence: model.Confirmed, Health: model.Healthy,
			Apps: map[string]coreprotocol.AppObservation{"vlc": {SourceID: "vlc", State: "playing", Available: true, ObservedAt: now.Add(-time.Second)}},
		}},
	}
	if err := batch.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(batch); err != nil {
		t.Fatal(err)
	}
	report, err := (Engine{Config: config, Store: store}).Today(now)
	if err != nil {
		t.Fatal(err)
	}
	if report.CurrentState != model.Working {
		t.Fatalf("Core did not classify remote AFK plus VLC playing: %#v", report)
	}
	last := report.Timeline[len(report.Timeline)-1]
	if !last.PassiveEvidence["vlc"].PassiveWork {
		t.Fatalf("Core did not derive passive evidence: %#v", last)
	}
}

func TestStorePersistsAcrossRecreationAndWeekSumsDays(t *testing.T) {
	config := testConfig(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenStore(config.DataDir, config.Retention)
	if err != nil {
		t.Fatal(err)
	}
	batch := coreprotocol.Batch{
		SchemaVersion: 1, AgentID: "host", SentAt: now,
		Windows:     []coreprotocol.WindowObservation{{Start: now.Add(-time.Hour), End: now, Executable: "Code.exe"}},
		AFK:         []coreprotocol.AFKObservation{{Start: now.Add(-time.Hour), End: now, Status: "not-afk"}},
		HostContext: []coreprotocol.HostContextObservation{{Start: now, End: now, Location: model.Remote, LocationEvidence: model.Confirmed, Health: model.Healthy}},
	}
	if err := store.Ingest(batch); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(config.DataDir, config.Retention)
	if err != nil {
		t.Fatal(err)
	}
	engine := Engine{Config: config, Store: reopened}
	week, err := engine.Week(now)
	if err != nil {
		t.Fatal(err)
	}
	var summed model.Totals
	for _, day := range week.Days {
		summed.WorkingSeconds += day.Totals.WorkingSeconds
		summed.BreakSeconds += day.Totals.BreakSeconds
		summed.UntrackedSeconds += day.Totals.UntrackedSeconds
	}
	if week.Totals != summed || week.Totals.WorkingSeconds == 0 {
		t.Fatalf("persisted weekly totals=%#v days=%#v", week.Totals, week.Days)
	}
}

func TestObservationEndpointRequiresTokenAndServesRealReports(t *testing.T) {
	config := testConfig(t)
	store, _ := OpenStore(config.DataDir, config.Retention)
	server, err := NewServer(config, store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := coreprotocol.Batch{SchemaVersion: 1, AgentID: "host", SentAt: now}
	payload, _ := json.Marshal(batch)

	unauthorized := httptest.NewRequest(http.MethodPost, ObservationsPath, bytes.NewReader(payload))
	unauthorized.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.HTTPServer().Handler.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", response.Code)
	}

	authorized := httptest.NewRequest(http.MethodPost, ObservationsPath, bytes.NewReader(payload))
	authorized.Header.Set("Content-Type", "application/json")
	authorized.Header.Set("Authorization", "Bearer 0123456789abcdef-test-token")
	response = httptest.NewRecorder()
	server.HTTPServer().Handler.ServeHTTP(response, authorized)
	if response.Code != http.StatusAccepted {
		t.Fatalf("authorized status=%d body=%s", response.Code, response.Body.String())
	}

	for _, path := range []string{"/health", "/api/v1/status", "/api/v1/reports/today", "/api/v1/reports/week"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response = httptest.NewRecorder()
		server.HTTPServer().Handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !json.Valid(response.Body.Bytes()) {
			t.Fatalf("GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestDashboardIsServedWithoutRawTitleProjection(t *testing.T) {
	config := testConfig(t)
	store, _ := OpenStore(config.DataDir, config.Retention)
	server, err := NewServer(config, store)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	server.HTTPServer().Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !bytes.Contains([]byte(body), []byte("WorkChronicle")) || bytes.Contains([]byte(body), []byte("foreground.title")) {
		t.Fatalf("dashboard branding/privacy projection is invalid")
	}
	for _, marker := range []string{"Session time = Working + Break + Untracked.", "Credited working today", "First work", "Last work", "Report through", "Total session", "Remaining working target"} {
		if !bytes.Contains([]byte(body), []byte(marker)) {
			t.Fatalf("dashboard omitted presentation marker %q", marker)
		}
	}
}

func TestHealthPreservesAggregateBrowserCountAndFamilyDiagnostics(t *testing.T) {
	config := testConfig(t)
	store, _ := OpenStore(config.DataDir, config.Retention)
	now := time.Now().UTC()
	batch := coreprotocol.Batch{
		SchemaVersion: 1, AgentID: "host", SentAt: now,
		Browser: []browsercontext.Observation{
			{SchemaVersion: 1, Browser: "firefox", TabID: "one", Active: true, Visible: true, ObservedAt: now},
			{SchemaVersion: 1, Browser: "firefox", TabID: "two", Active: false, Visible: false, ObservedAt: now.Add(-time.Second)},
			{SchemaVersion: 1, Browser: "chrome", TabID: "one", Active: true, Visible: true, ObservedAt: now},
		},
	}
	if err := batch.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(batch); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config, store)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	server.HTTPServer().Handler.ServeHTTP(response, request)
	var health struct {
		ObservationCounts map[string]int `json:"observation_counts"`
		Browsers          BrowserHealth  `json:"browsers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.ObservationCounts["browser"] != 3 || health.Browsers.ActiveCount != 2 || health.Browsers.Sources["firefox"].Observations != 2 {
		t.Fatalf("health diagnostics=%#v", health)
	}
}
