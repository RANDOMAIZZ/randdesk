//go:build windows

package desktop

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const maxInflight = 3 // кадров «в пути» без подтверждения — ограничивает задержку буферизации

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
	R     bool    `json:"r"` // игровой режим: кнопки без перемещения курсора в точку
	Codec string  `json:"codec"`
}

type config struct {
	quality, fps, mon, scale int
	codec                    string // "h264" — браузер умеет декодировать видео, иначе JPEG
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
	h264Fails int // сбои аппаратного кодировщика в этом сеансе; после нескольких — JPEG

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
	stopIndicator := showIndicator("Удалённое управление · RANDSEC", false)
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
		case "full", "key":
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
			if m.Codec == "h264" || m.Codec == "jpeg" {
				s.cfg.codec = m.Codec
			}
			changed := old.mon != s.cfg.mon || old.scale != s.cfg.scale || old.codec != s.cfg.codec ||
				(s.cfg.codec == "h264" && (old.quality != s.cfg.quality || old.fps != s.cfg.fps)) // видео: новый поток
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
		if cfg.codec == "h264" && s.h264Fails < 3 {
			err = s.streamH264(c, cfg, &desk)
			if err != nil && !errors.Is(err, errReconfig) {
				s.h264Fails++
				if errors.Is(err, errNoH264) {
					s.h264Fails = 3
				}
				log.Printf("видео H.264: %v", err)
				if s.h264Fails >= 3 {
					s.sendJSON(map[string]any{"type": "codec", "codec": "jpeg", "reason": err.Error()})
				}
				c.close()
				continue
			}
		} else {
			if cfg.codec == "h264" {
				s.sendJSON(map[string]any{"type": "codec", "codec": "jpeg"})
			}
			err = s.stream(c, cfg, &desk)
		}
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
		id         uint32
		pending    bool
		lastSend   time.Time
		lastCheck  = time.Now()
		lastChange time.Time
		encDone    chan struct{} // кодирование предыдущего кадра (идёт параллельно с захватом)
		sendFailed atomic.Bool
	)
	soft := newDirtyMap(cur.Rect.Dx(), cur.Rect.Dy()) // области, отправленные с пониженным качеством
	waitEnc := func() bool {
		if encDone != nil {
			<-encDone
			encDone = nil
		}
		return !sendFailed.Load()
	}
	// encodeAsync кодирует области из prev и отправляет кадр, не задерживая захват следующего.
	// prev не меняется, пока кодирование не закончится (см. waitEnc перед copyRects).
	encodeAsync := func(rs []rect, q int, id uint32) {
		done := make(chan struct{})
		encDone = done
		s.inflight.Add(1)
		go func() {
			defer close(done)
			if err := s.sendBinary(encodeFrame(prev, rs, q, id)); err != nil {
				sendFailed.Store(true)
			}
		}()
	}
	defer waitEnc()
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
			// картинка замерла — досылаем размытые при движении области в высоком качестве
			if soft.any() && time.Since(lastChange) > 300*time.Millisecond && s.inflight.Load() < maxInflight {
				if !waitEnc() {
					s.stop()
					return nil
				}
				id++
				encodeAsync(soft.take(), refineQuality, id)
				lastSend = time.Now()
			}
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
		if !waitEnc() {
			s.stop()
			return nil
		}
		copyRects(prev, cur, rs)
		if cfg.quality < refineQuality {
			soft.mark(rs)
		} else {
			soft.clear(rs)
		}
		id++
		encodeAsync(rs, cfg.quality, id)
		lastSend, lastChange = time.Now(), time.Now()
	}
}

var errNoH264 = errors.New("аппаратный H.264 недоступен")

