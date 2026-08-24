package windows

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type runnerCall struct {
	name string
	args []string
}
type fakeTaskRunner struct {
	calls []runnerCall
	run   func(int, string, []string) ([]byte, error)
}

func (f *fakeTaskRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, runnerCall{name, args})
	return f.run(len(f.calls), name, args)
}

func TestInstallBlockedDoesNotBypassPolicy(t *testing.T) {
	f := &fakeTaskRunner{}
	f.run = func(n int, _ string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if n == 1 {
			return []byte("ERROR: cannot find"), errors.New("exit 1")
		}
		if strings.Contains(joined, "/Create") {
			return []byte("Access is denied by policy"), errors.New("exit 5")
		}
		return []byte("ERROR: cannot find"), errors.New("exit 1")
	}
	err := installTask(context.Background(), f, "WorkTracker", "C:\\app.exe", "C:\\config.json")
	if err == nil || !strings.Contains(err.Error(), "no security policy was changed") {
		t.Fatalf("err=%v", err)
	}
	for _, c := range f.calls {
		joined := strings.Join(c.args, " ")
		if strings.Contains(joined, "policy") || strings.Contains(joined, "/Change") {
			t.Fatalf("policy bypass attempted: %#v", f.calls)
		}
	}
}
func TestExistingTaskIsUntouched(t *testing.T) {
	f := &fakeTaskRunner{run: func(int, string, []string) ([]byte, error) { return []byte("ready"), nil }}
	err := installTask(context.Background(), f, "WorkTracker", "x", "y")
	if err == nil || len(f.calls) != 1 {
		t.Fatalf("err=%v calls=%#v", err, f.calls)
	}
}

func TestStartFailureRollsBackNewTask(t *testing.T) {
	f := &fakeTaskRunner{}
	f.run = func(n int, _ string, args []string) ([]byte, error) {
		switch n {
		case 1:
			return []byte("ERROR: cannot find"), errors.New("exit 1")
		case 2:
			return []byte("created"), nil
		case 3:
			return []byte("start denied"), errors.New("exit 5")
		default:
			if strings.Contains(strings.Join(args, " "), "/Delete") {
				return []byte("deleted"), nil
			}
			return nil, nil
		}
	}
	err := installTask(context.Background(), f, "WorkTracker", "x", "y")
	if err == nil || !strings.Contains(err.Error(), "rolled back") || len(f.calls) != 4 || !strings.Contains(strings.Join(f.calls[3].args, " "), "/Delete") {
		t.Fatalf("err=%v calls=%#v", err, f.calls)
	}
}
