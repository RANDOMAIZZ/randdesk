package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"randdesk/internal/proto"
	"randdesk/internal/wol"
	"randdesk/web"
)

const (
	sessionCookie = "rd_session"
	sessionTTL    = 7 * 24 * time.Hour
	historyMax    = 1800 // 1 час при интервале 2 с
)

type session struct {
	user    string
	expires time.Time
}

type pendingStream struct {
	devID string
	ch    chan *websocket.Conn
}

type Server struct {
	store         *Store
	audit         *Audit
	port          string
	fingerprint   string // отпечаток самоподписанного сертификата (пусто для своего сертификата)
	net           *internet
	limiter       *limiter // вход в веб-интерфейс
	enrollLimiter *limiter // привязка агентов
	up            websocket.Upgrader
	static        http.Handler
	mu            sync.Mutex
	agents        map[string]*agentConn
	history       map[string][]proto.Stats
	streams       map[string]*pendingStream
	sessions      map[string]session
	tickets       map[string]*loginTicket
	pendingTOTP   map[string]pendingTOTP
	nonces        map[string]time.Time
	node          *NodeHooks
	sessions2     map[string]*sessionConn // помощники сеанса по ID устройства
}

func NewServer(store *Store, audit *Audit, port, fingerprint string) *Server {
	return &Server{
		store:         store,
		audit:         audit,
		port:          port,
		fingerprint:   fingerprint,
		limiter:       newLimiter(),
		enrollLimiter: newLimiter(),
		static:        http.FileServer(http.FS(web.FS())),
		agents:        map[string]*agentConn{},
		history:       map[string][]proto.Stats{},
		streams:       map[string]*pendingStream{},
		sessions:      map[string]session{},
		tickets:       map[string]*loginTicket{},
		pendingTOTP:   map[string]pendingTOTP{},
		up: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
			CheckOrigin: func(r *http.Request) bool {
				o := r.Header.Get("Origin")
				if o == "" { // агенты не присылают Origin
					return true
				}
				u, err := url.Parse(o)
				return err == nil && strings.EqualFold(u.Host, r.Host)
			},
		},
	}
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/state", s.hState)
	m.HandleFunc("POST /api/setup", s.csrf(s.hSetup))
	m.HandleFunc("POST /api/login", s.csrf(s.hLogin))
	m.HandleFunc("POST /api/login/totp", s.csrf(s.hLoginTOTP))
	m.HandleFunc("GET /api/me", s.auth(s.hMe))
	m.HandleFunc("POST /api/2fa/setup", s.auth(s.h2FASetup))
	m.HandleFunc("POST /api/2fa/enable", s.auth(s.h2FAEnable))
	m.HandleFunc("POST /api/2fa/disable", s.auth(s.h2FADisable))
	m.HandleFunc("POST /api/2fa/recovery", s.auth(s.h2FARecovery))
	m.HandleFunc("GET /api/users", s.auth(s.hUsers))
	m.HandleFunc("POST /api/users", s.auth(s.hUserCreate))
	m.HandleFunc("DELETE /api/users/{name}", s.auth(s.hUserDelete))
	m.HandleFunc("GET /api/internet", s.auth(s.hInternet))
	m.HandleFunc("GET /api/zerotier", s.auth(s.hZeroTier))
	m.HandleFunc("POST /api/zerotier", s.auth(s.hZeroTierSet))
	m.HandleFunc("POST /api/internet", s.auth(s.hInternetSet))
	m.HandleFunc("POST /api/logout", s.csrf(s.hLogout))
	m.HandleFunc("POST /api/password", s.auth(s.hPassword))
	m.HandleFunc("GET /api/enroll-key", s.auth(s.hEnrollKey))
	m.HandleFunc("POST /api/enroll-key/rotate", s.auth(s.hRotateKey))
	m.HandleFunc("GET /api/devices", s.authDev(s.hDevices, false))
	m.HandleFunc("GET /api/devices/{id}", s.authDev(s.hDevice, true))
	m.HandleFunc("GET /api/devices/{id}/history", s.authDev(s.hHistory, true))
	m.HandleFunc("GET /api/access", s.auth(s.hAccess))
	m.HandleFunc("POST /api/access", s.auth(s.hAccessSet))
	m.HandleFunc("POST /api/devices/{id}/input", s.auth(s.hInput))
	m.HandleFunc("POST /api/devices/{id}/clipboard", s.auth(s.hClipboard))
	m.HandleFunc("GET /api/agent/session", s.hAgentSession)
	m.HandleFunc("POST /api/devices/{id}/rename", s.auth(s.hRename))
	m.HandleFunc("DELETE /api/devices/{id}", s.auth(s.hDelete))
	m.HandleFunc("POST /api/devices/{id}/rpc/{method}", s.authDev(s.hRPC, true))
	m.HandleFunc("POST /api/devices/{id}/wake", s.authDev(s.hWake, true))
	m.HandleFunc("GET /api/devices/{id}/terminal", s.authDev(s.hTerminal, true))
	m.HandleFunc("GET /api/devices/{id}/desktop", s.authDev(s.hDesktop, true))
	m.HandleFunc("GET /api/audit", s.auth(s.hAudit))
	m.HandleFunc("POST /api/agent/enroll", s.hAgentEnroll)
	m.HandleFunc("POST /api/agent/pair", s.hAgentPair)
	m.HandleFunc("GET /api/agent/ws", s.hAgentWS)
	m.HandleFunc("GET /api/agent/stream", s.hAgentStream)
	m.Handle("/", s.static)
	return secHeaders(m)
}

func secHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		} else {
			h.Set("Cache-Control", "no-cache") // после обновления хаба браузер сразу берёт новый интерфейс
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- helpers ----------

type ctxKey struct{}

func userOf(r *http.Request) string {
	u, _ := r.Context().Value(ctxKey{}).(string)
	return u
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}

// csrf требует кастомный заголовок: браузер не отправит его кросс-доменно без CORS.
func (s *Server) csrf(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "randdesk" {
			jsonErr(w, http.StatusForbidden, "запрос отклонён (CSRF)")
			return
		}
		h(w, r)
	}
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return s.csrf(func(w http.ResponseWriter, r *http.Request) {
		user := s.sessionUser(r)
		if user == "" {
			jsonErr(w, http.StatusUnauthorized, "требуется вход")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

func (s *Server) sessionUser(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[c.Value]
	if !ok {
		return ""
	}
	if time.Now().After(sess.expires) {
		delete(s.sessions, c.Value)
		return ""
	}
	return sess.user
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user string) {
	tok := randToken(32)
	s.mu.Lock()
	s.sessions[tok] = session{user: user, expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(sessionTTL.Seconds()),
	})
}

func (s *Server) agent(id string) *agentConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agents[id]
}

func (s *Server) deviceName(id string) string {
	if d, ok := s.store.Device(id); ok {
		return d.Name
	}
	return id
}

// ---------- аккаунт ----------

func (s *Server) hState(w http.ResponseWriter, r *http.Request) {
	asDevice := ""
	if r.Header.Get("X-RandDesk-Local") == "1" {
		if d, ok := s.authAgent(r); ok {
			asDevice = d.Name
		}
	}
	writeJSON(w, 200, map[string]any{
		"as_device":      asDevice,
		"version":        proto.Version,
		"setup_needed":   !s.store.HasUsers(),
		"can_setup":      isLoopback(r),
		"user":           s.sessionUser(r),
		"hub_urls":       s.hubURLs(),
		"secure":         r.TLS != nil,
		"node":           s.node != nil,
		"server_time_ms": time.Now().UnixMilli(),
	})
}

func (s *Server) hSetup(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		jsonErr(w, http.StatusForbidden, "первичная настройка доступна только на ПК с хабом (http://localhost)")
		return
	}
	var req struct{ User, Password string }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	req.User = strings.TrimSpace(req.User)
	if req.User == "" || len(req.Password) < 8 {
		jsonErr(w, 400, "укажите имя и пароль не короче 8 символов")
		return
	}
	if err := s.store.CreateUser(req.User, req.Password, true); err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}
	if s.node != nil {
		if err := s.node.SetAccount(req.User, req.Password); err != nil {
			log.Printf("настройка ПК: %v", err)
		}
	}
	s.audit.Log(req.User, clientIP(r), "", "setup", "создан администратор")
	s.startSession(w, r, req.User)
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) hLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) hPassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Old, New string }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	user := userOf(r)
	if _, ok := s.store.CheckUser(user, req.Old); !ok {
		jsonErr(w, 400, "текущий пароль неверен")
		return
	}
	if len(req.New) < 8 {
		jsonErr(w, 400, "новый пароль должен быть не короче 8 символов")
		return
	}
	if err := s.store.SetPassword(user, req.New); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if s.node != nil && strings.EqualFold(s.node.Account().Login, user) {
		if err := s.node.SetAccount(user, req.New); err != nil {
			log.Printf("смена пароля: %v", err)
		}
	}
	// сбрасываем остальные сессии пользователя
	s.dropSessions(user, currentSession(r))
	s.audit.Log(user, clientIP(r), "", "password_changed", "")
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) hEnrollKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"key": s.store.EnrollKey(), "hub_urls": s.hubURLs(), "addrs": s.hubAddrs(), "fingerprint": s.fingerprint, "node": s.node != nil})
}

