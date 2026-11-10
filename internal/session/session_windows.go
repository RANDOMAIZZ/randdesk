//go:build windows

// Package session — помощник в сеансе пользователя: отключение физических клавиатуры и мыши
// по команде администратора и единый буфер обмена между ПК.
package session

import (
	"encoding/json"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/windows"

	"randdesk/internal/desktop"
	"randdesk/internal/proto"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pSetWindowsHookExW          = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx        = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx             = user32.NewProc("CallNextHookEx")
	pGetMessageW                = user32.NewProc("GetMessageW")
	pTranslateMessage           = user32.NewProc("TranslateMessage")
	pDispatchMessageW           = user32.NewProc("DispatchMessageW")
	pPostThreadMessageW         = user32.NewProc("PostThreadMessageW")
	pPostMessageW               = user32.NewProc("PostMessageW")
	pRegisterClassExW           = user32.NewProc("RegisterClassExW")
	pCreateWindowExW            = user32.NewProc("CreateWindowExW")
	pDefWindowProcW             = user32.NewProc("DefWindowProcW")
	pAddClipboardFormatListener = user32.NewProc("AddClipboardFormatListener")
	pOpenClipboard              = user32.NewProc("OpenClipboard")
	pCloseClipboard             = user32.NewProc("CloseClipboard")
	pEmptyClipboard             = user32.NewProc("EmptyClipboard")
	pGetClipboardData           = user32.NewProc("GetClipboardData")
	pSetClipboardData           = user32.NewProc("SetClipboardData")
	pGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
	pGetCurrentThreadId         = kernel32.NewProc("GetCurrentThreadId")
	pGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	pGlobalLock                 = kernel32.NewProc("GlobalLock")
	pGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	pGlobalFree                 = kernel32.NewProc("GlobalFree")
)

const (
	whKeyboardLL      = 13
	whMouseLL         = 14
	llkhfInjected     = 0x10
	llmhfInjected     = 0x01
	wmQuit            = 0x0012
	wmClipboardUpdate = 0x031D
	wmApp             = 0x8000
	cfUnicodeText     = 13
	gmemMoveable      = 0x0002
	maxClip           = 1 << 20 // символов
)

type msg struct {
	hwnd    uintptr
	message uint32
	wparam  uintptr
	lparam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

type kbdHook struct {
	vk, scan, flags, time uint32
	extra                 uintptr
}

type mouseHook struct {
	pt                struct{ x, y int32 }
	data, flags, time uint32
	extra             uintptr
}

// ptr превращает адрес из Win32 API в указатель (память принадлежит системе, а не сборщику мусора Go).
func ptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// ---------- блокировка ввода ----------

var lockKbd, lockMouse atomic.Bool

var (
	kbdProc = windows.NewCallback(func(code int32, wparam, lparam uintptr) uintptr {
		if code == 0 && lockKbd.Load() {
			if (*kbdHook)(ptr(lparam)).flags&llkhfInjected == 0 {
				return 1 // физическая клавиатура отключена; удалённый ввод (injected) проходит
			}
		}
		r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wparam, lparam)
		return r
	})
	mouseProc = windows.NewCallback(func(code int32, wparam, lparam uintptr) uintptr {
		if code == 0 && lockMouse.Load() {
			if (*mouseHook)(ptr(lparam)).flags&llmhfInjected == 0 {
				return 1
			}
		}
		r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wparam, lparam)
		return r
	})
)

// hooker держит низкоуровневые перехватчики, только пока что-то отключено:
// без блокировки перехват не установлен и не влияет на отзывчивость ввода.
type hooker struct {
	mu       sync.Mutex
	threadID uintptr
	done     chan struct{}
	hideBar  func()
}

func (h *hooker) apply(kbd, mouse bool) {
	lockKbd.Store(kbd)
	lockMouse.Store(mouse)
	h.mu.Lock()
	defer h.mu.Unlock()
	need := kbd || mouse
	if need && h.done == nil {
		h.start()
	}
	if !need && h.done != nil {
		pPostThreadMessageW.Call(h.threadID, wmQuit, 0, 0)
		<-h.done
		h.done = nil
	}
	if h.hideBar != nil {
		h.hideBar()
		h.hideBar = nil
	}
	if need {
		text := "RandDesk · администратор отключил "
		switch {
		case kbd && mouse:
			text += "клавиатуру и мышь"
		case kbd:
			text += "клавиатуру"
		default:
			text += "мышь"
		}
		h.hideBar = desktop.ShowIndicator(text)
	}
}

