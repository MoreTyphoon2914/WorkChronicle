//go:build !windows

package nativewatcher

import (
	"context"
	"fmt"
	"time"
)

type WindowsReader struct{}

func NewWindowsReader(func(SessionTransition)) (WindowsReader, error) {
	return WindowsReader{}, fmt.Errorf("native Windows session acquisition is unavailable")
}

func (WindowsReader) Close() error { return nil }

func (WindowsReader) Foreground(context.Context) (Foreground, error) {
	return Foreground{}, fmt.Errorf("native Windows foreground acquisition is unavailable")
}
func (WindowsReader) IdleDuration(context.Context) (time.Duration, error) {
	return 0, fmt.Errorf("native Windows input acquisition is unavailable")
}
func (WindowsReader) SessionLocked(context.Context) (bool, error) {
	return false, fmt.Errorf("native Windows session acquisition is unavailable")
}
