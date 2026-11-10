//go:build windows

package desktop

import (
	"encoding/json"
	"errors"
	"image"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const maxInflight = 2 // кадров «в пути» без подтверждения — ограничивает задержку буферизации

var errReconfig = errors.New("смена настроек")

// clientMsg — сообщения браузера (текстовые JSON-кадры).
type clientMsg struct {
	T     string  `json:"t"`
	ID    uint32  `json:"id"`
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
}

type config struct {
	quality, fps, mon, scale int
}

type geometry struct {
	b    image.Rectangle // монитор в координатах рабочего стола
	w, h int             // размер передаваемого изображения
}

type session struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	done chan struct{}
	once sync.Once

	cfgMu    sync.Mutex
	cfg      config
	reconfig chan struct{}

	ackSig    chan struct{}
	inflight  atomic.Int32
	forceFull atomic.Bool

	input chan clientMsg

	geomMu sync.Mutex
	geom   geometry
}

// Serve обслуживает один сеанс удалённого рабочего стола до закрытия соединения.
func Serve(ws *websocket.Conn) error {
	setDPIAware()
	s := &session{
		ws:       ws,
		done:     make(chan struct{}),
		cfg:      config{quality: 70, fps: 60, mon: -1, scale: 100},
		reconfig: make(chan struct{}, 1),
		ackSig:   make(chan struct{}, 1),
		input:    make(chan clientMsg, 1024),
	}
	stopIndicator := showIndicator("RandDesk · этим ПК управляют удалённо")
	defer stopIndicator()
	go s.readLoop()
	go s.inputLoop()
	err := s.captureLoop()
	s.stop()
	return err
}

func (s *session) stop() {
	s.once.Do(func() {
		close(s.done)
		s.ws.Close()
	})
}

func (s *session) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *session) sendJSON(v any) error {
	b, _ := json.Marshal(v)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return s.ws.WriteMessage(websocket.TextMessage, b)
}

func (s *session) sendBinary(b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return s.ws.WriteMessage(websocket.BinaryMessage, b)
}

func (s *session) getCfg() config {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.cfg
}

func (s *session) readLoop() {
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
			old := s.cfg
			if m.Q >= 10 && m.Q <= 95 {
				s.cfg.quality = m.Q
			}
			if m.FPS >= 1 && m.FPS <= 120 {
				s.cfg.fps = m.FPS
			}
			if m.Scale == 50 || m.Scale == 100 {
				s.cfg.scale = m.Scale
			}
			if m.Mon != nil {
				s.cfg.mon = *m.Mon
			}
			changed := old.mon != s.cfg.mon || old.scale != s.cfg.scale
			s.cfgMu.Unlock()
			if changed {
				select {
				case s.reconfig <- struct{}{}:
				default:
				}
			}
		default:
			select {
			case s.input <- m:
			case <-s.done:
				return
			}
		}
	}
}

// ---------- захват и отправка ----------