func (h *hooker) start() {
	ready := make(chan uintptr)
	h.done = make(chan struct{})
	done := h.done
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		tid, _, _ := pGetCurrentThreadId.Call()
		inst, _, _ := pGetModuleHandleW.Call(0)
		hk, _, _ := pSetWindowsHookExW.Call(whKeyboardLL, kbdProc, inst, 0)
		hm, _, _ := pSetWindowsHookExW.Call(whMouseLL, mouseProc, inst, 0)
		if hk == 0 || hm == 0 {
			log.Printf("блокировка ввода: не удалось установить перехват")
		}
		ready <- tid
		var m msg
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		if hk != 0 {
			pUnhookWindowsHookEx.Call(hk)
		}
		if hm != 0 {
			pUnhookWindowsHookEx.Call(hm)
		}
	}()
	h.threadID = <-ready
}

// ---------- буфер обмена ----------

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type clipboard struct {
	hwnd    uintptr
	enabled atomic.Bool
	mu      sync.Mutex
	pending string // текст для записи (передаётся в поток окна)
	last    string // последний текст, записанный или отправленный нами (защита от петли)
	onCopy  func(string)
}

var clip *clipboard

var clipProc = windows.NewCallback(func(hwnd, m, wparam, lparam uintptr) uintptr {
	switch uint32(m) {
	case wmClipboardUpdate:
		if clip.enabled.Load() {
			if t, ok := readClipboard(hwnd); ok {
				clip.mu.Lock()
				dup := t == clip.last
				if !dup {
					clip.last = t
				}
				clip.mu.Unlock()
				if !dup && t != "" && len(t) <= maxClip*3 {
					clip.onCopy(t)
				}
			}
		}
		return 0
	case wmApp:
		clip.mu.Lock()
		t := clip.pending
		clip.last = t
		clip.mu.Unlock()
		writeClipboard(hwnd, t)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wparam, lparam)
	return r
})

func openClipboard(hwnd uintptr) bool {
	for range 10 { // буфер может быть кратко занят другой программой
		if r, _, _ := pOpenClipboard.Call(hwnd); r != 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func readClipboard(hwnd uintptr) (string, bool) {
	if !openClipboard(hwnd) {
		return "", false
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(h)
	var u []uint16
	for i := uintptr(0); i < maxClip; i++ {
		c := *(*uint16)(ptr(p + i*2))
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u)), true
}

func writeClipboard(hwnd uintptr, t string) {
	u := utf16.Encode([]rune(t))
	u = append(u, 0)
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(len(u)*2))
	if h == 0 {
		return
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return
	}
	copy(unsafe.Slice((*uint16)(ptr(p)), len(u)), u)
	pGlobalUnlock.Call(h)
	if !openClipboard(hwnd) {
		pGlobalFree.Call(h)
		return
	}
	pEmptyClipboard.Call()
	if r, _, _ := pSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		pGlobalFree.Call(h)
	}
	pCloseClipboard.Call()
}

func (c *clipboard) set(t string) {
	c.mu.Lock()
	c.pending = t
	c.mu.Unlock()
	pPostMessageW.Call(c.hwnd, wmApp, 0, 0)
}

func startClipboard(onCopy func(string)) *clipboard {
	c := &clipboard{onCopy: onCopy}
	clip = c
	ready := make(chan uintptr)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		inst, _, _ := pGetModuleHandleW.Call(0)
		cls, _ := windows.UTF16PtrFromString("RandDeskClipboard")
		wc := wndClassEx{wndProc: clipProc, instance: inst, className: cls}
		wc.size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		const hwndMessage = ^uintptr(2) // HWND_MESSAGE = -3
		hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, inst, 0)
		if hwnd != 0 {
			pAddClipboardFormatListener.Call(hwnd)
		}
		ready <- hwnd
		var m msg
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	c.hwnd = <-ready
	if c.hwnd == 0 {
		log.Printf("буфер обмена: не удалось создать окно")
	}
	return c
}

// ---------- связь с хабом ----------

// Serve обслуживает соединение с хабом до его закрытия.
func Serve(ws *websocket.Conn) error {
	var wmu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
		ws.WriteMessage(websocket.TextMessage, b)
	}
	h := &hooker{}
	defer h.apply(false, false) // при обрыве связи ввод всегда возвращается пользователю
	c := startClipboard(func(t string) { send(proto.SessionMsg{Type: "clip", Text: t}) })

	go func() { // поддержание соединения
		for {
			time.Sleep(30 * time.Second)
			wmu.Lock()
			err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
			wmu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	ws.SetReadLimit(4 << 20)
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var m struct {
			Type      string `json:"type"`
			Keyboard  bool   `json:"keyboard"`
			Mouse     bool   `json:"mouse"`
			Clipboard bool   `json:"clipboard"`
			Text      string `json:"text"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "state":
			h.apply(m.Keyboard, m.Mouse)
			c.enabled.Store(m.Clipboard)
		case "clip":
			if c.enabled.Load() && c.hwnd != 0 {
				c.set(m.Text)
			}
		}
	}
}