func (s *Server) hRotateKey(w http.ResponseWriter, r *http.Request) {
	k, err := s.store.RotateEnrollKey()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.audit.Log(userOf(r), clientIP(r), "", "enroll_key_rotated", "")
	writeJSON(w, 200, map[string]string{"key": k})
}

func (s *Server) hAudit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.audit.Recent(500))
}

// ---------- устройства ----------

type deviceView struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Online   bool          `json:"online"`
	LastSeen time.Time     `json:"last_seen"`
	LastIP   string        `json:"last_ip"`
	Created  time.Time     `json:"created"`
	Info     proto.SysInfo `json:"info"`
	Stats    *proto.Stats  `json:"stats,omitempty"`
	Lock     InputLock     `json:"lock"`
	Clip     bool          `json:"clip"`            // участвует в общем буфере обмена
	Self     bool          `json:"self"`            // это главный ПК
	Controls []string      `json:"controls"`        // кем этот ПК может управлять
	Route    string        `json:"route,omitempty"` // тип связи: lan | vpn | zt | internet
}

func (s *Server) view(d Device) deviceView {
	v := deviceView{ID: d.ID, Name: d.Name, LastSeen: d.LastSeen, LastIP: d.LastIP, Created: d.Created, Info: d.Info,
		Lock: d.Lock, Clip: !d.NoClip, Self: s.isSelf(d), Controls: append([]string{}, d.Controls...)}
	s.mu.Lock()
	if ac := s.agents[d.ID]; ac != nil {
		v.Online, v.Route = true, ac.route
	}
	if h := s.history[d.ID]; v.Online && len(h) > 0 {
		st := h[len(h)-1]
		v.Stats = &st
	}
	s.mu.Unlock()
	return v
}

func (s *Server) hDevices(w http.ResponseWriter, r *http.Request) {
	devs := s.store.Devices()
	out := make([]deviceView, 0, len(devs))
	me := deviceOf(r)
	for _, d := range devs {
		if me != "" && !s.store.CanControl(me, d.ID) {
			continue // ПК видит только устройства, которыми ему разрешено управлять
		}
		v := s.view(d)
		if me != "" {
			v.Controls = nil
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
}

func (s *Server) hDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.Device(r.PathValue("id"))
	if !ok {
		jsonErr(w, 404, "устройство не найдено")
		return
	}
	writeJSON(w, 200, s.view(d))
}

type histPoint struct {
	T   int64   `json:"t"`
	CPU float64 `json:"cpu"`
	Mem float64 `json:"mem"`
	Rx  float64 `json:"rx"`
	Tx  float64 `json:"tx"`
}

func (s *Server) hHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.store.Device(id)
	if !ok {
		jsonErr(w, 404, "устройство не найдено")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	s.mu.Lock()
	h := s.history[id]
	pts := make([]histPoint, 0, len(h))
	for _, st := range h {
		if st.Time <= since {
			continue
		}
		mem := 0.0
		if st.MemTotal > 0 {
			mem = float64(st.MemUsed) / float64(st.MemTotal) * 100
		}
		pts = append(pts, histPoint{T: st.Time, CPU: st.CPU, Mem: mem, Rx: st.NetRx, Tx: st.NetTx})
	}
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"device": s.view(d), "points": pts})
}

