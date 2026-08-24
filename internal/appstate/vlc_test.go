package appstate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVLCStates(t *testing.T) {
	for _, state := range []string{"playing", "paused", "stopped"} {
		t.Run(state, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				u, p, ok := r.BasicAuth()
				if !ok || u != "" || p != "secret" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"state":"` + state + `"}`))
			}))
			defer srv.Close()
			d := VLCDetector{URL: srv.URL, Password: "secret", Client: srv.Client()}
			o, err := d.Observe(context.Background())
			if err != nil || !o.Available || o.State != state {
				t.Fatalf("obs=%#v err=%v", o, err)
			}
		})
	}
}
func TestVLCFailuresNeverQualify(t *testing.T) {
	handlers := []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`not-json`)) }, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte(`{"state":"playing"}`))
	}}
	for i, h := range handlers {
		srv := httptest.NewServer(h)
		client := srv.Client()
		if i == 2 {
			client.Timeout = time.Millisecond
		}
		d := VLCDetector{URL: srv.URL, Client: client}
		o, err := d.Observe(context.Background())
		srv.Close()
		if err == nil || o.Available {
			t.Fatalf("case %d obs=%#v err=%v", i, o, err)
		}
	}
}
