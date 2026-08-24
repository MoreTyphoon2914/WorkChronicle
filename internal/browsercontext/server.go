package browsercontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const ObservationsPath = "/api/v1/browser/observations"

type Store interface {
	Save(context.Context, Observation) error
}

type Server struct {
	store        Store
	maxBodyBytes int64
	http         *http.Server
}

func NewServer(store Store, maxBodyBytes int64) *Server {
	s := &Server{store: store, maxBodyBytes: maxBodyBytes}
	mux := http.NewServeMux()
	mux.HandleFunc(ObservationsPath, s.handleObservation)
	mux.HandleFunc("/healthz", s.handleHealth)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	return s
}

// ListenLoopback intentionally exposes no host parameter: callers cannot
// accidentally bind the ingestion endpoint to a LAN interface.
func ListenLoopback(port int) (net.Listener, error) {
	return net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func (s *Server) Serve(listener net.Listener) error {
	err := s.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	if !requestIsLoopback(r) {
		http.Error(w, "loopback requests only", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"status":"ok","schema_version":1}`)
}

func (s *Server) handleObservation(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	if !requestIsLoopback(r) {
		http.Error(w, "loopback requests only", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var observation Observation
	if err := decoder.Decode(&observation); err != nil {
		http.Error(w, "invalid browser observation: "+safeDecodeError(err), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "request must contain one JSON object", http.StatusBadRequest)
		return
	}
	if err := observation.NormalizeAndValidate(); err != nil {
		http.Error(w, "invalid browser observation: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.Save(r.Context(), observation); err != nil {
		http.Error(w, "browser observation could not be persisted", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	io.WriteString(w, `{"accepted":true,"schema_version":1}`)
}

func requestIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func safeDecodeError(err error) string {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("malformed JSON near byte %d", syntax.Offset)
	}
	if strings.Contains(err.Error(), "http: request body too large") {
		return "request body too large"
	}
	return err.Error()
}
