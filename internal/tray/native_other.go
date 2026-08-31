//go:build !windows

package tray

import (
	"context"
	"fmt"
)

type unsupportedUI struct{}

func NewNative(NativeOptions) UI { return unsupportedUI{} }

func (unsupportedUI) Update(ViewModel) {}

func (unsupportedUI) Run(context.Context, func()) error {
	return fmt.Errorf("the WorkChronicle system tray is supported only on Windows")
}