func (s *Server) hRename(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 100 {
		jsonErr(w, 400, "укажите имя (до 100 символов)")
		return
	}
	id := r.PathValue("id")
	old := s.deviceName(id)
	if err := s.store.Rename(id, strings.TrimSpace(req.Name)); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	s.audit.Log(userOf(r), clientIP(r), old, "rename", req.Name)
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) hDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := s.deviceName(id)
	if err := s.store.Delete(id); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	s.mu.Lock()
	ac := s.agents[id]
	delete(s.history, id)
	s.mu.Unlock()
	if ac != nil {
		ac.close()
	}
	s.audit.Log(userOf(r), clientIP(r), name, "device_deleted", "")
	writeJSON(w, 200, proto.OK{OK: true})
}

// rpcMethods: разрешённые методы и нужно ли писать их в журнал.
var rpcMethods = map[string]bool{
	"procs":    false,
	"services": false,
	"kill":     true,
	"service":  true,
	"exec":     true,
	"power":    true,
}

func (s *Server) hRPC(w http.ResponseWriter, r *http.Request) {
	id, method := r.PathValue("id"), r.PathValue("method")
	logIt, ok := rpcMethods[method]
	if !ok {
		jsonErr(w, 404, "неизвестный метод")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		jsonErr(w, 400, "неверный JSON")
		return
	}
	ac := s.agent(id)
	if ac == nil {
		jsonErr(w, http.StatusConflict, "устройство не в сети")
		return
	}
	timeout := 30 * time.Second
	if method == "exec" {
		timeout = 11 * time.Minute
	}
	if logIt {
		s.audit.Log(userOf(r), clientIP(r), s.deviceName(id), method, string(body))
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	res, err := ac.call(ctx, method, body)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(res) == 0 {
		res = []byte("null")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(res)
}

func (s *Server) hWake(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.store.Device(id)
	if !ok {
		jsonErr(w, 404, "устройство не найдено")
		return
	}
	var macs []string
	for _, n := range d.Info.NICs {
		if n.MAC != "" {
			macs = append(macs, n.MAC)
		}
	}
	if len(macs) == 0 {
		jsonErr(w, 400, "MAC-адрес устройства неизвестен (агент ещё ни разу не подключался)")
		return
	}
	sent := 0
	for _, m := range macs {
		if wol.Send(m) == nil {
			sent++
		}
	}
	// ретрансляция через другие агенты в сети — они могут быть в одной локалке с целевым ПК
	s.mu.Lock()
	var relays []*agentConn
	for aid, ac := range s.agents {
		if aid != id {
			relays = append(relays, ac)
		}
	}
	s.mu.Unlock()
	var relayed atomic.Int32
	var wg sync.WaitGroup
	for _, ac := range relays {
		wg.Add(1)
		go func(ac *agentConn) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			okAny := false
			for _, m := range macs {
				b, _ := json.Marshal(proto.WolReq{MAC: m})
				if _, err := ac.call(ctx, "wol", b); err == nil {
					okAny = true
				}
			}
			if okAny {
				relayed.Add(1)
			}
		}(ac)
	}
	wg.Wait()
	s.audit.Log(userOf(r), clientIP(r), d.Name, "wake", strings.Join(macs, ", "))
	writeJSON(w, 200, map[string]any{"macs": macs, "hub_sent": sent > 0, "relays": relayed.Load()})
}

// ---------- терминал ----------

func (s *Server) hTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ac := s.agent(id)
	if ac == nil {
		jsonErr(w, http.StatusConflict, "устройство не в сети")
		return
	}
	q := r.URL.Query()
	shell := q.Get("shell")
	switch shell {
	case "powershell", "cmd", "pwsh":
	default:
		shell = "powershell"
	}
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	if cols < 20 || cols > 500 {
		cols = 120
	}
	if rows < 5 || rows > 200 {
		rows = 30
	}
	s.relayStream(w, r, ac, proto.StreamOpen{Kind: "terminal", Shell: shell, Cols: cols, Rows: rows}, "terminal")
}

