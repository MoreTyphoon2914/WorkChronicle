//go:build windows

package windows

import (
	"context"
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW prevents console-subsystem children from allocating a
// console host. HideWindow additionally asks Windows not to show any window a
// child might otherwise create through STARTUPINFO.
const createNoWindow uint32 = 0x08000000

// HiddenCommandContext is the single constructor for background child
// processes. Future Windows command integrations should use it instead of
// exec.Command or exec.CommandContext directly.
func HiddenCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	return cmd
}
