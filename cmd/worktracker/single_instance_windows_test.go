//go:build windows

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"worktracker/internal/config"
	wtwindows "worktracker/internal/windows"
)

func TestAllRunModesRejectAnExistingCollectorBeforeStartup(t *testing.T) {
	for _, test := range []struct {
		name       string
		firstTray  bool
		secondTray bool
	}{
		{name: "normal versus normal"},
		{name: "normal versus tray", secondTray: true},
		{name: "tray versus tray", firstTray: true, secondTray: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{ConfigPath: filepath.Join(t.TempDir(), "config.json")}
			// The mutex is acquired before run mode branches, so holding it
			// represents either a normal or tray-enabled first collector.
			_ = test.firstTray
			held, err := wtwindows.AcquireMutex(cfg.ConfigPath)
			if err != nil {
				t.Fatalf("hold first collector mutex: %v", err)
			}
			defer held.Close()

			var stdout, stderr bytes.Buffer
			code := runCollector(context.Background(), cfg, test.secondTray, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("second run exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "WorkSense collector is already running") {
				t.Fatalf("missing clear conflict message: %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("runtime startup began before rejection: %q", stdout.String())
			}
		})
	}
}
