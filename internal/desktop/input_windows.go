//go:build windows

package desktop

import (
	"unicode/utf16"
	"unsafe"
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove       = 0x0001
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseXDown      = 0x0080
	mouseXUp        = 0x0100
	mouseWheel      = 0x0800
	mouseHWheel     = 0x1000
	mouseVirtual    = 0x4000
	mouseAbsolute   = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyUnicode  = 0x0004
	keyScancode = 0x0008
)

// Раскладка структур INPUT для amd64 (размер 40 байт).
type mouseInput struct {
	Type      uint32
	_         uint32
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	Extra     uintptr
}

type keybdInput struct {
	Type  uint32
	_     uint32
	Vk    uint16
	Scan  uint16
	Flags uint32
	Time  uint32
	Extra uintptr
	_     [8]byte
}

func sendMouse(in ...mouseInput) {
	if len(in) == 0 {
		return
	}
	procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

func sendKeys(in ...keybdInput) {
	if len(in) == 0 {
		return
	}
	procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

// absMove — абсолютное перемещение в координатах виртуального экрана.
func absMove(sx, sy int) mouseInput {
	vx, vy, vw, vh := virtualScreen()
	if vw < 2 || vh < 2 {
		return mouseInput{Type: inputMouse}
	}
	nx := int32((int64(sx-vx)*65535 + int64(vw-1)/2) / int64(vw-1))
	ny := int32((int64(sy-vy)*65535 + int64(vh-1)/2) / int64(vh-1))
	return mouseInput{Type: inputMouse, Dx: nx, Dy: ny, Flags: mouseMove | mouseAbsolute | mouseVirtual}
}

// buttonFlags: кнопки браузера 0 — левая, 1 — средняя, 2 — правая, 3/4 — «назад/вперёд».
func buttonFlags(b int, down bool) (flags, data uint32) {
	switch b {
	case 0:
		if down {
			return mouseLeftDown, 0
		}
		return mouseLeftUp, 0
	case 1:
		if down {
			return mouseMiddleDown, 0
		}
		return mouseMiddleUp, 0
	case 2:
		if down {
			return mouseRightDown, 0
		}
		return mouseRightUp, 0
	case 3, 4:
		x := uint32(1)
		if b == 4 {
			x = 2
		}
		if down {
			return mouseXDown, x
		}
		return mouseXUp, x
	}
	return 0, 0
}

type scan struct {
	code uint16
	ext  bool
}

// keymap: KeyboardEvent.code -> скан-код (набор 1). Ввод по скан-кодам не зависит от раскладки.
var keymap = map[string]scan{
	"Escape": {0x01, false}, "Digit1": {0x02, false}, "Digit2": {0x03, false}, "Digit3": {0x04, false},
	"Digit4": {0x05, false}, "Digit5": {0x06, false}, "Digit6": {0x07, false}, "Digit7": {0x08, false},
	"Digit8": {0x09, false}, "Digit9": {0x0A, false}, "Digit0": {0x0B, false}, "Minus": {0x0C, false},
	"Equal": {0x0D, false}, "Backspace": {0x0E, false}, "Tab": {0x0F, false},
	"KeyQ": {0x10, false}, "KeyW": {0x11, false}, "KeyE": {0x12, false}, "KeyR": {0x13, false},
	"KeyT": {0x14, false}, "KeyY": {0x15, false}, "KeyU": {0x16, false}, "KeyI": {0x17, false},
	"KeyO": {0x18, false}, "KeyP": {0x19, false}, "BracketLeft": {0x1A, false}, "BracketRight": {0x1B, false},
	"Enter": {0x1C, false}, "ControlLeft": {0x1D, false},
	"KeyA": {0x1E, false}, "KeyS": {0x1F, false}, "KeyD": {0x20, false}, "KeyF": {0x21, false},
	"KeyG": {0x22, false}, "KeyH": {0x23, false}, "KeyJ": {0x24, false}, "KeyK": {0x25, false},
	"KeyL": {0x26, false}, "Semicolon": {0x27, false}, "Quote": {0x28, false}, "Backquote": {0x29, false},
	"ShiftLeft": {0x2A, false}, "Backslash": {0x2B, false},
	"KeyZ": {0x2C, false}, "KeyX": {0x2D, false}, "KeyC": {0x2E, false}, "KeyV": {0x2F, false},
	"KeyB": {0x30, false}, "KeyN": {0x31, false}, "KeyM": {0x32, false}, "Comma": {0x33, false},
	"Period": {0x34, false}, "Slash": {0x35, false}, "ShiftRight": {0x36, false},
	"NumpadMultiply": {0x37, false}, "AltLeft": {0x38, false}, "Space": {0x39, false}, "CapsLock": {0x3A, false},
	"F1": {0x3B, false}, "F2": {0x3C, false}, "F3": {0x3D, false}, "F4": {0x3E, false}, "F5": {0x3F, false},
	"F6": {0x40, false}, "F7": {0x41, false}, "F8": {0x42, false}, "F9": {0x43, false}, "F10": {0x44, false},
	"NumLock": {0x45, true}, "ScrollLock": {0x46, false},
	"Numpad7": {0x47, false}, "Numpad8": {0x48, false}, "Numpad9": {0x49, false}, "NumpadSubtract": {0x4A, false},
	"Numpad4": {0x4B, false}, "Numpad5": {0x4C, false}, "Numpad6": {0x4D, false}, "NumpadAdd": {0x4E, false},
	"Numpad1": {0x4F, false}, "Numpad2": {0x50, false}, "Numpad3": {0x51, false}, "Numpad0": {0x52, false},
	"NumpadDecimal": {0x53, false}, "IntlBackslash": {0x56, false}, "F11": {0x57, false}, "F12": {0x58, false},
	"IntlRo": {0x73, false}, "IntlYen": {0x7D, false},
	"NumpadEnter": {0x1C, true}, "ControlRight": {0x1D, true}, "NumpadDivide": {0x35, true},
	"PrintScreen": {0x37, true}, "AltRight": {0x38, true}, "Home": {0x47, true}, "ArrowUp": {0x48, true},
	"PageUp": {0x49, true}, "ArrowLeft": {0x4B, true}, "ArrowRight": {0x4D, true}, "End": {0x4F, true},
	"ArrowDown": {0x50, true}, "PageDown": {0x51, true}, "Insert": {0x52, true}, "Delete": {0x53, true},
	"MetaLeft": {0x5B, true}, "MetaRight": {0x5C, true}, "ContextMenu": {0x5D, true},
}

// keyEvent — нажатие/отпускание по KeyboardEvent.code; если кода нет в таблице — по виртуальному коду.
func keyEvent(code string, vk int, down bool) {
	var f uint32
	if !down {
		f |= keyUp
	}
	if s, ok := keymap[code]; ok {
		f |= keyScancode
		if s.ext {
			f |= keyExtended
		}
		sendKeys(keybdInput{Type: inputKeyboard, Scan: s.code, Flags: f})
		return
	}
	if vk > 0 && vk < 256 {
		sendKeys(keybdInput{Type: inputKeyboard, Vk: uint16(vk), Flags: f})
	}
}

// typeText вводит произвольный текст как Unicode-символы (вставка текста).
func typeText(s string) {
	var in []keybdInput
	for _, u := range utf16.Encode([]rune(s)) {
		if u == '\n' {
			in = append(in, keybdInput{Type: inputKeyboard, Vk: 0x0D}, keybdInput{Type: inputKeyboard, Vk: 0x0D, Flags: keyUp})
			continue
		}
		if u == '\r' {
			continue
		}
		in = append(in,
			keybdInput{Type: inputKeyboard, Scan: u, Flags: keyUnicode},
			keybdInput{Type: inputKeyboard, Scan: u, Flags: keyUnicode | keyUp})
	}
	for len(in) > 0 { // порциями, чтобы не переполнять очередь ввода
		n := min(len(in), 200)
		sendKeys(in[:n]...)
		in = in[n:]
	}
}
