//go:build windows

package main

// Thin Win32/COM calls: modern folder picker, dark title bar, shell open, message box.

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	coInit   = ole32.NewProc("CoInitializeEx")
	coUninit = ole32.NewProc("CoUninitialize")
	coCreate = ole32.NewProc("CoCreateInstance")
	coFree   = ole32.NewProc("CoTaskMemFree")
	shParse  = shell32.NewProc("SHCreateItemFromParsingName")
	shExec   = shell32.NewProc("ShellExecuteW")
	msgBox   = user32.NewProc("MessageBoxW")
	dwmSet   = dwmapi.NewProc("DwmSetWindowAttribute")
)

func u16(s string) uintptr { p, _ := syscall.UTF16PtrFromString(s); return uintptr(unsafe.Pointer(p)) }

func messageBox(title, text string) { msgBox.Call(0, u16(text), u16(title), 0x10) }

// flashIfBackground blinks the taskbar button when a download ends while the user is elsewhere.
func flashIfBackground(hwnd uintptr) {
	if fg, _, _ := user32.NewProc("GetForegroundWindow").Call(); fg == hwnd {
		return
	}
	fi := struct { // FLASHWINFO
		size                  uint32
		hwnd                  uintptr
		flags, count, timeout uint32
	}{hwnd: hwnd, flags: 0x3 | 0xC} // FLASHW_ALL | FLASHW_TIMERNOFG: until the window is brought forward
	fi.size = uint32(unsafe.Sizeof(fi))
	user32.NewProc("FlashWindowEx").Call(uintptr(unsafe.Pointer(&fi)))
}

// clipboardText returns the clipboard's text, or "" when it holds none.
func clipboardText() string {
	k32 := windows.NewLazySystemDLL("kernel32.dll")
	if r, _, _ := user32.NewProc("OpenClipboard").Call(0); r == 0 {
		return ""
	}
	defer user32.NewProc("CloseClipboard").Call()
	h, _, _ := user32.NewProc("GetClipboardData").Call(13) // CF_UNICODETEXT
	if h == 0 {
		return ""
	}
	p, _, _ := k32.NewProc("GlobalLock").Call(h)
	if p == 0 {
		return ""
	}
	defer k32.NewProc("GlobalUnlock").Call(h)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(p)))
}

func shellOpen(target string) { shExec.Call(0, u16("open"), u16(target), 0, 0, 1) }

// darkTitleBar matches the native caption to the system theme.
func darkTitleBar(hwnd uintptr) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("AppsUseLightTheme"); err == nil && v == 0 {
		setTitleBar(hwnd, true)
	}
}

// setTitleBar follows the theme chosen in Settings.
func setTitleBar(hwnd uintptr, dark bool) {
	var on int32
	if dark {
		on = 1
	}
	dwmSet.Call(hwnd, 20, uintptr(unsafe.Pointer(&on)), 4) // DWMWA_USE_IMMERSIVE_DARK_MODE
}

// call invokes COM vtable slot i on obj. (go vet flags the uintptr→pointer cast; obj is a COM pointer, not Go memory.)
func call(obj uintptr, i int, args ...uintptr) uintptr {
	vtbl := *(**[32]uintptr)(unsafe.Pointer(obj))
	r, _, _ := syscall.SyscallN(vtbl[i], append([]uintptr{obj}, args...)...)
	return r
}

var (
	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidShellItem        = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

// pickFolder shows the Windows 11 folder picker. Runs on its own STA thread so the
// WebView2 message handler is never blocked by a nested modal loop.
func pickFolder(owner uintptr, start string) string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	coInit.Call(0, 0x2) // COINIT_APARTMENTTHREADED
	defer coUninit.Call()

	var dlg uintptr
	if r, _, _ := coCreate.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, 1 /*INPROC_SERVER*/, uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&dlg))); r != 0 {
		return ""
	}
	defer call(dlg, 2) // Release

	var opts uint32
	call(dlg, 10, uintptr(unsafe.Pointer(&opts)))   // GetOptions
	call(dlg, 9, uintptr(opts|0x20|0x40|0x800))     // SetOptions: PICKFOLDERS | FORCEFILESYSTEM | PATHMUSTEXIST
	call(dlg, 17, u16("Choose where downloads go")) // SetTitle
	call(dlg, 18, u16("Use this folder"))           // SetOkButtonLabel
	if start != "" {
		var item uintptr
		if r, _, _ := shParse.Call(u16(start), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item))); r == 0 {
			call(dlg, 12, item) // SetFolder
			call(item, 2)
		}
	}
	if call(dlg, 3, owner) != 0 { // Show; non-zero = cancelled
		return ""
	}
	var res uintptr
	if call(dlg, 20, uintptr(unsafe.Pointer(&res))) != 0 { // GetResult
		return ""
	}
	defer call(res, 2)
	var p *uint16
	if call(res, 5, 0x80058000, uintptr(unsafe.Pointer(&p))) != 0 { // GetDisplayName(SIGDN_FILESYSPATH)
		return ""
	}
	defer coFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p)
}
