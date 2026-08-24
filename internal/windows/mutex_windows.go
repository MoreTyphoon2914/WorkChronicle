//go:build windows

package windows

import (
	"crypto/sha256"
	"fmt"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var createMutex = kernel32.NewProc("CreateMutexW")
var releaseMutex = kernel32.NewProc("ReleaseMutex")

type Mutex struct{ handle syscall.Handle }

func AcquireMutex(key string) (*Mutex, error) {
	sum := sha256.Sum256([]byte(key))
	name := fmt.Sprintf("Local\\WorkTracker-%x", sum[:8])
	p, _ := syscall.UTF16PtrFromString(name)
	h, _, e := createMutex.Call(0, 1, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return nil, e
	}
	if syscall.GetLastError() == syscall.ERROR_ALREADY_EXISTS {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, fmt.Errorf("collector is already running")
	}
	return &Mutex{handle: syscall.Handle(h)}, nil
}
func (m *Mutex) Close() error {
	if m == nil || m.handle == 0 {
		return nil
	}
	releaseMutex.Call(uintptr(m.handle))
	return syscall.CloseHandle(m.handle)
}
