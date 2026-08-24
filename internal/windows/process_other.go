//go:build !windows

package windows

import (
	"context"
	"os/exec"
)

// HiddenCommandContext preserves the same command-construction API on
// non-Windows systems, where Windows console creation flags do not apply.
func HiddenCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
