//go:build linux

// Удалённый рабочий стол на Linux: захват экрана X11 и ввод через XTest.
// Под Wayland работает только для программ Xwayland (ограничение Wayland).
package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"

	"randsec/internal/android"
	"randsec/internal/x11"
)

const maxInflight = 2

type clientMsg struct {
	T     string  `json:"t"`
	X     int     `json:"x"`
	Y     int     `json:"y"`
	B     int     `json:"b"`
	DX    int     `json:"dx"`
	DY    int     `json:"dy"`
	C     string  `json:"c"`
	K     int     `json:"k"`
	S     string  `json:"s"`
	TS    float64 `json:"ts"`
	Q     int     `json:"q"`
	FPS   int     `json:"fps"`
	Mon   *int    `json:"mon"`
	Scale int     `json:"scale"`
	R     bool    `json:"r"`   // игровой режим: кнопки без перемещения курсора в точку
	Key   string  `json:"key"` // символ клавиши в раскладке браузера (для телефона)
}

type monitor struct {
	Index   int    `json:"index"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Primary bool   `json:"primary"`
	Name    string `json:"name"`
}

func monitors() []monitor {
	n := screenshot.NumActiveDisplays()
	out := make([]monitor, 0, n)
	for i := 0; i < n; i++ {
		b := screenshot.GetDisplayBounds(i)
		out = append(out, monitor{Index: i, X: b.Min.X, Y: b.Min.Y, W: b.Dx(), H: b.Dy(), Primary: i == 0,
			Name: fmt.Sprintf("Монитор %d (%d×%d)", i+1, b.Dx(), b.Dy())})
	}
	return out
}

type lsession struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	done chan struct{}
	once sync.Once
	in   *x11.Input

	cfgMu    sync.Mutex
	quality  int
	fps      int
	mon      int
	scale    int
	reconfig chan struct{}

	ackSig    chan struct{}
	inflight  atomic.Int32
	forceFull atomic.Bool

	geomMu sync.Mutex
	bounds image.Rectangle
	w, h   int
}

// ShowIndicator — на Linux плашка не показывается (нет общего способа вывести поверх всех окон).
func ShowIndicator(string) func() { return func() {} }

// ShowNotice — см. ShowIndicator.
func ShowNotice(string) func() { return func() {} }

// SetIndicatorPos — на Linux плашки нет, настройка не используется.
func SetIndicatorPos(string) {}

// Serve обслуживает один сеанс удалённого рабочего стола до закрытия соединения.
func Serve(ws *websocket.Conn) error {
	if android.On() {
		return serveAndroid(ws)
	}
	in, err := x11.OpenInput()
	if err != nil {
		msg := "нет доступа к рабочему столу X11: " + err.Error()
		b, _ := json.Marshal(map[string]string{"type": "error", "message": msg})
		ws.WriteMessage(websocket.TextMessage, b)
		return errors.New(msg)
	}
	defer in.Close()
	s := &lsession{ws: ws, done: make(chan struct{}), in: in, quality: 70, fps: 30, mon: -1, scale: 100,
		reconfig: make(chan struct{}, 1), ackSig: make(chan struct{}, 1)}
	defer in.ReleaseAll()
	go s.readLoop()
	err = s.captureLoop()
	s.stop()
	return err
}

func (s *lsession) stop() { s.once.Do(func() { close(s.done); s.ws.Close() }) }

func (s *lsession) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *lsession) sendJSON(v any) {
	b, _ := json.Marshal(v)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	s.ws.WriteMessage(websocket.TextMessage, b)
}

func (s *lsession) sendBinary(b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return s.ws.WriteMessage(websocket.BinaryMessage, b)
}

func (s *lsession) toScreen(x, y int) (int, int, bool) {
	s.geomMu.Lock()
	b, w, h := s.bounds, s.w, s.h
	s.geomMu.Unlock()
	if w == 0 || h == 0 {
		return 0, 0, false
	}
	x = max(0, min(x, w-1))
	y = max(0, min(y, h-1))
	return b.Min.X + x*b.Dx()/w, b.Min.Y + y*b.Dy()/h, true
}

func (s *lsession) readLoop() {
	defer s.stop()
	s.ws.SetReadLimit(1 << 20)
	for {
		_, data, err := s.ws.ReadMessage()
		if err != nil {
			return
		}
		var m clientMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "ack":
			if s.inflight.Add(-1) < 0 {
				s.inflight.Store(0)
			}
			select {
			case s.ackSig <- struct{}{}:
			default:
			}
		case "ping":
			s.sendJSON(map[string]any{"type": "pong", "ts": m.TS})
		case "full":
			s.forceFull.Store(true)
		case "cfg":
			s.cfgMu.Lock()
			oldMon, oldScale := s.mon, s.scale
			if m.Q >= 10 && m.Q <= 95 {
				s.quality = m.Q
			}
			if m.FPS >= 1 && m.FPS <= 60 {
				s.fps = m.FPS
			}
			if m.Scale == 50 || m.Scale == 100 {
				s.scale = m.Scale
			}
			if m.Mon != nil {
				s.mon = *m.Mon
			}
			changed := oldMon != s.mon || oldScale != s.scale
			s.cfgMu.Unlock()
			if changed {
				select {
				case s.reconfig <- struct{}{}:
				default:
				}
			}
		case "mm":
			if x, y, ok := s.toScreen(m.X, m.Y); ok {
				s.in.Move(x, y)
			}
		case "mr":
			s.in.MoveRel(max(-4000, min(4000, m.DX)), max(-4000, min(4000, m.DY)))
		case "md", "mu":
			if !m.R {
				if x, y, ok := s.toScreen(m.X, m.Y); ok {
					s.in.Move(x, y)
				}
			}
			s.in.Button(m.B, m.T == "md")
		case "wh":
			s.in.Wheel(m.DX, m.DY)
		case "kd", "ku":
			s.in.Key(m.C, m.T == "kd")
		case "rel":
			s.in.ReleaseAll()
		case "text":
			if len(m.S) <= 20000 {
				s.in.Type(m.S)
			}
		}
	}
}

func (s *lsession) captureLoop() error {
	fails := 0
	for !s.isDone() {
		mons := monitors()
		if len(mons) == 0 {
			if fails++; fails > 30 {
				return errors.New("нет активных мониторов")
			}
			time.Sleep(time.Second)
			continue
		}
		s.cfgMu.Lock()
		mi := s.mon
		s.cfgMu.Unlock()
		if mi < 0 || mi >= len(mons) {
			mi = 0
		}
		m := mons[mi]
		s.sendJSON(map[string]any{"type": "info", "monitors": mons, "monitor": mi, "capture": "X11", "desktop": "X11"})
		err := s.stream(image.Rect(m.X, m.Y, m.X+m.W, m.Y+m.H))
		if err == nil {
			fails = 0
			continue
		}
		if fails++; fails > 30 {
			return err
		}
		log.Printf("захват прерван: %v", err)
		s.sendJSON(map[string]any{"type": "error", "message": "захват экрана: " + err.Error()})
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (s *lsession) stream(b image.Rectangle) error {
	s.cfgMu.Lock()
	scale := s.scale
	s.cfgMu.Unlock()
	w, h := b.Dx(), b.Dy()
	if scale == 50 {
		w, h = w/2, h/2
	}
	cur := image.NewRGBA(image.Rect(0, 0, w, h))
	prev := image.NewRGBA(cur.Rect)
	s.geomMu.Lock()
	s.bounds, s.w, s.h = b, w, h
	s.geomMu.Unlock()
	s.forceFull.Store(true)
	var id uint32
	var lastSend time.Time
	for {
		select {
		case <-s.done:
			return nil
		case <-s.reconfig:
			return nil
		default:
		}
		if s.inflight.Load() >= maxInflight {
			select {
			case <-s.ackSig:
			case <-s.done:
				return nil
			case <-time.After(2 * time.Second):
				s.inflight.Store(0)
			}
			continue
		}
		s.cfgMu.Lock()
		fps, quality := s.fps, s.quality
		s.cfgMu.Unlock()
		if wait := time.Second/time.Duration(fps) - time.Since(lastSend); wait > 0 {
			time.Sleep(wait)
		}
		img, err := screenshot.CaptureRect(b)
		if err != nil {
			return err
		}
		if scale == 50 {
			downscale2x(cur, img)
		} else {
			cur = img // CaptureRect каждый раз возвращает новое изображение
		}
		var rs []rect
		if s.forceFull.Swap(false) {
			rs = fullRects(cur)
		} else {
			rs = diffRects(cur, prev)
		}
		lastSend = time.Now()
		if len(rs) == 0 {
			continue
		}
		id++
		msg := encodeFrame(cur, rs, quality, id)
		copyRects(prev, cur, rs)
		s.inflight.Add(1)
		if err := s.sendBinary(msg); err != nil {
			s.stop()
			return nil
		}
	}
}