// streamH264 — поток видео H.264 с аппаратным сжатием: кадр целиком на видеокарте, поэтому
// процессор занят только копированием и переводом цвета — 60 кадров/с даже на 1440p.
func (s *session) streamH264(c capturer, cfg config, desk *deskSwitcher) error {
	b := c.bounds()
	w, h := b.Dx(), b.Dy()
	if cfg.scale == 50 {
		w, h = w/2, h/2
	}
	w, h = w&^1, h&^1
	// кадр переводится в NV12 сразу при захвате (из памяти DXGI, без промежуточной копии)
	nv12 := make([]byte, w*h*3/2)
	raw, _ := c.(rawCapturer)
	var full *image.RGBA
	if raw == nil {
		full = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	}
	grab := func(timeout uint) (bool, error) {
		if raw != nil {
			return raw.nextRaw(timeout, func(pix []byte, stride int, bgr bool) { toNV12(nv12, pix, stride, b.Dx(), bgr, w, h) })
		}
		got, err := c.next(full, timeout)
		if got {
			rgbaToNV12(nv12, full, w, h)
		}
		return got, err
	}
	enc, err := newH264(w, h, cfg.fps, h264Bitrate(cfg.quality, w, h, cfg.fps))
	if err != nil {
		return fmt.Errorf("%w: %v", errNoH264, err)
	}
	defer enc.close()
	s.geomMu.Lock()
	s.geom = geometry{b: b, w: w, h: h}
	s.geomMu.Unlock()
	s.sendJSON(map[string]any{"type": "codec", "codec": "h264", "encoder": enc.name})
	s.forceFull.Store(false)

	sendErr := make(chan error, 1)
	go func() {
		var id uint32
		for f := range enc.out {
			id++
			msg := make([]byte, 10, 10+len(f.data))
			msg[0] = 2
			binary.LittleEndian.PutUint32(msg[1:], id)
			binary.LittleEndian.PutUint16(msg[5:], uint16(w))
			binary.LittleEndian.PutUint16(msg[7:], uint16(h))
			if f.key {
				msg[9] = 1
			}
			s.inflight.Add(1)
			if err := s.sendBinary(append(msg, f.data...)); err != nil {
				select {
				case sendErr <- err:
				default:
				}
			}
		}
	}()

	var (
		pending    = true
		key        = true
		refine     = 0
		lastSend   time.Time
		lastChange = time.Now()
		lastCheck  = time.Now()
	)
	for {
		select {
		case <-s.done:
			return nil
		case <-s.reconfig:
			return errReconfig
		case <-sendErr:
			s.stop()
			return nil
		default:
		}
		if time.Since(lastCheck) > time.Second {
			lastCheck = time.Now()
			if changed, _ := desk.sync(); changed {
				return errors.New("смена рабочего стола: " + desk.name)
			}
		}
		timeout := uint(50)
		if pending {
			timeout = 0
		}
		got, err := grab(timeout)
		if err != nil {
			return err
		}
		if got {
			pending, refine, lastChange = true, 0, time.Now()
		}
		if s.forceFull.Swap(false) { // браузер просит ключевой кадр (новый декодер, ошибка)
			pending, key = true, true
		}
		if !pending {
			// картинка замерла: пара повторов того же кадра — кодировщик дочищает качество,
			// а последний кадр гарантированно выходит из конвейера
			if refine < 2 && time.Since(lastChange) > 150*time.Millisecond && time.Since(lastSend) > 150*time.Millisecond {
				refine++
				pending = true
			} else {
				continue
			}
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
		if wait := time.Second/time.Duration(cfg.fps) - time.Since(lastSend); wait > 0 {
			time.Sleep(min(wait, 4*time.Millisecond))
			continue
		}
		pending = false
		if err := enc.encode(func(dst []byte) { copy(dst, nv12) }, key); err != nil {
			return err
		}
		key = false
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
		case "mr":
			// игровой режим: относительное движение, как от настоящей мыши — его видят игры,
			// которые прячут курсор и читают «сырой» ввод (шутеры и т. п.)
			sendMouse(mouseInput{Type: inputMouse, Dx: int32(max(-4000, min(4000, m.DX))), Dy: int32(max(-4000, min(4000, m.DY))), Flags: mouseMove})
		case "md", "mu":
			down := m.T == "md"
			f, d := buttonFlags(m.B, down)
			if f == 0 {
				continue
			}
			btn := mouseInput{Type: inputMouse, Flags: f, MouseData: d}
			if m.R {
				sendMouse(btn)
			} else if x, y, ok := s.toScreen(m.X, m.Y); ok {
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
