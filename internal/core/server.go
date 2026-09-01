package core

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"worktracker/internal/coreprotocol"
)

const ObservationsPath = "/api/v1/observations"

//go:embed static/index.html
var dashboardFiles embed.FS

type Server struct {
	config Config
	store  *Store
	engine Engine
	token  string
	http   *http.Server
}

func NewServer(config Config, store *Store) (*Server, error) {
	b, err := os.ReadFile(config.AgentTokenFile)
	if err != nil {
		return nil, fmt.Errorf("read agent token file: %w", err)
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 16 {
		return nil, fmt.Errorf("agent token must contain at least 16 characters")
	}
	s := &Server{config: config, store: store, engine: Engine{Config: config, Store: store}, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/reports/today", s.handleToday)
	mux.HandleFunc("/api/v1/reports/week", s.handleWeek)
	mux.HandleFunc(ObservationsPath, s.handleObservations)
	s.http = &http.Server{Addr: config.ListenAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	return s, nil
}

func (s *Server) HTTPServer() *http.Server { return s.http }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	state := s.store.Snapshot()
	status := "degraded"
	agentConnected := false
	if !state.LastIngest.IsZero() && time.Since(state.LastIngest) <= s.config.AgentStale {
		status = "healthy"
		agentConnected = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "agent_connected": agentConnected, "last_ingest": state.LastIngest, "persistence": "available",
		"browsers": browserHealth(state.Browser, time.Now(), s.config.BrowserFreshness),
		"observation_counts": map[string]int{
			"windows": len(state.Windows), "afk": len(state.AFK), "stored_context": len(state.StoredContext),
			"host_context": len(state.HostContext), "browser": len(state.Browser),
		},
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	page, err := dashboardFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	report, err := s.engine.Status(time.Now())
	if err != nil {
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleToday(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	report, err := s.engine.Today(time.Now())
	if err != nil {
		http.Error(w, "daily report unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleWeek(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	report, err := s.engine.Week(time.Now())
	if err != nil {
		http.Error(w, "weekly report unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleObservations(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var batch coreprotocol.Batch
	if err := decoder.Decode(&batch); err != nil {
		http.Error(w, "invalid observation batch", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "request must contain one JSON object", http.StatusBadRequest)
		return
	}
	if err := batch.NormalizeAndValidate(); err != nil {
		http.Error(w, "invalid observation batch: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.Ingest(batch); err != nil {
		http.Error(w, "observation persistence failed", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "schema_version": coreprotocol.SchemaVersion})
}

func (s *Server) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return len(provided) == len(s.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