func (s *Server) hDesktop(w http.ResponseWriter, r *http.Request) {
	ac := s.agent(r.PathValue("id"))
	if ac == nil {
		jsonErr(w, http.StatusConflict, "устройство не в сети")
		return
	}
	s.relayStream(w, r, ac, proto.StreamOpen{Kind: "desktop"}, "desktop")
}

// relayStream просит агента открыть поток и прозрачно соединяет его с браузером.
func (s *Server) relayStream(w http.ResponseWriter, r *http.Request, ac *agentConn, so proto.StreamOpen, auditName string) {
	id := ac.id
	bws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	sid := randToken(16)
	ps := &pendingStream{devID: id, ch: make(chan *websocket.Conn, 1)}
	s.mu.Lock()
	s.streams[sid] = ps
	s.mu.Unlock()
	cleanup := func() {
		s.mu.Lock()
		delete(s.streams, sid)
		s.mu.Unlock()
	}

	so.Session = sid
	data, _ := json.Marshal(so)
	if err := ac.send(proto.Msg{Type: proto.TypeStreamOpen, Data: data}); err != nil {
		cleanup()
		termError(bws, "не удалось связаться с агентом")
		return
	}
	var aws *websocket.Conn
	select {
	case aws = <-ps.ch:
	case <-time.After(15 * time.Second):
		cleanup()
		go func() { // агент мог подключиться в последний момент
			select {
			case c := <-ps.ch:
				c.Close()
			case <-time.After(5 * time.Second):
			}
		}()
		termError(bws, "агент не открыл сеанс")
		return
	}
	cleanup()
	user := userOf(r)
	name := s.deviceName(id)
	s.audit.Log(user, clientIP(r), name, auditName+"_open", so.Shell)
	start := time.Now()
	pipe(bws, aws)
	s.audit.Log(user, clientIP(r), name, auditName+"_close", time.Since(start).Round(time.Second).String())
}

func termError(ws *websocket.Conn, msg string) {
	b, _ := json.Marshal(proto.TermControl{Type: "error", Message: msg})
	ws.WriteMessage(websocket.TextMessage, b)
	ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	ws.Close()
}

// pipe прозрачно пересылает кадры между двумя WebSocket-соединениями.
func pipe(a, b *websocket.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				return
			}
			if err := dst.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	b.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	a.Close()
	b.Close()
	<-done
}

// ---------- сторона агента ----------

func (s *Server) authAgent(r *http.Request) (Device, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return Device{}, false
	}
	id, tok, ok := strings.Cut(strings.TrimPrefix(h, "Bearer "), ".")
	if !ok {
		return Device{}, false
	}
	return s.store.AuthDevice(id, tok)
}

func (s *Server) hAgentEnroll(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if wait := s.enrollLimiter.blocked(ip); wait > 0 {
		jsonErr(w, http.StatusTooManyRequests, "слишком много попыток")
		return
	}
	var req proto.EnrollReq
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	resp, err := s.store.Enroll(req.Key, req.Hostname, ip)
	if err != nil {
		s.enrollLimiter.fail(ip)
		s.audit.Log("", ip, req.Hostname, "enroll_failed", err.Error())
		jsonErr(w, http.StatusForbidden, err.Error())
		return
	}
	s.enrollLimiter.reset(ip)
	s.audit.Log("", ip, req.Hostname, "device_enrolled", resp.DeviceID)
	log.Printf("привязано новое устройство: %s (%s)", req.Hostname, ip)
	writeJSON(w, 200, resp)
}

