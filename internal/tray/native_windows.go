//go:build windows

package tray

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmCommand      = 0x0111
	wmNull         = 0x0000
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1
	wmRefresh      = wmApp + 2
	nimAdd         = 0x00000000
	nimModify      = 0x00000001
	nimDelete      = 0x00000002
	nifMessage     = 0x00000001
	nifIcon        = 0x00000002
	nifTip         = 0x00000004
	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	tpmRightButton = 0x00000002
	mbOK           = 0x00000000
	mbIconInfo     = 0x00000040
	swShowNormal   = 1
	menuReports    = 1001
	menuLogs       = 1002
	menuExit       = 1003
	idiError       = 32513
	idiWarning     = 32515
	idiInformation = 32516
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procUnregisterClassW    = user32.NewProc("UnregisterClassW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW       = shell32.NewProc("ShellExecuteW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	windowProcedure         = syscall.NewCallback(trayWindowProc)
	trayWindows             sync.Map
)

type wndClassEx struct {
	Size, Style  uint32
	WndProc      uintptr
	ClassExtra   int32
	WindowExtra  int32
	Instance     uintptr
	Icon, Cursor uintptr
	Background   uintptr
	MenuName     *uint16
	ClassName    *uint16
	SmallIcon    uintptr
}

type point struct{ X, Y int32 }

type message struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type notifyIconData struct {
	Size            uint32
	Window          uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            [16]byte
	BalloonIcon     uintptr
}

type nativeUI struct {
	options   NativeOptions
	mu        sync.RWMutex
	view      ViewModel
	window    uintptr
	exit      func()
	exitOnce  sync.Once
	instance  uintptr
	className *uint16
	icons     map[IconState]uintptr
}

func NewNative(options NativeOptions) UI {
	return &nativeUI{options: options, view: FailureView(fmt.Errorf("waiting for first status refresh"))}
}

func (u *nativeUI) Update(view ViewModel) {
	u.mu.Lock()
	u.view = view
	hwnd := u.window
	u.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmRefresh, 0, 0)
	}
}

func (u *nativeUI) Run(ctx context.Context, requestExit func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u.exit = requestExit
	instance, _, _ := procGetModuleHandleW.Call(0)
	u.instance = instance
	className, err := utf16Ptr(fmt.Sprintf("WorkTrackerTray_%d", os.Getpid()))
	if err != nil {
		return err
	}
	u.className = className
	class := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: windowProcedure, Instance: instance, ClassName: className}
	if registered, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); registered == 0 {
		return fmt.Errorf("register tray window class: %w", callErr)
	}
	defer procUnregisterClassW.Call(uintptr(unsafe.Pointer(className)), instance)
	title, _ := utf16Ptr("WorkTracker")
	hwnd, _, callErr := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("create tray message window: %w", callErr)
	}
	u.mu.Lock()
	u.window = hwnd
	u.mu.Unlock()
	trayWindows.Store(hwnd, u)
	defer func() {
		trayWindows.Delete(hwnd)
		u.mu.Lock()
		u.window = 0
		u.mu.Unlock()
	}()
	u.icons = map[IconState]uintptr{
		IconWorking:   loadSystemIcon(idiInformation),
		IconBreak:     loadSystemIcon(idiWarning),
		IconUntracked: loadSystemIcon(idiError),
	}
	if !u.notify(nimAdd) {
		procDestroyWindow.Call(hwnd)
		return fmt.Errorf("add WorkTracker notification icon")
	}
	go func() {
		<-ctx.Done()
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}()
	var msg message
	for {
		result, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("read tray window message: %w", err)
		}
		if result == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func trayWindowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	value, ok := trayWindows.Load(hwnd)
	if !ok {
		result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
		return result
	}
	u := value.(*nativeUI)
	switch msg {
	case wmTrayCallback:
		switch uint32(lparam) {
		case wmLButtonUp:
			u.showStatus()
		case wmRButtonUp:
			u.showMenu()
		}
		return 0
	case wmRefresh:
		u.notify(nimModify)
		return 0
	case wmCommand:
		switch uint16(wparam & 0xffff) {
		case menuReports:
			u.openFolder(u.options.ReportsDir)
		case menuLogs:
			u.openFolder(u.options.LogsDir)
		case menuExit:
			u.exitOnce.Do(u.exit)
			procDestroyWindow.Call(hwnd)
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		u.notify(nimDelete)
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return result
}

func (u *nativeUI) notify(action uintptr) bool {
	u.mu.RLock()
	view := u.view
	hwnd := u.window
	u.mu.RUnlock()
	icon := u.icons[view.Icon]
	if icon == 0 {
		icon = u.icons[IconUntracked]
	}
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmTrayCallback, Icon: icon}
	copyUTF16(data.Tip[:], "WorkTracker — "+view.State+" (click for status)")
	result, _, _ := procShellNotifyIconW.Call(action, uintptr(unsafe.Pointer(&data)))
	return result != 0
}

func (u *nativeUI) showStatus() {
	u.mu.RLock()
	text := u.view.PopupText()
	u.mu.RUnlock()
	messageText, _ := utf16Ptr(text)
	title, _ := utf16Ptr("WorkTracker Status")
	procMessageBoxW.Call(u.window, uintptr(unsafe.Pointer(messageText)), uintptr(unsafe.Pointer(title)), mbOK|mbIconInfo)
}

func (u *nativeUI) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendMenu(menu, mfString, menuReports, "Open reports folder")
	appendMenu(menu, mfString, menuLogs, "Open logs folder")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuExit, "Exit")
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWindow.Call(u.window)
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(int64(cursor.X)), uintptr(int64(cursor.Y)), 0, u.window, 0)
	procPostMessageW.Call(u.window, wmNull, 0, 0)
}

func (u *nativeUI) openFolder(path string) {
	if path == "" {
		u.showFolderError("folder path is unavailable")
		return
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		u.showFolderError(err.Error())
		return
	}
	operation, _ := utf16Ptr("open")
	target, _ := utf16Ptr(path)
	result, _, _ := procShellExecuteW.Call(u.window, uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(target)), 0, 0, swShowNormal)
	if result <= 32 {
		u.showFolderError(fmt.Sprintf("Windows could not open %s", path))
	}
}

func (u *nativeUI) showFolderError(message string) {
	text, _ := utf16Ptr(message)
	title, _ := utf16Ptr("WorkTracker")
	procMessageBoxW.Call(u.window, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbOK)
}

func appendMenu(menu uintptr, flags uint32, id uint16, label string) {
	var labelPtr uintptr
	if label != "" {
		value, _ := utf16Ptr(label)
		labelPtr = uintptr(unsafe.Pointer(value))
	}
	procAppendMenuW.Call(menu, uintptr(flags), uintptr(id), labelPtr)
}

func loadSystemIcon(id uintptr) uintptr {
	icon, _, _ := procLoadIconW.Call(0, id)
	return icon
}

func copyUTF16(destination []uint16, value string) {
	encoded := utf16.Encode([]rune(value))
	if len(encoded) >= len(destination) {
		encoded = encoded[:len(destination)-1]
	}
	copy(destination, encoded)
}

func utf16Ptr(value string) (*uint16, error) {
	return syscall.UTF16PtrFromString(value)
}
