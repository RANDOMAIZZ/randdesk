//go:build linux

// Package session — помощник в сеансе пользователя: отключение физических клавиатуры и мыши
// по команде администратора и единый буфер обмена между ПК.
package session

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"randsec/internal/evdev"
	"randsec/internal/proto"
	"randsec/internal/x11"
)

const maxClipRunes = 1 << 20

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
	grab := evdev.NewGrabber()
	defer grab.Set(0) // при обрыве связи ввод всегда возвращается пользователю

	var clipOn atomic.Bool
	cb, err := x11.OpenClipboard(func(t string) {
		if clipOn.Load() && len([]rune(t)) <= maxClipRunes {
			send(proto.SessionMsg{Type: "clip", Text: t})
		}
	})
	if err != nil {
		log.Printf("буфер обмена недоступен: %v", err)
	} else {
		defer cb.Close()
	}

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
			var k evdev.Kind
			if m.Keyboard {
				k |= evdev.Keyboard
			}
			if m.Mouse {
				k |= evdev.Mouse
			}
			grab.Set(k)
			clipOn.Store(m.Clipboard)
		case "clip":
			if clipOn.Load() && cb != nil {
				cb.Set(m.Text)
			}
		}
	}
}