func (s *Server) hAgentWS(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authAgent(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized, "устройство не авторизовано")
		return
	}
	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ip := clientIP(r)
	ac := newAgentConn(dev.ID, ws)
	ac.route = routeOf(net.ParseIP(ip))
	s.mu.Lock()
	old := s.agents[dev.ID]
	s.agents[dev.ID] = ac
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	s.store.Touch(dev.ID, ip)
	log.Printf("агент в сети: %s (%s)", dev.Name, ip)
	s.sendHubs(ac)
	if dev.Lock.Gamepad { // геймпад должен оставаться отключённым и после перезагрузки ПК
		go s.setGamepad(ac, true)
	}
	defer func() {
		s.mu.Lock()
		if s.agents[dev.ID] == ac {
			delete(s.agents, dev.ID)
		}
		s.mu.Unlock()
		ac.close()
		s.store.Touch(dev.ID, "")
		log.Printf("агент отключился: %s", dev.Name)
	}()

	ws.SetReadLimit(8 << 20)
	ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					ac.close()
					return
				}
			case <-ac.closed:
				return
			}
		}
	}()

	for {
		var m proto.Msg
		if err := ws.ReadJSON(&m); err != nil {
			return
		}
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		switch m.Type {
		case proto.TypeHello:
			var info proto.SysInfo
			if json.Unmarshal(m.Data, &info) == nil {
				s.store.UpdateInfo(dev.ID, info, ip)
			}
		case proto.TypeStats:
			var st proto.Stats
			if json.Unmarshal(m.Data, &st) == nil {
				s.pushStats(dev.ID, st)
			}
		case proto.TypeResp:
			ac.deliver(m)
		}
	}
}

func (s *Server) pushStats(id string, st proto.Stats) {
	st.Time = time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	h := append(s.history[id], st)
	if len(h) > historyMax+300 {
		h = append([]proto.Stats(nil), h[len(h)-historyMax:]...)
	}
	s.history[id] = h
}

func (s *Server) hAgentStream(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authAgent(r)
	if !ok {
		jsonErr(w, http.StatusUnauthorized, "устройство не авторизовано")
		return
	}
	sid := r.URL.Query().Get("session")
	s.mu.Lock()
	ps := s.streams[sid]
	if ps != nil && ps.devID == dev.ID {
		delete(s.streams, sid)
	} else {
		ps = nil
	}
	s.mu.Unlock()
	if ps == nil {
		jsonErr(w, 404, "сеанс не найден")
		return
	}
	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ps.ch <- ws
}

// ---------- соединение с агентом ----------

type agentConn struct {
	id      string
	route   string // как ПК подключён: lan | vpn | zt | internet
	ws      *websocket.Conn
	wmu     sync.Mutex
	pmu     sync.Mutex
	pending map[string]chan proto.Msg
	seq     atomic.Uint64
	closed  chan struct{}
	once    sync.Once
}

func newAgentConn(id string, ws *websocket.Conn) *agentConn {
	return &agentConn{id: id, ws: ws, pending: map[string]chan proto.Msg{}, closed: make(chan struct{})}
}

func (a *agentConn) send(m proto.Msg) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	a.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return a.ws.WriteJSON(m)
}

func (a *agentConn) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := strconv.FormatUint(a.seq.Add(1), 10)
	ch := make(chan proto.Msg, 1)
	a.pmu.Lock()
	a.pending[id] = ch
	a.pmu.Unlock()
	defer func() {
		a.pmu.Lock()
		delete(a.pending, id)
		a.pmu.Unlock()
	}()
	if err := a.send(proto.Msg{Type: proto.TypeReq, ID: id, Method: method, Data: params}); err != nil {
		return nil, errors.New("не удалось отправить запрос агенту")
	}
	select {
	case m := <-ch:
		if m.Error != "" {
			return nil, errors.New(m.Error)
		}
		return m.Data, nil
	case <-ctx.Done():
		return nil, errors.New("агент не ответил вовремя")
	case <-a.closed:
		return nil, errors.New("агент отключился")
	}
}