func (s *session) captureLoop() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var desk deskSwitcher
	fails := 0
	for !s.isDone() {
		if _, err := desk.sync(); err != nil {
			log.Printf("рабочий стол: %v", err)
		}
		mons := monitors()
		if len(mons) == 0 {
			fails++
			if fails > 30 {
				return errors.New("нет активных мониторов")
			}
			time.Sleep(time.Second)
			continue
		}
		cfg := s.getCfg()
		mi := cfg.mon
		if mi < 0 || mi >= len(mons) {
			mi = 0
			for i, m := range mons {
				if m.Primary {
					mi = i
				}
			}
		}
		m := mons[mi]
		c, err := openCapturer(image.Rect(m.X, m.Y, m.X+m.W, m.Y+m.H))
		if err != nil {
			fails++
			s.sendJSON(map[string]any{"type": "error", "message": "захват экрана: " + err.Error()})
			if fails > 30 {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		s.sendJSON(map[string]any{"type": "info", "monitors": mons, "monitor": mi, "capture": c.kind(), "desktop": desk.name})
		err = s.stream(c, cfg, &desk)
		c.close()
		switch {
		case err == nil, errors.Is(err, errReconfig):
			fails = 0
		default:
			fails++
			log.Printf("захват прерван (%s): %v", c.kind(), err)
			if fails > 50 {
				return err
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return nil
}

func (s *session) stream(c capturer, cfg config, desk *deskSwitcher) error {
	b := c.bounds()
	full := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	cur := full
	if cfg.scale == 50 {
		cur = image.NewRGBA(image.Rect(0, 0, b.Dx()/2, b.Dy()/2))
	}
	prev := image.NewRGBA(cur.Rect)
	s.geomMu.Lock()
	s.geom = geometry{b: b, w: cur.Rect.Dx(), h: cur.Rect.Dy()}
	s.geomMu.Unlock()
	s.forceFull.Store(true)

	var (
		id        uint32
		pending   bool
		lastSend  time.Time
		lastCheck = time.Now()
	)
	for {
		select {
		case <-s.done:
			return nil
		case <-s.reconfig:
			return errReconfig
		default:
		}
		if time.Since(lastCheck) > time.Second {
			lastCheck = time.Now()
			if changed, _ := desk.sync(); changed { // UAC / экран блокировки
				return errors.New("смена рабочего стола: " + desk.name)
			}
		}
		force := s.forceFull.Load()
		timeout := uint(100)
		if pending || force {
			timeout = 0 // уже есть что отправить — только забираем свежий кадр
		}
		got, err := c.next(full, timeout)
		if err != nil {
			return err
		}
		if got {
			pending = true
			if cur != full {
				downscale2x(cur, full)
			}
		}
		if !pending && !force {
			continue
		}
		if s.inflight.Load() >= maxInflight {
			select {
			case <-s.ackSig:
			case <-s.done:
				return nil
			case <-time.After(2 * time.Second):
				s.inflight.Store(0) // подтверждение потерялось
			}
			continue
		}
		cfg = s.getCfg()
		if wait := time.Second/time.Duration(cfg.fps) - time.Since(lastSend); wait > 0 {
			time.Sleep(min(wait, 4*time.Millisecond))
			continue
		}
		var rs []rect
		if force {
			s.forceFull.Store(false)
			rs = fullRects(cur)
		} else {
			rs = diffRects(cur, prev)
		}
		pending = false
		if len(rs) == 0 {
			continue
		}
		id++
		msg := encodeFrame(cur, rs, cfg.quality, id)
		copyRects(prev, cur, rs)
		s.inflight.Add(1)
		if err := s.sendBinary(msg); err != nil {
			s.stop()
			return nil
		}
		lastSend = time.Now()
	}
}

// ---------- ввод ----------

func (s *session) toScreen(x, y int) (int, int, bool) {
	s.geomMu.Lock()
	g := s.geom
	s.geomMu.Unlock()
	if g.w == 0 || g.h == 0 {
		return 0, 0, false
	}
	x = max(0, min(x, g.w-1))
	y = max(0, min(y, g.h-1))
	return g.b.Min.X + x*g.b.Dx()/g.w, g.b.Min.Y + y*g.b.Dy()/g.h, true
}

func (s *session) inputLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var desk deskSwitcher
	desk.sync()
	lastSync := time.Now()
	keys := map[string]int{}
	buttons := map[int]bool{}
	releaseAll := func() {
		for c, k := range keys {
			keyEvent(c, k, false)
		}
		for b := range buttons {
			f, d := buttonFlags(b, false)
			sendMouse(mouseInput{Type: inputMouse, Flags: f, MouseData: d})
		}
		clear(keys)
		clear(buttons)
	}
	defer releaseAll()
	for {
		var m clientMsg
		select {
		case <-s.done:
			return
		case m = <-s.input:
		}
		if time.Since(lastSync) > 250*time.Millisecond {
			desk.sync()
			lastSync = time.Now()
		}
		switch m.T {
		case "mm":
			if x, y, ok := s.toScreen(m.X, m.Y); ok {
				sendMouse(absMove(x, y))
			}
		case "md", "mu":
			down := m.T == "md"
			f, d := buttonFlags(m.B, down)
			if f == 0 {
				continue
			}
			btn := mouseInput{Type: inputMouse, Flags: f, MouseData: d}
			if x, y, ok := s.toScreen(m.X, m.Y); ok {
				sendMouse(absMove(x, y), btn)
			} else {
				sendMouse(btn)
			}
			if down {
				buttons[m.B] = true
			} else {
				delete(buttons, m.B)
			}
		case "wh":
			if m.DY != 0 {
				sendMouse(mouseInput{Type: inputMouse, Flags: mouseWheel, MouseData: uint32(int32(max(-2400, min(2400, m.DY))))})
			}
			if m.DX != 0 {
				sendMouse(mouseInput{Type: inputMouse, Flags: mouseHWheel, MouseData: uint32(int32(max(-2400, min(2400, m.DX))))})
			}
		case "kd":
			keyEvent(m.C, m.K, true)
			keys[m.C] = m.K
		case "ku":
			keyEvent(m.C, m.K, false)
			delete(keys, m.C)
		case "rel":
			releaseAll()
		case "text":
			if len(m.S) <= 20000 {
				typeText(m.S)
			}
		}
	}
}
