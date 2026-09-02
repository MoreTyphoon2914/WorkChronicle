//go:build windows

package nativewatcher

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmDestroy            = 0x0002
	wmClose              = 0x0010
	wmWTSSessionChange   = 0x02B1
	wtsSessionLock       = 0x7
	wtsSessionUnlock     = 0x8
	notifyForThisSession = 0
)

var (
	wtsapi32                         = syscall.NewLazyDLL("wtsapi32.dll")
	getModuleHandleW                 = kernel32.NewProc("GetModuleHandleW")
	registerClassExW                 = user32.NewProc("RegisterClassExW")
	unregisterClassW                 = user32.NewProc("UnregisterClassW")
	createWindowExW                  = user32.NewProc("CreateWindowExW")
	destroyWindow                    = user32.NewProc("DestroyWindow")
	defWindowProcW                   = user32.NewProc("DefWindowProcW")
	getMessageW                      = user32.NewProc("GetMessageW")
	translateMessage                 = user32.NewProc("TranslateMessage")
	dispatchMessageW                 = user32.NewProc("DispatchMessageW")
	postMessageW                     = user32.NewProc("PostMessageW")
	postQuitMessage                  = user32.NewProc("PostQuitMessage")
	wtsRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	wtsUnregisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
	sessionWindows                   sync.Map
	sessionClassSequence             atomic.Uint64
	sessionWindowCallback            = syscall.NewCallback(sessionWindowProc)
)

type point struct{ X, Y int32 }

type windowMessage struct {
	HWND    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type windowClassEx struct {
	Size        uint32
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
	SmallIcon   uintptr
}

type windowsSessionMonitor struct {
	state     *sessionState
	notify    func(SessionTransition)
	hwnd      atomic.Uintptr
	done      chan struct{}
	closeOnce sync.Once
}

type sessionStartResult struct{ err error }

func startWindowsSessionMonitor(state *sessionState, notify func(SessionTransition)) (*windowsSessionMonitor, error) {
	monitor := &windowsSessionMonitor{state: state, notify: notify, done: make(chan struct{})}
	ready := make(chan sessionStartResult, 1)
	go monitor.messageLoop(ready)
	result := <-ready
	if result.err != nil {
		<-monitor.done
		return nil, result.err
	}
	return monitor, nil
}

func (m *windowsSessionMonitor) messageLoop(ready chan<- sessionStartResult) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(m.done)

	instance, _, callErr := getModuleHandleW.Call(0)
	if instance == 0 {
		ready <- sessionStartResult{err: fmt.Errorf("GetModuleHandleW: %w", callErr)}
		return
	}
	name, err := syscall.UTF16PtrFromString(fmt.Sprintf("WorkChronicleSessionMonitor_%d", sessionClassSequence.Add(1)))
	if err != nil {
		ready <- sessionStartResult{err: err}
		return
	}
	class := windowClassEx{Size: uint32(unsafe.Sizeof(windowClassEx{})), WindowProc: sessionWindowCallback, Instance: instance, ClassName: name}
	atom, _, callErr := registerClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		ready <- sessionStartResult{err: fmt.Errorf("RegisterClassExW: %w", callErr)}
		return
	}
	defer unregisterClassW.Call(uintptr(unsafe.Pointer(name)), instance)

	hwnd, _, callErr := createWindowExW.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- sessionStartResult{err: fmt.Errorf("CreateWindowExW: %w", callErr)}
		return
	}
	m.hwnd.Store(hwnd)
	sessionWindows.Store(hwnd, m)
	defer sessionWindows.Delete(hwnd)
	defer destroyWindow.Call(hwnd)

	ok, _, callErr := wtsRegisterSessionNotification.Call(hwnd, notifyForThisSession)
	if ok == 0 {
		ready <- sessionStartResult{err: fmt.Errorf("WTSRegisterSessionNotification: %w", callErr)}
		return
	}
	defer wtsUnregisterSessionNotification.Call(hwnd)
	ready <- sessionStartResult{}

	var message windowMessage
	for {
		result, _, callErr := getMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			_ = callErr
			return
		}
		if result == 0 {
			return
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&message)))
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func sessionWindowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if value, ok := sessionWindows.Load(hwnd); ok {
		monitor := value.(*windowsSessionMonitor)
		switch message {
		case wmWTSSessionChange:
			state := SessionUnknown
			switch wParam {
			case wtsSessionLock:
				state = SessionLocked
			case wtsSessionUnlock:
				state = SessionUnlocked
			}
			if state != SessionUnknown {
				transition := SessionTransition{State: state, ObservedAt: time.Now().UTC()}
				if monitor.state.notification(state, transition.ObservedAt) && monitor.notify != nil {
					go monitor.notify(transition)
				}
			}
		case wmClose:
			destroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			postQuitMessage.Call(0)
			return 0
		}
	}
	result, _, _ := defWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

func (m *windowsSessionMonitor) Close() error {
	m.closeOnce.Do(func() {
		if hwnd := m.hwnd.Load(); hwnd != 0 {
			postMessageW.Call(hwnd, wmClose, 0, 0)
		}
	})
	<-m.done
	return nil
}