func (a *agentConn) deliver(m proto.Msg) {
	a.pmu.Lock()
	ch := a.pending[m.ID]
	a.pmu.Unlock()
	if ch != nil {
		select {
		case ch <- m:
		default:
		}
	}
}

func (a *agentConn) close() {
	a.once.Do(func() {
		close(a.closed)
		a.ws.Close()
	})
}

// ---------- ограничение попыток входа ----------

type limiter struct {
	mu sync.Mutex
	m  map[string]*limEntry
}

type limEntry struct {
	fails int
	until time.Time
}

func newLimiter() *limiter { return &limiter{m: map[string]*limEntry{}} }

func (l *limiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.m[ip]; e != nil {
		if d := time.Until(e.until); d > 0 {
			return d
		}
	}
	return 0
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[ip]
	if e == nil {
		e = &limEntry{}
		l.m[ip] = e
	}
	e.fails++
	if e.fails >= 5 {
		e.until = time.Now().Add(time.Duration(e.fails-4) * 30 * time.Second)
	}
}

func (l *limiter) gc() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, e := range l.m {
		if time.Since(e.until) > time.Hour {
			delete(l.m, k)
		}
	}
}

func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}

// hubAddr — адрес, по которому агенты и браузер могут достучаться до хаба.
type hubAddr struct {
	URL  string `json:"url"`
	Kind string `json:"kind"` // lan | vpn | public
}

// hubAddrs — адреса хаба: сначала локальные сетевые интерфейсы, затем внешние (интернет).
func (s *Server) hubAddrs() []hubAddr {
	var out []hubAddr
	seen := map[string]bool{}
	add := func(u, kind string) {
		if !seen[u] {
			seen[u] = true
			out = append(out, hubAddr{URL: u, Kind: kind})
		}
	}
	// порядок важен: агенты выбирают первый доступный адрес, поэтому прямая локальная
	// сеть всегда впереди, а ZeroTier (через интернет, медленнее) — после неё
	var lan, vpn, zt, pub []string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				u := "https://" + net.JoinHostPort(ipn.IP.String(), s.port)
				switch {
				case isZeroTierIface(ifc.Name):
					zt = append(zt, u)
				case addrKind(ipn.IP) == "lan":
					lan = append(lan, u)
				case addrKind(ipn.IP) == "vpn":
					vpn = append(vpn, u)
				default:
					pub = append(pub, u)
				}
			}
		}
	}
	for _, u := range lan {
		add(u, "lan")
	}
	for _, u := range vpn {
		add(u, "vpn")
	}
	for _, u := range zt {
		add(u, "zt")
	}
	for _, u := range pub {
		add(u, "public")
	}
	for _, u := range s.net.publicURLs() {
		add(u, "public")
	}
	return out
}

func (s *Server) hubURLs() []string {
	var out []string
	for _, a := range s.hubAddrs() {
		out = append(out, a.URL)
	}
	return out
}

// sendHubs сообщает агенту актуальные адреса хаба, чтобы он мог подключаться
// и из локальной сети, и через интернет без переустановки.
func (s *Server) sendHubs(ac *agentConn) {
	b, _ := json.Marshal(proto.HubList{URLs: s.hubURLs()})
	ac.send(proto.Msg{Type: proto.TypeHubs, Data: b})
}

func (s *Server) broadcastHubs() {
	s.mu.Lock()
	list := make([]*agentConn, 0, len(s.agents))
	for _, ac := range s.agents {
		list = append(list, ac)
	}
	s.mu.Unlock()
	for _, ac := range list {
		go s.sendHubs(ac)
	}
}
