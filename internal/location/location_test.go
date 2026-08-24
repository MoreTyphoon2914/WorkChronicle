package location

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"worktracker/internal/model"
)

func TestNetworkScriptDoesNotSpawnNestedConsoleProcess(t *testing.T) {
	lower := strings.ToLower(networkScript)
	if strings.Contains(lower, "ping.exe") || strings.Contains(lower, "ping -") || strings.Contains(lower, "cmd.exe") || strings.Contains(lower, "powershell.exe") {
		t.Fatalf("periodic network script launches a nested console process: %s", networkScript)
	}
	if !strings.Contains(networkScript, "Test-Connection") {
		t.Fatal("network script no longer primes the neighbor cache through an in-process PowerShell cmdlet")
	}
}

type fakeRunner struct {
	out []byte
	err error
}

func (f *fakeRunner) Run(context.Context) ([]byte, error) { return f.out, f.err }
func TestLocationMappingAndStaleFallback(t *testing.T) {
	r := &fakeRunner{out: []byte(`{"IP":"private","Gateway":"private","GatewayMAC":"aa:bb:cc:dd:ee:ff"}`)}
	d := New(r, []string{"AA-BB-CC-DD-EE-FF"}, nil, 3*time.Minute)
	now := time.Now().UTC()
	got, err := d.Observe(context.Background(), now)
	if err != nil || got.Location != model.Office || got.Evidence != model.Confirmed {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	r.err = errors.New("blocked")
	r.out = nil
	got, _ = d.Observe(context.Background(), now.Add(time.Minute))
	if got.Location != model.Office || got.Evidence != model.Stale || got.Health != model.Degraded {
		t.Fatalf("stale=%#v", got)
	}
	got, _ = d.Observe(context.Background(), now.Add(4*time.Minute))
	if got.Location != model.Remote || got.Evidence != model.Fallback {
		t.Fatalf("fallback=%#v", got)
	}
}
func TestUnknownNetworkIsRemote(t *testing.T) {
	r := &fakeRunner{out: []byte(`{"GatewayMAC":"11-22-33-44-55-66"}`)}
	d := New(r, []string{"AA-BB-CC-DD-EE-FF"}, nil, time.Minute)
	got, err := d.Observe(context.Background(), time.Now())
	if err != nil || got.Location != model.Remote || got.Evidence != model.Confirmed {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
