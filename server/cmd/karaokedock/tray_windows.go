//go:build windows

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"karaokedock/internal/api"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procSetMenuDefaultItem       = user32.NewProc("SetMenuDefaultItem")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procLoadIconW                = user32.NewProc("LoadIconW")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmAppTray       = 0x8001
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNonotify    = 0x0080

	cmdOpenAdmin = 1001
	cmdOpenTV    = 1002
	cmdOpenData  = 1003
	cmdQuit      = 1004
)

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     syscall.Handle
}

type notifyIconDataW struct {
	Size            uint32
	Wnd             syscall.Handle
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            syscall.Handle
	Tip             [128]uint16
}

type point struct{ X, Y int32 }

type winMsg struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

var activeTray struct {
	links trayLinks
	stop  func()
	nid   notifyIconDataW
}

func prepareTrayDefaults(o *options) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return
	}
	appHome := filepath.Join(base, "KaraokeDock")
	toolsBin := filepath.Join(appHome, "tools")
	_ = os.MkdirAll(toolsBin, 0o755)
	_ = os.Setenv("PATH", toolsBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if !o.metadataExplicit {
		o.metadata = filepath.Join(appHome, "data", "metadata")
	}
}

func openBrowser(target string) {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(target)
	_, _, _ = procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
}

func loadTrayIcon() syscall.Handle {
	png := api.IconPNG()
	if len(png) > 0 {
		h, _, _ := procCreateIconFromResourceEx.Call(
			uintptr(unsafe.Pointer(&png[0])),
			uintptr(len(png)),
			1,
			0x00030000,
			32,
			32,
			0,
		)
		if h != 0 {
			return syscall.Handle(h)
		}
	}
	h, _, _ := procLoadIconW.Call(0, 32512) // IDI_APPLICATION
	return syscall.Handle(h)
}

func setTrayTip(tip string) {
	u, _ := syscall.UTF16FromString(tip)
	clear(activeTray.nid.Tip[:])
	copy(activeTray.nid.Tip[:len(activeTray.nid.Tip)-1], u)
	if activeTray.nid.Wnd != 0 {
		_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&activeTray.nid)))
	}
}

func showTrayMenu(hwnd syscall.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	addItem := func(id uintptr, label string) {
		txt, _ := syscall.UTF16PtrFromString(label)
		_, _, _ = procAppendMenuW.Call(hMenu, mfString, id, uintptr(unsafe.Pointer(txt)))
	}
	addItem(cmdOpenAdmin, "Open Admin")
	addItem(cmdOpenTV, "Open TV Screen")
	addItem(cmdOpenData, "Open Data Folder")
	_, _, _ = procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
	addItem(cmdQuit, "Quit KaraokeDock")
	_, _, _ = procSetMenuDefaultItem.Call(hMenu, cmdOpenAdmin, 0)

	var pt point
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWindow.Call(uintptr(hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(hMenu, tpmRightButton|tpmReturnCmd|tpmNonotify, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)

	switch cmd {
	case cmdOpenAdmin:
		openBrowser(activeTray.links.AdminURL)
	case cmdOpenTV:
		openBrowser(activeTray.links.TVURL)
	case cmdOpenData:
		openBrowser(activeTray.links.DataDir)
	case cmdQuit:
		if activeTray.stop != nil {
			activeTray.stop()
		}
	}
}

func trayWndProc(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmAppTray:
		switch uint32(lparam & 0xFFFF) {
		case wmLButtonUp, wmLButtonDblClk:
			// Clicking the KaraokeDock tray icon opens Admin by default.
			openBrowser(activeTray.links.AdminURL)
		case wmRButtonUp, wmContextMenu:
			showTrayMenu(hwnd)
		}
		return 0
	case wmClose:
		_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&activeTray.nid)))
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return ret
}

func startTray(ctx context.Context, stop func(), links trayLinks) {
	activeTray.links = links
	activeTray.stop = stop
	ready := make(chan struct{})

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hInst, _, _ := procGetModuleHandleW.Call(0)
		clsName, _ := syscall.UTF16PtrFromString("KaraokeDockTrayClass")
		wc := wndClassExW{
			Size:      uint32(unsafe.Sizeof(wndClassExW{})),
			WndProc:   syscall.NewCallback(trayWndProc),
			Instance:  syscall.Handle(hInst),
			ClassName: clsName,
		}
		_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(clsName)), uintptr(unsafe.Pointer(clsName)), 0, 0, 0, 0, 0, 0, 0, hInst, 0)
		activeTray.nid = notifyIconDataW{
			Size:            uint32(unsafe.Sizeof(notifyIconDataW{})),
			Wnd:             syscall.Handle(hwnd),
			ID:              1,
			Flags:           nifMessage | nifIcon | nifTip,
			CallbackMessage: wmAppTray,
			Icon:            loadTrayIcon(),
		}
		setTrayTip("KaraokeDock — Running (click for Admin)")
		_, _, _ = procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&activeTray.nid)))
		close(ready)

		go func() {
			<-ctx.Done()
			_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
		}()

		var m winMsg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()

	<-ready
}

// bootstrapDesktopDeps finishes setup when the app starts, in case the
// installer couldn't: helper programs, Python AI libraries and AI models. The
// installer normally leaves nothing to do here.
func bootstrapDesktopDeps(ctx context.Context, sidecarDir, metadataDir string) {
	err := setupDeps(ctx, sidecarDir, metadataDir, "", false, func(step string) {
		setTrayTip("KaraokeDock — " + step)
		slog.Info("setting up", "step", step)
	})
	if err != nil {
		slog.Warn("setup isn't complete", "err", err)
	}
	setTrayTip("KaraokeDock — Running (click for Admin)")
}
