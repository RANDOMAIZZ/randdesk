//go:build linux

// Рабочий стол телефона: картинку снимает и сжимает в H.264 само приложение Android
// (MediaProjection + MediaCodec), а этот сеанс пересылает кадры браузеру и команды —
// приложению. Мост — локальное TCP-соединение 127.0.0.1 с токеном (RANDSEC_SCREEN).
package desktop

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type asession struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	br   net.Conn
	bmu  sync.Mutex
	done chan struct{}
	once sync.Once

	inflight atomic.Int32
	needKey  atomic.Bool
	w, h     atomic.Int32
	cfgMu    sync.Mutex
	quality  int
	fps      int
	scale    int
	started  bool
}

func serveAndroid(ws *websocket.Conn) error {
	send := func(v any) { b, _ := json.Marshal(v); ws.WriteMessage(websocket.TextMessage, b) }
	addr := os.Getenv("RANDSEC_SCREEN")
	i := strings.IndexByte(addr, '/')
	if i < 0 {
		send(map[string]string{"type": "error", "message": "показ экрана телефона недоступен: обновите приложение RANDSEC"})
		return errors.New("нет моста показа экрана")
	}
	br, err := net.DialTimeout("tcp", addr[:i], 3*time.Second)
	if err != nil {
		send(map[string]string{"type": "error", "message": "приложение RANDSEC на телефоне не отвечает: " + err.Error()})
		return err
	}
	defer br.Close()
	if _, err := io.WriteString(br, addr[i+1:]+"\n"); err != nil {
		return err
	}
	s := &asession{ws: ws, br: br, done: make(chan struct{}), quality: 70, fps: 60, scale: 100}
	s.sendJSON(map[string]any{"type": "info", "capture": "Android", "desktop": "",
		"monitors": []monitor{{Index: 0, Name: "Экран телефона", Primary: true}}, "monitor": 0})
	s.sendJSON(map[string]any{"type": "error", "message": "Подтвердите показ экрана на телефоне: «Начать» в окне или в уведомлении RANDSEC"})
	go s.readBrowser()
	err = s.readBridge()
	s.stop()
	return err
}

func (s *asession) stop() {
	s.once.Do(func() {
		close(s.done)
		s.cmd(map[string]any{"cmd": "stop"})
		s.ws.Close()
		s.br.Close()
	})
}

func (s *asession) sendJSON(v any) {
	b, _ := json.Marshal(v)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	s.ws.WriteMessage(websocket.TextMessage, b)
}

func (s *asession) sendBinary(b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return s.ws.WriteMessage(websocket.BinaryMessage, b)
}

func (s *asession) cmd(v any) {
	b, _ := json.Marshal(v)
	s.bmu.Lock()
	defer s.bmu.Unlock()
	s.br.SetWriteDeadline(time.Now().Add(5 * time.Second))
	s.br.Write(append(b, '\n'))
}

// start просит приложение начать (или перезапустить с новыми настройками) показ экрана.
func (s *asession) start() {
	s.cfgMu.Lock()
	q, fps, scale := s.quality, s.fps, s.scale
	s.cfgMu.Unlock()
	maxSide := 1920
	if scale == 50 {
		maxSide = 1080
	}
	// битрейт по качеству: 70 → ~8 Мбит/с при 60 к/с
	kbps := (2000 + (q-30)*150) * fps / 60
	s.cmd(map[string]any{"cmd": "start", "maxSide": maxSide, "fps": fps, "kbps": max(800, kbps)})
}

func (s *asession) readBrowser() {
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
		case "ping":
			s.sendJSON(map[string]any{"type": "pong", "ts": m.TS})
		case "full", "key":
			s.cmd(map[string]any{"cmd": "key"})
		case "cfg":
			s.cfgMu.Lock()
			changed := !s.started
			if m.Q >= 10 && m.Q <= 95 && m.Q != s.quality {
				s.quality, changed = m.Q, true
			}
			if m.FPS >= 1 && m.FPS <= 60 && m.FPS != s.fps {
				s.fps, changed = m.FPS, true
			}
			if (m.Scale == 50 || m.Scale == 100) && m.Scale != s.scale {
				s.scale, changed = m.Scale, true
			}
			s.started = true
			s.cfgMu.Unlock()
			if changed {
				s.start()
			}
		case "mm", "md", "mu", "wh", "kd", "text":
			if m.T == "text" && len(m.S) > 20000 {
				continue
			}
			s.cmd(map[string]any{"cmd": "in", "t": m.T, "x": m.X, "y": m.Y, "b": m.B, "dx": m.DX, "dy": m.DY, "c": m.C, "s": m.S, "key": m.Key})
		}
	}
}

// readBridge читает сообщения приложения: состояние и кадры H.264.
func (s *asession) readBridge() error {
	r := bufio.NewReaderSize(s.br, 1<<16)
	var hdr [5]byte
	var id uint32
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			select {
			case <-s.done:
				return nil
			default:
				return err
			}
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > 16<<20 {
			return errors.New("слишком большой кадр от приложения")
		}
		buf := make([]byte, 9+n) // место под заголовок кадра для браузера; buf[9] — флаг ключевого кадра
		if _, err := io.ReadFull(r, buf[9:]); err != nil {
			return err
		}
		switch hdr[0] {
		case 1:
			var st struct {
				State   string `json:"state"`
				Msg     string `json:"msg"`
				W, H    int32
				Encoder string `json:"encoder"`
				Control bool   `json:"control"`
			}
			json.Unmarshal(buf[9:], &st)
			switch st.State {
			case "started":
				s.w.Store(st.W)
				s.h.Store(st.H)
				s.needKey.Store(true)
				s.sendJSON(map[string]any{"type": "codec", "codec": "h264", "encoder": st.Encoder})
				s.sendJSON(map[string]any{"type": "android", "control": st.Control})
				s.cmd(map[string]any{"cmd": "key"})
				log.Printf("рабочий стол телефона: %d×%d, %s", st.W, st.H, st.Encoder)
			case "waiting":
				s.sendJSON(map[string]any{"type": "error", "message": "Подтвердите показ экрана на телефоне: «Начать» в окне или в уведомлении RANDSEC"})
			case "denied", "stopped", "error":
				msg := st.Msg
				if msg == "" {
					msg = "показ экрана телефона остановлен"
				}
				s.sendJSON(map[string]any{"type": "error", "message": msg})
				if st.State != "error" {
					return nil
				}
			}
		case 2:
			key := buf[9]&1 == 1
			if s.w.Load() == 0 {
				continue // размер ещё неизвестен
			}
			if s.needKey.Load() && !key {
				continue // ждём ключевой кадр
			}
			if s.inflight.Load() >= 8 { // браузер не успевает — пропускаем до следующего ключевого
				s.needKey.Store(true)
				s.cmd(map[string]any{"cmd": "key"})
				continue
			}
			s.needKey.Store(false)
			id++
			buf[0] = 2
			binary.LittleEndian.PutUint32(buf[1:], id)
			binary.LittleEndian.PutUint16(buf[5:], uint16(s.w.Load()))
			binary.LittleEndian.PutUint16(buf[7:], uint16(s.h.Load()))
			buf[9] &= 1
			s.inflight.Add(1)
			if err := s.sendBinary(buf); err != nil {
				return nil
			}
		}
	}
}
