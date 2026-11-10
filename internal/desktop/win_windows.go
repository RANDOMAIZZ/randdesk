//go:build windows

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                            = windows.NewLazySystemDLL("user32.dll")
	procOpenInputDesktop              = user32.NewProc("OpenInputDesktop")
	procSetThreadDesktop              = user32.NewProc("SetThreadDesktop")
	procCloseDesktop                  = user32.NewProc("CloseDesktop")
	procGetUserObjectInformationW     = user32.NewProc("GetUserObjectInformationW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procSendInput                     = user32.NewProc("SendInput")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

// setDPIAware включает PerMonitorV2: координаты и захват в физических пикселях.
func setDPIAware() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(uintptr(^uintptr(3))) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	}
}

func sysMetric(i int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(i))
	return int(int32(r))
}

// virtualScreen — прямоугольник всех мониторов (для абсолютных координат мыши).
func virtualScreen() (x, y, w, h int) {
	return sysMetric(smXVirtualScreen), sysMetric(smYVirtualScreen), sysMetric(smCXVirtualScreen), sysMetric(smCYVirtualScreen)
}

// deskSwitcher привязывает поток к текущему входному рабочему столу (Default, Winlogon/UAC).
// Поток должен быть закреплён runtime.LockOSThread и не иметь окон.
type deskSwitcher struct {
	h    uintptr
	name string
}

func desktopName(h uintptr) string {
	var buf [256]uint16
	var n uint32
	r, _, _ := procGetUserObjectInformationW.Call(h, 2 /* UOI_NAME */, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

// sync переключает поток на входной рабочий стол; возвращает true, если стол сменился.
func (d *deskSwitcher) sync() (bool, error) {
	h, _, err := procOpenInputDesktop.Call(0, 0, 0x02000000 /* MAXIMUM_ALLOWED */)
	if h == 0 {
		return false, err
	}
	name := desktopName(h)
	if d.h != 0 && name == d.name {
		procCloseDesktop.Call(h)
		return false, nil
	}
	if r, _, err := procSetThreadDesktop.Call(h); r == 0 {
		procCloseDesktop.Call(h)
		return false, err
	}
	if d.h != 0 {
		procCloseDesktop.Call(d.h)
	}
	changed := d.h != 0
	d.h, d.name = h, name
	return changed, nil
}
