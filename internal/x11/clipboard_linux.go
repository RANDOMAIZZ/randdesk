//go:build linux

package x11

import (
	"errors"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
)

const maxClip = 4 << 20 // байт

// Clipboard — буфер обмена (выделение CLIPBOARD) с уведомлением об изменениях.
type Clipboard struct {
	c        *xgb.Conn
	win      xproto.Window
	clip     xproto.Atom
	utf8     xproto.Atom
	targets  xproto.Atom
	prop     xproto.Atom
	mu       sync.Mutex
	text     string // наш текст, пока мы владеем буфером
	last     string // последний текст, записанный или отправленный нами
	onChange func(string)
}

func intern(c *xgb.Conn, name string) (xproto.Atom, error) {
	r, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return r.Atom, nil
}

// OpenClipboard подключается к дисплею из DISPLAY; onChange вызывается при новом тексте
// в буфере (скопированном программами этого ПК).
func OpenClipboard(onChange func(string)) (*Clipboard, error) {
	c, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	if err := xfixes.Init(c); err != nil {
		c.Close()
		return nil, errors.New("на этом рабочем столе нет расширения XFixes")
	}
	if _, err := xfixes.QueryVersion(c, 5, 0).Reply(); err != nil {
		c.Close()
		return nil, err
	}
	cb := &Clipboard{c: c, onChange: onChange}
	for name, dst := range map[string]*xproto.Atom{"CLIPBOARD": &cb.clip, "UTF8_STRING": &cb.utf8, "TARGETS": &cb.targets, "RANDDESK_CLIP": &cb.prop} {
		if *dst, err = intern(c, name); err != nil {
			c.Close()
			return nil, err
		}
	}
	root := xproto.Setup(c).DefaultScreen(c).Root
	cb.win, err = xproto.NewWindowId(c)
	if err != nil {
		c.Close()
		return nil, err
	}
	xproto.CreateWindow(c, 0, cb.win, root, 0, 0, 1, 1, 0, xproto.WindowClassInputOnly, 0, 0, nil)
	xfixes.SelectSelectionInput(c, cb.win, cb.clip, xfixes.SelectionEventMaskSetSelectionOwner)
	go cb.loop()
	return cb, nil
}

func (cb *Clipboard) Close() { cb.c.Close() }

// Set делает этот ПК владельцем буфера с указанным текстом.
func (cb *Clipboard) Set(text string) {
	cb.mu.Lock()
	cb.text, cb.last = text, text
	cb.mu.Unlock()
	xproto.SetSelectionOwner(cb.c, cb.win, cb.clip, xproto.TimeCurrentTime)
	cb.c.Sync()
}

func (cb *Clipboard) loop() {
	for {
		ev, err := cb.c.WaitForEvent()
		if ev == nil && err == nil {
			return // соединение закрыто
		}
		switch e := ev.(type) {
		case xfixes.SelectionNotifyEvent:
			if e.Owner != cb.win && e.Owner != 0 {
				// буфер занял кто-то другой — запрашиваем его содержимое
				xproto.ConvertSelection(cb.c, cb.win, cb.clip, cb.utf8, cb.prop, e.SelectionTimestamp)
			}
		case xproto.SelectionNotifyEvent:
			if e.Property == 0 {
				continue
			}
			r, err := xproto.GetProperty(cb.c, true, cb.win, cb.prop, xproto.GetPropertyTypeAny, 0, maxClip/4).Reply()
			if err != nil || r.Format != 8 {
				continue
			}
			t := string(r.Value)
			cb.mu.Lock()
			dup := t == cb.last
			cb.last = t
			cb.mu.Unlock()
			if !dup && t != "" {
				cb.onChange(t)
			}
		case xproto.SelectionRequestEvent:
			cb.answer(e)
		}
	}
}

// answer отдаёт наш текст программе, которая вставляет из буфера.
func (cb *Clipboard) answer(e xproto.SelectionRequestEvent) {
	prop := e.Property
	if prop == 0 {
		prop = e.Target
	}
	cb.mu.Lock()
	text := cb.text
	cb.mu.Unlock()
	switch {
	case e.Target == cb.targets:
		atoms := []xproto.Atom{cb.targets, cb.utf8, xproto.AtomString}
		buf := make([]byte, 4*len(atoms))
		for i, a := range atoms {
			xgb.Put32(buf[i*4:], uint32(a))
		}
		xproto.ChangeProperty(cb.c, xproto.PropModeReplace, e.Requestor, prop, xproto.AtomAtom, 32, uint32(len(atoms)), buf)
	case e.Target == cb.utf8 || e.Target == xproto.AtomString:
		xproto.ChangeProperty(cb.c, xproto.PropModeReplace, e.Requestor, prop, e.Target, 8, uint32(len(text)), []byte(text))
	default:
		prop = 0 // такой формат не поддерживаем
	}
	n := xproto.SelectionNotifyEvent{Time: e.Time, Requestor: e.Requestor, Selection: e.Selection, Target: e.Target, Property: prop}
	xproto.SendEvent(cb.c, false, e.Requestor, 0, string(n.Bytes()))
	cb.c.Sync()
}
