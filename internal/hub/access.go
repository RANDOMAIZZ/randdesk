package hub

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"randdesk/internal/proto"
)

// ---------- кто делает запрос: администратор или ПК с разрешением ----------

type ctxDevKey struct{}

// deviceOf — ID ПК, от имени которого идёт запрос (пусто — администратор).
func deviceOf(r *http.Request) string {
	d, _ := r.Context().Value(ctxDevKey{}).(string)
	return d
}

// authDev пропускает администратора или обычный ПК (через его локальный интерфейс).
// ПК допускается только к устройствам, которыми администратор разрешил ему управлять.
func (s *Server) authDev(h http.HandlerFunc, needTarget bool) http.HandlerFunc {
	return s.csrf(func(w http.ResponseWriter, r *http.Request) {
		if user := s.sessionUser(r); user != "" {
			h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
			return
		}
		dev, ok := s.authAgent(r)
		if !ok || r.Header.Get("X-RandDesk-Local") != "1" {
			jsonErr(w, http.StatusUnauthorized, "требуется вход")
			return
		}
		if needTarget {
			target := r.PathValue("id")
			if target == dev.ID || !s.store.CanControl(dev.ID, target) {
				jsonErr(w, http.StatusForbidden, "администратор не разрешил этому ПК управлять этим устройством")
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, "ПК «"+dev.Name+"»")
		ctx = context.WithValue(ctx, ctxDevKey{}, dev.ID)
		h(w, r.WithContext(ctx))
	})
}

// ---------- права доступа ----------

func (s *Server) hAccess(w http.ResponseWriter, r *http.Request) {
	devs := s.store.Devices()
	grants := map[string][]string{}
	for _, d := range devs {
		grants[d.ID] = append([]string{}, d.Controls...)
	}
	writeJSON(w, 200, map[string]any{"grants": grants, "clip_shared": s.store.Settings().ClipShared})
}

func (s *Server) hAccessSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Grants     map[string][]string `json:"grants"`
		ClipShared *bool               `json:"clip_shared"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	valid := map[string]bool{}
	for _, d := range s.store.Devices() {
		valid[d.ID] = true
	}
	for from, list := range req.Grants {
		if !valid[from] {
			continue
		}
		var clean []string
		for _, to := range list {
			if valid[to] && to != from {
				clean = append(clean, to)
			}
		}
		s.store.UpdateDevice(from, func(d *Device) { d.Controls = clean })
	}
	if req.ClipShared != nil {
		st := s.store.Settings()
		st.ClipShared = *req.ClipShared
		s.store.SetSettings(st)
		s.pushSessionState("")
	}
	b, _ := json.Marshal(req)
	s.audit.Log(userOf(r), clientIP(r), "", "access_changed", string(b))
	writeJSON(w, 200, proto.OK{OK: true})
}

// ---------- устройства ввода ----------

func (s *Server) hInput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req InputLock
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	d, ok := s.store.Device(id)
	if !ok {
		jsonErr(w, 404, "устройство не найдено")
		return
	}
	if s.isSelf(d) && (req.Keyboard || req.Mouse) {
		jsonErr(w, 400, "нельзя отключить клавиатуру и мышь на главном ПК — вы потеряете управление")
		return
	}
	prev := d.Lock
	if err := s.store.UpdateDevice(id, func(d *Device) { d.Lock = req }); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	b, _ := json.Marshal(req)
	s.audit.Log(userOf(r), clientIP(r), d.Name, "input_lock", string(b))
	s.pushSessionState(id)
	if prev.Gamepad != req.Gamepad {
		if ac := s.agent(id); ac != nil {
			if err := s.setGamepad(ac, req.Gamepad); err != nil {
				jsonErr(w, http.StatusBadGateway, "геймпад: "+err.Error())
				return
			}
		}
	}
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) setGamepad(ac *agentConn, disable bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	b, _ := json.Marshal(proto.GamepadReq{Disable: disable})
	_, err := ac.call(ctx, "gamepad", b)
	return err
}

func (s *Server) hClipboard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct{ Enabled bool }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	if err := s.store.UpdateDevice(id, func(d *Device) { d.NoClip = !req.Enabled }); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	s.pushSessionState(id)
	writeJSON(w, 200, proto.OK{OK: true})
}

// isSelf — устройство на самом главном ПК.
func (s *Server) isSelf(d Device) bool {
	return s.node != nil && s.node.SelfMachine != "" && d.MachineID == s.node.SelfMachine
}

// ---------- помощник сеанса на ПК (буфер обмена, блокировка ввода) ----------

type sessionConn struct {
	ws  *websocket.Conn
	wmu sync.Mutex
}

func (c *sessionConn) send(v any) {
	b, _ := json.Marshal(v)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	c.ws.WriteMessage(websocket.TextMessage, b)
}

func (s *Server) sessionState(d Device) proto.SessionState {
	return proto.SessionState{
		Type:      "state",
		Keyboard:  d.Lock.Keyboard,
		Mouse:     d.Lock.Mouse,
		Clipboard: s.store.Settings().ClipShared && !d.NoClip,
	}
}

// pushSessionState рассылает актуальное состояние помощникам (id пусто — всем).
func (s *Server) pushSessionState(id string) {
	s.mu.Lock()
	conns := map[string]*sessionConn{}
	for k, c := range s.sessions2 {
		if id == "" || k == id {
			conns[k] = c
		}
	}
	s.mu.Unlock()
	for k, c := range conns {
		if d, ok := s.store.Device(k); ok {
			go c.send(s.sessionState(d))
		}
	}
}

func (s *Server) hAgentSession(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authAgent(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized, "устройство не авторизовано")
		return
	}
	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &sessionConn{ws: ws}
	s.mu.Lock()
	if s.sessions2 == nil {
		s.sessions2 = map[string]*sessionConn{}
	}
	if old := s.sessions2[dev.ID]; old != nil {
		old.ws.Close()
	}
	s.sessions2[dev.ID] = c
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.sessions2[dev.ID] == c {
			delete(s.sessions2, dev.ID)
		}
		s.mu.Unlock()
		ws.Close()
	}()
	c.send(s.sessionState(dev))
	ws.SetReadLimit(2 << 20)
	for {
		ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var m proto.SessionMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.Type == "clip" {
			s.shareClipboard(dev.ID, m)
		}
	}
}

// shareClipboard пересылает скопированное на одном ПК во все остальные, участвующие в общем буфере.
func (s *Server) shareClipboard(from string, m proto.SessionMsg) {
	if !s.store.Settings().ClipShared {
		return
	}
	src, ok := s.store.Device(from)
	if !ok || src.NoClip {
		return
	}
	s.mu.Lock()
	targets := make([]*sessionConn, 0, len(s.sessions2))
	ids := make([]string, 0, len(s.sessions2))
	for k, c := range s.sessions2 {
		if k != from {
			targets = append(targets, c)
			ids = append(ids, k)
		}
	}
	s.mu.Unlock()
	n := 0
	for i, c := range targets {
		if d, ok := s.store.Device(ids[i]); ok && !d.NoClip {
			go c.send(m)
			n++
		}
	}
	if n > 0 {
		log.Printf("буфер обмена: %s → %d ПК (%d символов)", src.Name, n, len([]rune(m.Text)))
	}
}
