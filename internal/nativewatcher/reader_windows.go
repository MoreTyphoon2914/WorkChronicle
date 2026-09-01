//go:build windows

package nativewatcher

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	desktopSwitchDesktop           = 0x0100
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	getForegroundWindow       = user32.NewProc("GetForegroundWindow")
	getWindowThreadProcessID  = user32.NewProc("GetWindowThreadProcessId")
	getWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	getWindowTextW            = user32.NewProc("GetWindowTextW")
	getLastInputInfo          = user32.NewProc("GetLastInputInfo")
	openInputDesktop          = user32.NewProc("OpenInputDesktop")
	switchDesktop             = user32.NewProc("SwitchDesktop")
	closeDesktop              = user32.NewProc("CloseDesktop")
	openProcess               = kernel32.NewProc("OpenProcess")
	closeHandle               = kernel32.NewProc("CloseHandle")
	queryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	getTickCount64            = kernel32.NewProc("GetTickCount64")
)

type WindowsReader struct{}

type lastInputInfo struct {
	Size uint32
	Time uint32
}

func (WindowsReader) Foreground(context.Context) (Foreground, error) {
	hwnd, _, _ := getForegroundWindow.Call()
	if hwnd == 0 {
		return Foreground{}, fmt.Errorf("no foreground window")
	}
	var pid uint32
	getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return Foreground{}, fmt.Errorf("foreground process unavailable")
	}
	handle, _, callErr := openProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return Foreground{}, fmt.Errorf("open foreground process: %w", callErr)
	}
	defer closeHandle.Call(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	ok, _, callErr := queryFullProcessImageName.Call(handle, 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return Foreground{}, fmt.Errorf("query foreground executable: %w", callErr)
	}
	return Foreground{Executable: filepath.Base(syscall.UTF16ToString(buffer[:size])), Title: windowTitle(hwnd)}, nil
}

func (WindowsReader) IdleDuration(context.Context) (time.Duration, error) {
	info := lastInputInfo{Size: uint32(unsafe.Sizeof(lastInputInfo{}))}
	ok, _, callErr := getLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0, fmt.Errorf("GetLastInputInfo: %w", callErr)
	}
	ticks, _, _ := getTickCount64.Call()
	delta := uint32(ticks) - info.Time
	return time.Duration(delta) * time.Millisecond, nil
}

func (WindowsReader) SessionLocked(context.Context) (bool, error) {
	desktop, _, callErr := openInputDesktop.Call(0, 0, desktopSwitchDesktop)
	if desktop == 0 {
		// The signed-in user cannot open Winlogon's secure input desktop while
		// the workstation is locked. Treat that documented access boundary as
		// explicit lock evidence; retain other failures as degraded health.
		if callErr == syscall.ERROR_ACCESS_DENIED {
			return true, nil
		}
		return false, fmt.Errorf("OpenInputDesktop: %w", callErr)
	}
	defer closeDesktop.Call(desktop)
	ok, _, _ := switchDesktop.Call(desktop)
	return ok == 0, nil
}

func windowTitle(hwnd uintptr) string {
	length, _, _ := getWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, length+1)
	written, _, _ := getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if written == 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer[:written])
}
