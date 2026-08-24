//go:build windows

package windows

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func uniqueMutexKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d-%d", t.Name(), os.Getpid(), time.Now().UnixNano())
}

func TestAcquireMutexRejectsSecondCollector(t *testing.T) {
	key := uniqueMutexKey(t)
	first, err := AcquireMutex(key)
	if err != nil {
		t.Fatalf("acquire first mutex: %v", err)
	}
	defer first.Close()

	second, err := AcquireMutex(key)
	if second != nil {
		second.Close()
		t.Fatal("second collector acquired the existing mutex")
	}
	if !errors.Is(err, ErrCollectorAlreadyRunning) {
		t.Fatalf("second collector error = %v, want ErrCollectorAlreadyRunning", err)
	}
}

func TestClosingMutexAllowsCollectorRestart(t *testing.T) {
	key := uniqueMutexKey(t)
	first, err := AcquireMutex(key)
	if err != nil {
		t.Fatalf("acquire first mutex: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first mutex: %v", err)
	}

	restarted, err := AcquireMutex(key)
	if err != nil {
		t.Fatalf("acquire mutex after clean shutdown: %v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("close restarted mutex: %v", err)
	}
}
