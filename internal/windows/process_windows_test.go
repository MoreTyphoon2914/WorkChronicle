//go:build windows

package windows

import (
	"context"
	"testing"
)

func TestHiddenCommandContextConfiguresInvisibleWindowsChild(t *testing.T) {
	cmd := HiddenCommandContext(context.Background(), "powershell.exe", "-NoProfile")
	if cmd.SysProcAttr == nil {
		t.Fatal("Windows child process has no SysProcAttr")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("Windows child process is not configured with HideWindow")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("Windows child process creation flags %#x omit CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.Path == "" || len(cmd.Args) != 2 || cmd.Args[1] != "-NoProfile" {
		t.Fatalf("command construction changed path or arguments: path=%q args=%#v", cmd.Path, cmd.Args)
	}
}
