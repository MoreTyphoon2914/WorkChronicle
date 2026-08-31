//go:build windows

package windows

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var createMutex = kernel32.NewProc("CreateMutexW")
var releaseMutex = kernel32.NewProc("ReleaseMutex")

var ErrCollectorAlreadyRunning = errors.New("WorkChronicle collector is already running; stop the existing instance before starting another")

type Mutex struct{ handle syscall.Handle }

func AcquireMutex(key string) (*Mutex, error) {
	sum := sha256.Sum256([]byte(key))
	name := fmt.Sprintf("Local\\WorkTracker-%x", sum[:8])
	p, _ := syscall.UTF16PtrFromString(name)
	h, _, callErr := createMutex.Call(0, 1, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return nil, fmt.Errorf("create WorkChronicle single-instance mutex: %w", callErr)
	}
	// CreateMutexW reports ERROR_ALREADY_EXISTS through the call's captured
	// last-error value while still returning a valid handle. Reading
	// GetLastError later is racy and can miss the conflict.
	if callErr == syscall.ERROR_ALREADY_EXISTS {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, ErrCollectorAlreadyRunning
	}
	return &Mutex{handle: syscall.Handle(h)}, nil
}
func (m *Mutex) Close() error {
	if m == nil || m.handle == 0 {
		return nil
	}
	handle := m.handle
	m.handle = 0
	releaseMutex.Call(uintptr(handle))
	return syscall.CloseHandle(handle)
}
