package windows

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return HiddenCommandContext(ctx, name, args...).CombinedOutput()
}

func TaskExists(ctx context.Context, name string) (bool, error) {
	return taskExists(ctx, execRunner{}, name)
}
func taskExists(ctx context.Context, r commandRunner, name string) (bool, error) {
	b, err := r.Run(ctx, "schtasks.exe", "/Query", "/TN", name)
	if err != nil {
		if strings.Contains(strings.ToLower(string(b)), "cannot find") || strings.Contains(strings.ToLower(string(b)), "does not exist") {
			return false, nil
		}
		return false, fmt.Errorf("query scheduled task: %w: %s", err, sanitizeTaskOutput(b))
	}
	return true, nil
}

func InstallTask(ctx context.Context, name, exePath, configPath string) error {
	return installTask(ctx, execRunner{}, name, exePath, configPath)
}
func installTask(ctx context.Context, r commandRunner, name, exePath, configPath string) error {
	exists, err := taskExists(ctx, r, name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("scheduled task %q already exists; it was not changed", name)
	}
	xmlText := taskXML(exePath, configPath)
	tmp, err := os.CreateTemp("", "worktracker-task-*.xml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.WriteString(xmlText); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	b, err := r.Run(ctx, "schtasks.exe", "/Create", "/TN", name, "/XML", tmpName, "/F")
	if err != nil {
		if created, _ := taskExists(ctx, r, name); created {
			_, _ = r.Run(ctx, "schtasks.exe", "/Delete", "/TN", name, "/F")
		}
		return fmt.Errorf("Task Scheduler registration blocked or failed; no security policy was changed: %w: %s", err, sanitizeTaskOutput(b))
	}
	if b, err = r.Run(ctx, "schtasks.exe", "/Run", "/TN", name); err != nil {
		cleanup, cleanupErr := r.Run(ctx, "schtasks.exe", "/Delete", "/TN", name, "/F")
		if cleanupErr != nil {
			return fmt.Errorf("task registered but could not be started, and cleanup failed: %w: %s; cleanup: %s", err, sanitizeTaskOutput(b), sanitizeTaskOutput(cleanup))
		}
		return fmt.Errorf("task could not be started and the new registration was rolled back: %w: %s", err, sanitizeTaskOutput(b))
	}
	return nil
}
func UninstallTask(ctx context.Context, name string) error {
	exists, err := TaskExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("scheduled task %q does not exist", name)
	}
	b, err := execRunner{}.Run(ctx, "schtasks.exe", "/Delete", "/TN", name, "/F")
	if err != nil {
		return fmt.Errorf("remove scheduled task: %w: %s", err, sanitizeTaskOutput(b))
	}
	return nil
}

func taskXML(exePath, configPath string) string {
	escape := func(s string) string { var b strings.Builder; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	args := `run --config "` + configPath + `"`
	return `<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers><Principals><Principal id="Author"><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><Enabled>true</Enabled></Settings><Actions Context="Author"><Exec><Command>` + escape(filepath.Clean(exePath)) + `</Command><Arguments>` + escape(args) + `</Arguments></Exec></Actions></Task>`
}
func sanitizeTaskOutput(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}
