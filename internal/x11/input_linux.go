//go:build linux

// Package x11 — удалённый ввод и буфер обмена на рабочем столе Linux (X11, а также Xwayland).
package x11

import (
	"errors"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

// Input — удалённая мышь и клавиатура через расширение XTest.
type Input struct {
	c    *xgb.Conn
	root xproto.Window
	mu   sync.Mutex
	min  xproto.Keycode
	max  xproto.Keycode
	per  byte
	syms []xproto.Keysym // таблица раскладки: (keycode-min)*per + уровень
	held map[xproto.Keycode]bool
}

// OpenInput подключается к дисплею из переменной DISPLAY.
func OpenInput() (*Input, error) {
	c, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		return nil, errors.New("на этом рабочем столе нет расширения XTest")
	}
	setup := xproto.Setup(c)
	in := &Input{c: c, root: setup.DefaultScreen(c).Root, min: setup.MinKeycode, max: setup.MaxKeycode, held: map[xproto.Keycode]bool{}}
	in.loadMap()
	return in, nil
}

func (in *Input) Close() { in.c.Close() }

func (in *Input) loadMap() {
	n := byte(in.max - in.min + 1)
	r, err := xproto.GetKeyboardMapping(in.c, in.min, n).Reply()
	if err == nil {
		in.per, in.syms = r.KeysymsPerKeycode, r.Keysyms
	}
}

func (in *Input) fake(typ byte, detail byte, x, y int16) {
	xtest.FakeInput(in.c, typ, detail, 0, in.root, x, y, 0)
}

const (
	keyPress      = 2
	keyRelease    = 3
	buttonPress   = 4
	buttonRelease = 5
	motionNotify  = 6
)

// Move — абсолютное положение указателя в координатах экрана.
func (in *Input) Move(x, y int) {
	in.fake(motionNotify, 0, int16(x), int16(y))
	in.c.Sync()
}

// Button: b — номер кнопки из браузера (0 левая, 1 средняя, 2 правая, 3/4 назад/вперёд).
func (in *Input) Button(b int, down bool) {
	xb := map[int]byte{0: 1, 1: 2, 2: 3, 3: 8, 4: 9}[b]
	if xb == 0 {
		return
	}
	t := byte(buttonRelease)
	if down {
		t = buttonPress
	}
	in.fake(t, xb, 0, 0)
	in.c.Sync()
}

// Wheel — прокрутка (в единицах браузера, 120 ≈ один щелчок колеса).
func (in *Input) Wheel(dx, dy int) {
	click := func(b byte, n int) {
		for range min(max(n, 1), 20) {
			in.fake(buttonPress, b, 0, 0)
			in.fake(buttonRelease, b, 0, 0)
		}
	}
	if dy != 0 {
		b := byte(5) // вниз
		if dy < 0 {
			b, dy = 4, -dy
		}
		click(b, dy/100)
	}
	if dx != 0 {
		b := byte(7)
		if dx < 0 {
			b, dx = 6, -dx
		}
		click(b, dx/100)
	}
	in.c.Sync()
}

// Key — нажатие по физическому коду клавиши (KeyboardEvent.code): не зависит от раскладки.
func (in *Input) Key(code string, down bool) {
	ev, ok := evdevCode[code]
	if !ok {
		return
	}
	kc := xproto.Keycode(ev + 8) // X-код = код evdev + 8
	in.mu.Lock()
	if down {
		in.held[kc] = true
	} else {
		delete(in.held, kc)
	}
	in.mu.Unlock()
	t := byte(keyRelease)
	if down {
		t = keyPress
	}
	in.fake(t, byte(kc), 0, 0)
	in.c.Sync()
}

// ReleaseAll отпускает все нажатые клавиши (при отключении).
func (in *Input) ReleaseAll() {
	in.mu.Lock()
	keys := in.held
	in.held = map[xproto.Keycode]bool{}
	in.mu.Unlock()
	for kc := range keys {
		in.fake(keyRelease, byte(kc), 0, 0)
	}
	for _, b := range []byte{1, 2, 3} {
		in.fake(buttonRelease, b, 0, 0)
	}
	in.c.Sync()
}

func runeKeysym(r rune) xproto.Keysym {
	switch r {
	case '\n':
		return 0xff0d
	case '\t':
		return 0xff09
	}
	if r < 0x100 {
		return xproto.Keysym(r)
	}
	return xproto.Keysym(0x01000000 | r) // Unicode keysym
}

// Type вводит текст: символы, которых нет в раскладке, временно назначаются свободной клавише.
func (in *Input) Type(s string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	shift := xproto.Keycode(evdevCode["ShiftLeft"] + 8)
	for _, r := range s {
		if r == '\r' {
			continue
		}
		ks := runeKeysym(r)
		kc, level := in.find(ks)
		if kc == 0 {
			kc = in.spare()
			if kc == 0 {
				continue
			}
			xproto.ChangeKeyboardMapping(in.c, 1, kc, in.per, repeatSym(ks, int(in.per)))
			in.c.Sync()
			level = 0
		}
		if level == 1 {
			in.fake(keyPress, byte(shift), 0, 0)
		}
		in.fake(keyPress, byte(kc), 0, 0)
		in.fake(keyRelease, byte(kc), 0, 0)
		if level == 1 {
			in.fake(keyRelease, byte(shift), 0, 0)
		}
		in.c.Sync()
	}
	in.loadMap()
}

func repeatSym(ks xproto.Keysym, n int) []xproto.Keysym {
	out := make([]xproto.Keysym, n)
	for i := range out {
		out[i] = ks
	}
	return out
}

func (in *Input) find(ks xproto.Keysym) (xproto.Keycode, int) {
	if in.per == 0 {
		return 0, 0
	}
	for i := 0; i+int(in.per) <= len(in.syms); i += int(in.per) {
		for level := 0; level < 2 && level < int(in.per); level++ {
			if in.syms[i+level] == ks {
				return in.min + xproto.Keycode(i/int(in.per)), level
			}
		}
	}
	return 0, 0
}

// spare — клавиша без назначенных символов (для временного назначения при вводе текста).
func (in *Input) spare() xproto.Keycode {
	for i := len(in.syms) - int(in.per); i >= 0; i -= int(in.per) {
		empty := true
		for j := 0; j < int(in.per); j++ {
			if in.syms[i+j] != 0 {
				empty = false
				break
			}
		}
		if empty {
			return in.min + xproto.Keycode(i/int(in.per))
		}
	}
	return 0
}
