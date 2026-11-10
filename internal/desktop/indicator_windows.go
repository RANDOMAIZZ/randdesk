//go:build windows

package desktop

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Плашка на управляемом ПК: «идёт удалённое управление». Поверх всех окон,
// не перехватывает клики и (Windows 10 2004+) не попадает в сам захват экрана.

var (
	gdi32                          = windows.NewLazySystemDLL("gdi32.dll")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procBeginPaint                 = user32.NewProc("BeginPaint")
	procEndPaint                   = user32.NewProc("EndPaint")
	procDrawTextW                  = user32.NewProc("DrawTextW")
	procGetClientRect              = user32.NewProc("GetClientRect")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procSetWindowDisplayAffinity   = user32.NewProc("SetWindowDisplayAffinity")
	procCreateSolidBrush           = gdi32.NewProc("CreateSolidBrush")
	procCreateFontW                = gdi32.NewProc("CreateFontW")
	procSelectObject               = gdi32.NewProc("SelectObject")
	procSetBkMode                  = gdi32.NewProc("SetBkMode")
	procSetTextColor               = gdi32.NewProc("SetTextColor")
	procGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type winRect struct{ Left, Top, Right, Bottom int32 }

type paintStruct struct {
	HDC       uintptr
	Erase     int32
	Paint     winRect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
	Private uint32
}

const (
	wmDestroy = 0x0002
	wmPaint   = 0x000F
	wmClose   = 0x0010
)

var (
	indicatorText *uint16
	indicatorFont uintptr
)

func indicatorProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmPaint:
		var ps paintStruct
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		var rc winRect
		procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		procSetBkMode.Call(hdc, 1) // TRANSPARENT
		procSetTextColor.Call(hdc, 0x00FFFFFF)
		if indicatorFont != 0 {
			procSelectObject.Call(hdc, indicatorFont)
		}
		procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(indicatorText)), ^uintptr(0), uintptr(unsafe.Pointer(&rc)),
			0x1|0x4|0x20) // DT_CENTER | DT_VCENTER | DT_SINGLELINE
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// showIndicator показывает плашку и возвращает функцию, убирающую её.
func showIndicator(text string) func() {
	ready := make(chan uintptr, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		indicatorText, _ = windows.UTF16PtrFromString(text)
		face, _ := windows.UTF16PtrFromString("Segoe UI")
		indicatorFont, _, _ = procCreateFontW.Call(^uintptr(17), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))

		inst, _, _ := procGetModuleHandleW.Call(0)
		cls, _ := windows.UTF16PtrFromString("RandDeskIndicator")
		brush, _, _ := procCreateSolidBrush.Call(0x002530D9) // BGR: красный
		wc := wndClassEx{WndProc: syscall.NewCallback(indicatorProc), Instance: inst, Background: brush, ClassName: cls}
		wc.Size = uint32(unsafe.Sizeof(wc))
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		const w, h = 380, 30
		sw := sysMetric(0)                                           // SM_CXSCREEN
		exStyle := uintptr(0x8 | 0x80 | 0x08000000 | 0x80000 | 0x20) // TOPMOST|TOOLWINDOW|NOACTIVATE|LAYERED|TRANSPARENT
		hwnd, _, _ := procCreateWindowExW.Call(exStyle, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(cls)),
			0x80000000, uintptr((sw-w)/2), 0, w, h, 0, 0, inst, 0) // WS_POPUP
		if hwnd == 0 {
			ready <- 0
			return
		}
		procSetLayeredWindowAttributes.Call(hwnd, 0, 225, 2) // LWA_ALPHA
		procSetWindowDisplayAffinity.Call(hwnd, 0x11)        // WDA_EXCLUDEFROMCAPTURE
		procShowWindow.Call(hwnd, 4)                         // SW_SHOWNOACTIVATE
		ready <- hwnd
		var m winMsg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	hwnd := <-ready
	return func() {
		if hwnd != 0 {
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
		}
	}
}
