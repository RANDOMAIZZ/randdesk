//go:build windows

package desktop

import (
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Плашка на управляемом ПК: «идёт удалённое управление» — полупрозрачная «стеклянная»
// капсула вверху экрана с пульсирующим индикатором. Поверх всех окон, не перехватывает клики
// и (Windows 10 2004+) не попадает в сам захват экрана. Появляется и исчезает плавно.

var (
	gdi32                        = windows.NewLazySystemDLL("gdi32.dll")
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	procUpdateLayeredWindow      = user32.NewProc("UpdateLayeredWindow")
	procSetTimer                 = user32.NewProc("SetTimer")
	procKillTimer                = user32.NewProc("KillTimer")
	procGetDC                    = user32.NewProc("GetDC")
	procReleaseDC                = user32.NewProc("ReleaseDC")
	procGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	procSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	procCreateFontW              = gdi32.NewProc("CreateFontW")
	procSelectObject             = gdi32.NewProc("SelectObject")
	procDeleteObject             = gdi32.NewProc("DeleteObject")
	procSetBkMode                = gdi32.NewProc("SetBkMode")
	procSetTextColor             = gdi32.NewProc("SetTextColor")
	procCreateCompatibleDC       = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC                 = gdi32.NewProc("DeleteDC")
	procCreateDIBSection         = gdi32.NewProc("CreateDIBSection")
	procTextOutW                 = gdi32.NewProc("TextOutW")
	procGetTextExtentPoint32W    = gdi32.NewProc("GetTextExtentPoint32W")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
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

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type point struct{ X, Y int32 }
type size struct{ CX, CY int32 }
type blendFunc struct{ Op, Flags, Alpha, Format byte }

const (
	wmDestroy = 0x0002
	wmClose   = 0x0010
	wmTimer   = 0x0113
)

func ptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// dib — 32-битное изображение (BGRA, сверху вниз) в памяти, доступное и GDI, и Go.
type dib struct {
	dc, bmp, old uintptr
	pix          []byte
	w, h         int
}

func newDIB(w, h int) *dib {
	dc, _, _ := procCreateCompatibleDC.Call(0)
	bi := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits uintptr
	bmp, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == 0 {
		procDeleteDC.Call(dc)
		return nil
	}
	old, _, _ := procSelectObject.Call(dc, bmp)
	return &dib{dc: dc, bmp: bmp, old: old, pix: unsafe.Slice((*byte)(ptr(bits)), w*h*4), w: w, h: h}
}

func (d *dib) free() {
	procSelectObject.Call(d.dc, d.old)
	procDeleteObject.Call(d.bmp)
	procDeleteDC.Call(d.dc)
}

// ---------- рисование ----------

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// roundRectSDF — расстояние со знаком до скруглённого прямоугольника (отрицательное внутри).
func roundRectSDF(px, py, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	dx, dy := math.Abs(px-cx)-hx, math.Abs(py-cy)-hy
	ox, oy := math.Max(dx, 0), math.Max(dy, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(dx, dy), 0) - r
}

type rgba struct{ r, g, b, a float64 } // не умноженные на альфу, 0..1

// over накладывает цвет c с покрытием cov на пиксель (предумноженная альфа, BGRA).
func over(p []byte, c rgba, cov float64) {
	a := c.a * cov
	if a <= 0 {
		return
	}
	inv := 1 - a
	p[0] = byte(math.Min(255, c.b*a*255+float64(p[0])*inv))
	p[1] = byte(math.Min(255, c.g*a*255+float64(p[1])*inv))
	p[2] = byte(math.Min(255, c.r*a*255+float64(p[2])*inv))
	p[3] = byte(math.Min(255, a*255+float64(p[3])*inv))
}

// ---------- положение плашки ----------

// Положения: top, top-left, top-right, bottom, bottom-left, bottom-right; off — не показывать.
var (
	posMu       sync.Mutex
	indicatorAt = "top"
)

// SetIndicatorPos задаёт, где показывать плашку (настройка администратора).
func SetIndicatorPos(pos string) {
	switch pos {
	case "top", "top-left", "top-right", "bottom", "bottom-left", "bottom-right", "off":
	default:
		pos = "top"
	}
	posMu.Lock()
	indicatorAt = pos
	posMu.Unlock()
}

func indicatorPos() string {
	posMu.Lock()
	defer posMu.Unlock()
	return indicatorAt
}

type indicator struct {
	text  string
	pos   string
	scale float64
	w, h  int     // размер окна
	pad   float64 // отступ под тень
	base  []byte  // капсула и текст без анимированной точки
	frame *dib
	hwnd  uintptr
	start time.Time
	hide  time.Time // момент начала исчезновения
	dotX  float64
	dotY  float64
}

// плашки по окнам: старая может ещё плавно исчезать, когда появляется новая
var (
	indMu sync.Mutex
	inds  = map[uintptr]*indicator{}
)

func indicatorOf(hwnd uintptr) *indicator {
	indMu.Lock()
	defer indMu.Unlock()
	return inds[hwnd]
}

func (in *indicator) layout() bool {
	s := in.scale
	fh := int(11.5 * s)
	face, _ := windows.UTF16PtrFromString("Segoe UI Semibold")
	font, _, _ := procCreateFontW.Call(uintptr(-fh), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 4 /*ANTIALIASED_QUALITY*/, 0, uintptr(unsafe.Pointer(face)))
	defer procDeleteObject.Call(font)
	txt, _ := windows.UTF16FromString(in.text)
	n := len(txt) - 1

	// размер текста
	probe := newDIB(4, 4)
	if probe == nil {
		return false
	}
	procSelectObject.Call(probe.dc, font)
	var sz size
	procGetTextExtentPoint32W.Call(probe.dc, uintptr(unsafe.Pointer(&txt[0])), uintptr(n), uintptr(unsafe.Pointer(&sz)))
	probe.free()

	in.pad = 8 * s
	capH := 22 * s
	dotArea := 19 * s
	capW := dotArea + float64(sz.CX) + 10*s
	in.w, in.h = int(capW+2*in.pad), int(capH+2*in.pad)

	// маска текста: GDI рисует белым по чёрному, яркость = покрытие
	mask := newDIB(in.w, in.h)
	if mask == nil {
		return false
	}
	defer mask.free()
	procSelectObject.Call(mask.dc, font)
	procSetBkMode.Call(mask.dc, 1)
	procSetTextColor.Call(mask.dc, 0x00FFFFFF)
	tx := int32(in.pad + dotArea)
	ty := int32(in.pad + (capH-float64(sz.CY))/2)
	procTextOutW.Call(mask.dc, uintptr(tx), uintptr(ty), uintptr(unsafe.Pointer(&txt[0])), uintptr(n))

	// капсула: тень, «стекло», блик сверху, тонкая рамка, текст
	in.base = make([]byte, in.w*in.h*4)
	x0, y0 := in.pad, in.pad
	x1, y1 := in.pad+capW, in.pad+capH
	r := capH / 2
	for y := 0; y < in.h; y++ {
		for x := 0; x < in.w; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			p := in.base[(y*in.w+x)*4:][:4]
			// мягкая тень, смещённая вниз
			ds := roundRectSDF(px, py-1.5*s, x0, y0, x1, y1, r)
			if ds > -2 {
				sh := math.Exp(-math.Pow(math.Max(ds, 0)/(4*s), 2)) * 0.28
				over(p, rgba{0, 0, 0, 1}, sh)
			}
			d := roundRectSDF(px, py, x0, y0, x1, y1, r)
			fill := clamp01(0.5 - d)
			if fill > 0 {
				t := (py - y0) / (y1 - y0) // вертикальный градиент «стекла»
				over(p, rgba{0.075 + 0.05*(1-t), 0.08 + 0.05*(1-t), 0.11 + 0.06*(1-t), 0.48}, fill)
				// рамка: светлее сверху
				ring := clamp01(1 - math.Abs(d+0.9*s)/(0.9*s))
				over(p, rgba{1, 1, 1, 0.07 + 0.10*(1-t)}, ring*fill)
			}
			if m := float64(mask.pix[(y*in.w+x)*4+1]) / 255; m > 0 {
				over(p, rgba{0.96, 0.97, 1, 0.88}, m)
			}
		}
	}
	in.dotX, in.dotY = in.pad+11*s, in.pad+capH/2
	return true
}

// render рисует кадр: основа + пульсирующая точка; возвращает общую прозрачность окна.
func (in *indicator) render(now time.Time) (alpha byte, yOff int, done bool) {
	copy(in.frame.pix, in.base)
	s := in.scale
	ph := math.Mod(now.Sub(in.start).Seconds(), 1.6) / 1.6
	ringR := (3 + 5*ph) * s
	ringA := 0.5 * (1 - ph)
	red := rgba{1, 0.27, 0.23, 1}
	x0, x1 := int(in.dotX-10*s), int(in.dotX+10*s)
	y0, y1 := int(in.dotY-10*s), int(in.dotY+10*s)
	for y := max(y0, 0); y < min(y1, in.h); y++ {
		for x := max(x0, 0); x < min(x1, in.w); x++ {
			d := math.Hypot(float64(x)+0.5-in.dotX, float64(y)+0.5-in.dotY)
			p := in.frame.pix[(y*in.w+x)*4:][:4]
			// расходящееся кольцо
			if rc := clamp01(0.9*s - math.Abs(d-ringR)); rc > 0 {
				over(p, rgba{red.r, red.g, red.b, ringA}, rc/(0.9*s))
			}
			// свечение и сама точка
			over(p, rgba{red.r, red.g, red.b, 0.3}, clamp01(1-(d-3*s)/(3.5*s))*0.6)
			over(p, red, clamp01(3*s+0.5-d))
			over(p, rgba{1, 0.75, 0.72, 1}, clamp01(1.1*s+0.5-math.Hypot(float64(x)+0.5-in.dotX+0.8*s, float64(y)+0.5-in.dotY+0.8*s))*0.7)
		}
	}
	// появление: выезд сверху и проявление; исчезновение — обратное
	ease := func(t float64) float64 { t = clamp01(t); return 1 - math.Pow(1-t, 3) }
	k := ease(now.Sub(in.start).Seconds() / 0.45)
	if !in.hide.IsZero() {
		k = math.Min(k, 1-ease(now.Sub(in.hide).Seconds()/0.3))
		if now.Sub(in.hide) > 300*time.Millisecond {
			done = true
		}
	}
	return byte(215 * k), int(-(1 - k) * 16 * s), done // плашка в целом полупрозрачная
}

func (in *indicator) present(now time.Time) bool {
	alpha, yOff, done := in.render(now)
	// рабочая область основного монитора (без панели задач)
	var wa struct{ L, T, R, B int32 }
	if r, _, _ := procSystemParametersInfoW.Call(0x0030 /*SPI_GETWORKAREA*/, 0, uintptr(unsafe.Pointer(&wa)), 0); r == 0 {
		wa.R, wa.B = int32(sysMetric(0)), int32(sysMetric(1))
	}
	m, pad := int32(6*in.scale), int32(in.pad)
	var dst point
	switch {
	case strings.HasSuffix(in.pos, "left"):
		dst.X = wa.L + m - pad
	case strings.HasSuffix(in.pos, "right"):
		dst.X = wa.R - int32(in.w) - m + pad
	default:
		dst.X = wa.L + (wa.R-wa.L-int32(in.w))/2
	}
	if strings.HasPrefix(in.pos, "bottom") {
		dst.Y = wa.B - int32(in.h) - m + pad - int32(yOff) // выезжает снизу
	} else {
		dst.Y = wa.T + m - pad + int32(yOff) // выезжает сверху
	}
	sz := size{int32(in.w), int32(in.h)}
	src := point{}
	bf := blendFunc{0, 0, alpha, 1} // AC_SRC_OVER, AC_SRC_ALPHA
	screen, _, _ := procGetDC.Call(0)
	procUpdateLayeredWindow.Call(in.hwnd, screen, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&sz)),
		in.frame.dc, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&bf)), 2 /*ULW_ALPHA*/)
	procReleaseDC.Call(0, screen)
	return done
}

func indicatorProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmTimer:
		if in := indicatorOf(hwnd); in != nil && in.present(time.Now()) {
			procDestroyWindow.Call(hwnd)
		}
		return 0
	case wmClose:
		if in := indicatorOf(hwnd); in != nil && in.hide.IsZero() {
			in.hide = time.Now() // плавное исчезновение, окно закроется по таймеру
			return 0
		}
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procKillTimer.Call(hwnd, 1)
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// showIndicator показывает плашку и возвращает функцию, убирающую её.
// mandatory — показать, даже если администратор выключил плашку (например, «ввод отключён»).
func showIndicator(text string, mandatory bool) func() {
	pos := indicatorPos()
	if pos == "off" {
		if !mandatory {
			return func() {}
		}
		pos = "top"
	}
	ready := make(chan uintptr, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		setDPIAware()
		dpi := uintptr(96)
		if procGetDpiForSystem.Find() == nil {
			if d, _, _ := procGetDpiForSystem.Call(); d >= 96 {
				dpi = d
			}
		}
		in := &indicator{text: text, pos: pos, scale: float64(dpi) / 96, start: time.Now()}
		if !in.layout() {
			ready <- 0
			return
		}
		if in.frame = newDIB(in.w, in.h); in.frame == nil {
			ready <- 0
			return
		}
		defer in.frame.free()

		inst, _, _ := procGetModuleHandleW.Call(0)
		cls, _ := windows.UTF16PtrFromString("RandDeskIndicator")
		wc := wndClassEx{WndProc: syscall.NewCallback(indicatorProc), Instance: inst, ClassName: cls}
		wc.Size = uint32(unsafe.Sizeof(wc))
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		exStyle := uintptr(0x8 | 0x80 | 0x08000000 | 0x80000 | 0x20) // TOPMOST|TOOLWINDOW|NOACTIVATE|LAYERED|TRANSPARENT
		hwnd, _, _ := procCreateWindowExW.Call(exStyle, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(cls)),
			0x80000000, 0, 0, uintptr(in.w), uintptr(in.h), 0, 0, inst, 0) // WS_POPUP
		if hwnd == 0 {
			ready <- 0
			return
		}
		in.hwnd = hwnd
		indMu.Lock()
		inds[hwnd] = in
		indMu.Unlock()
		if os.Getenv("RANDDESK_INDICATOR_CAPTURE") == "" { // для проверки внешнего вида плашки на снимке экрана
			procSetWindowDisplayAffinity.Call(hwnd, 0x11) // WDA_EXCLUDEFROMCAPTURE
		}
		in.present(time.Now())
		procShowWindow.Call(hwnd, 4) // SW_SHOWNOACTIVATE
		procSetTimer.Call(hwnd, 1, 33, 0)
		ready <- hwnd
		var m winMsg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		indMu.Lock()
		delete(inds, hwnd)
		indMu.Unlock()
	}()
	hwnd := <-ready
	return func() {
		if hwnd != 0 {
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
		}
	}
}

// ShowIndicator показывает плашку (не попадает в захват) и возвращает функцию, убирающую её.
func ShowIndicator(text string) func() { return showIndicator(text, false) }

// ShowNotice — то же, но показывается всегда, даже если плашка выключена в настройках.
func ShowNotice(text string) func() { return showIndicator(text, true) }
