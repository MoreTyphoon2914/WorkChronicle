package browsercontext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type memoryStore struct {
	observations []Observation
	err          error
}

func (s *memoryStore) Save(_ context.Context, o Observation) error {
	if s.err != nil {
		return s.err
	}
	s.observations = append(s.observations, o)
	return nil
}

func requestFor(t *testing.T, body any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, ObservationsPath, bytes.NewReader(b))
	r.RemoteAddr = "127.0.0.1:45678"
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestObservationEndpoint(t *testing.T) {
	store := &memoryStore{}
	server := NewServer(store, 64<<10)
	w := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(w, requestFor(t, validObservation()))
	if w.Code != http.StatusAccepted || len(store.observations) != 1 {
		t.Fatalf("code=%d body=%s observations=%d", w.Code, w.Body.String(), len(store.observations))
	}
}

func TestMalformedAndInvalidPayloads(t *testing.T) {
	store := &memoryStore{}
	server := NewServer(store, 256)
	tests := []*http.Request{
		func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, ObservationsPath, bytes.NewBufferString("{"))
			r.RemoteAddr = "127.0.0.1:1"
			r.Header.Set("Content-Type", "application/json")
			return r
		}(),
		requestFor(t, map[string]any{"schema_version": 99}),
		requestFor(t, map[string]any{"schema_version": 1, "unknown": true}),
	}
	for i, r := range tests {
		w := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("case %d code=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	if len(store.observations) != 0 {
		t.Fatal("invalid observations were persisted")
	}
}

func TestPersistenceFailureIsIsolated(t *testing.T) {
	store := &memoryStore{err: errors.New("temporary ActivityWatch failure")}
	server := NewServer(store, 64<<10)
	w := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(w, requestFor(t, validObservation()))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestNonLoopbackRequestRejected(t *testing.T) {
	store := &memoryStore{}
	server := NewServer(store, 64<<10)
	r := requestFor(t, validObservation())
	r.RemoteAddr = "192.168.1.50:45678"
	w := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || len(store.observations) != 0 {
		t.Fatalf("code=%d observations=%d", w.Code, len(store.observations))
	}
}

func TestListenerBindsIPv4LoopbackOnly(t *testing.T) {
	listener, err := ListenLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("listener address=%v", listener.Addr())
	}
}
