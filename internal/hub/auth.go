package hub

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"randdesk/internal/proto"
)

// loginTicket — пароль проверен, ждём второй фактор.
type loginTicket struct {
	user    string
	expires time.Time
	tries   int
}

// pendingTOTP — секрет, показанный пользователю, но ещё не подтверждённый кодом.
type pendingTOTP struct {
	secret  string
	expires time.Time
}

const (
	ticketTTL      = 5 * time.Minute
	ticketMaxTries = 5
)

// ---------- вход ----------

func (s *Server) hLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if wait := s.limiter.blocked(ip); wait > 0 {
		jsonErr(w, http.StatusTooManyRequests, "слишком много попыток, подождите "+strconv.Itoa(int(wait.Seconds())+1)+" с")
		return
	}
	var req struct{ User, Password string }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	user, ok := s.store.CheckUser(strings.TrimSpace(req.User), req.Password)
	if !ok {
		s.limiter.fail(ip)
		s.audit.Log(req.User, ip, "", "login_failed", "")
		jsonErr(w, http.StatusUnauthorized, "неверное имя или пароль")
		return
	}
	if u, _ := s.store.User(user); u.TOTP {
		t := randToken(24)
		s.mu.Lock()
		s.tickets[t] = &loginTicket{user: user, expires: time.Now().Add(ticketTTL)}
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"totp_required": true, "ticket": t})
		return
	}
	s.limiter.reset(ip)
	s.audit.Log(user, ip, "", "login", "")
	s.startSession(w, r, user)
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) hLoginTOTP(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if wait := s.limiter.blocked(ip); wait > 0 {
		jsonErr(w, http.StatusTooManyRequests, "слишком много попыток, подождите "+strconv.Itoa(int(wait.Seconds())+1)+" с")
		return
	}
	var req struct{ Ticket, Code string }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	s.mu.Lock()
	t := s.tickets[req.Ticket]
	if t != nil && (time.Now().After(t.expires) || t.tries >= ticketMaxTries) {
		delete(s.tickets, req.Ticket)
		t = nil
	}
	if t != nil {
		t.tries++
	}
	s.mu.Unlock()
	if t == nil {
		jsonErr(w, http.StatusUnauthorized, "сеанс входа истёк — введите пароль заново")
		return
	}
	ok, usedRecovery := s.store.CheckSecondFactor(t.user, req.Code)
	if !ok {
		s.limiter.fail(ip)
		s.audit.Log(t.user, ip, "", "login_2fa_failed", "")
		jsonErr(w, http.StatusUnauthorized, "неверный код")
		return
	}
	s.mu.Lock()
	delete(s.tickets, req.Ticket)
	s.mu.Unlock()
	s.limiter.reset(ip)
	detail := "2FA"
	if usedRecovery {
		detail = "2FA, резервный код"
	}
	s.audit.Log(t.user, ip, "", "login", detail)
	s.startSession(w, r, t.user)
	writeJSON(w, 200, proto.OK{OK: true})
}

// confirmIdentity проверяет пароль текущего пользователя и, если 2FA включена, код.
func (s *Server) confirmIdentity(r *http.Request, password, code string) (string, bool) {
	user := userOf(r)
	ip := clientIP(r)
	if s.limiter.blocked(ip) > 0 {
		return "слишком много попыток, подождите", false
	}
	if _, ok := s.store.CheckUser(user, password); !ok {
		s.limiter.fail(ip)
		return "пароль неверен", false
	}
	if u, _ := s.store.User(user); u.TOTP {
		if ok, _ := s.store.CheckSecondFactor(user, code); !ok {
			s.limiter.fail(ip)
			return "неверный код подтверждения", false
		}
	}
	return "", true
}

func (s *Server) hMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.store.User(userOf(r))
	if !ok {
		jsonErr(w, 404, "пользователь не найден")
		return
	}
	writeJSON(w, 200, u)
}

// dropSessions завершает сессии пользователя, кроме текущей (keep).
func (s *Server) dropSessions(user, keep string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.sessions {
		if strings.EqualFold(v.user, user) && k != keep {
			delete(s.sessions, k)
		}
	}
}

func currentSession(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// ---------- 2FA ----------

func (s *Server) h2FASetup(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	if u, _ := s.store.User(user); u.TOTP {
		jsonErr(w, http.StatusConflict, "двухфакторная аутентификация уже включена")
		return
	}
	secret := newTOTPSecret()
	s.mu.Lock()
	s.pendingTOTP[strings.ToLower(user)] = pendingTOTP{secret: secret, expires: time.Now().Add(15 * time.Minute)}
	s.mu.Unlock()
	uri := totpURI(user, secret)
	resp := map[string]any{"secret": secret, "uri": uri}
	if code, err := qr.Encode(uri, qr.M); err == nil {
		code.Scale = 6
		resp["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
	}
	writeJSON(w, 200, resp)
}

func (s *Server) h2FAEnable(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	var req struct{ Code string }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	key := strings.ToLower(user)
	s.mu.Lock()
	p, ok := s.pendingTOTP[key]
	s.mu.Unlock()
	if !ok || time.Now().After(p.expires) {
		jsonErr(w, 400, "время настройки истекло — начните заново")
		return
	}
	if _, good := checkTOTP(p.secret, req.Code, 0, time.Now()); !good {
		jsonErr(w, 400, "код не подходит — проверьте время на телефоне и введите новый код")
		return
	}
	codes, hashes := newRecoveryCodes()
	if err := s.store.EnableTOTP(user, p.secret, hashes); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.mu.Lock()
	delete(s.pendingTOTP, key)
	s.mu.Unlock()
	s.dropSessions(user, currentSession(r))
	s.audit.Log(user, clientIP(r), "", "2fa_enabled", "")
	writeJSON(w, 200, map[string]any{"recovery": codes})
}

func (s *Server) h2FADisable(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password, Code string }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	if msg, ok := s.confirmIdentity(r, req.Password, req.Code); !ok {
		jsonErr(w, 400, msg)
		return
	}
	user := userOf(r)
	if err := s.store.DisableTOTP(user); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.audit.Log(user, clientIP(r), "", "2fa_disabled", "")
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) h2FARecovery(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password, Code string }
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	if msg, ok := s.confirmIdentity(r, req.Password, req.Code); !ok {
		jsonErr(w, 400, msg)
		return
	}
	user := userOf(r)
	codes, hashes := newRecoveryCodes()
	if err := s.store.SetRecovery(user, hashes); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.audit.Log(user, clientIP(r), "", "2fa_recovery_regenerated", "")
	writeJSON(w, 200, map[string]any{"recovery": codes})
}

// ---------- пользователи ----------

func (s *Server) hUsers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.store.Users())
}

func (s *Server) hUserCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name, Password  string
		CurrentPassword string `json:"current_password"`
		Code            string
	}
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	if msg, ok := s.confirmIdentity(r, req.CurrentPassword, req.Code); !ok {
		jsonErr(w, 400, "подтверждение: "+msg)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 || strings.ContainsAny(req.Name, "/\\<>\"") {
		jsonErr(w, 400, "укажите имя пользователя (до 64 символов, без / \\ < > \")")
		return
	}
	if len(req.Password) < 8 {
		jsonErr(w, 400, "пароль должен быть не короче 8 символов")
		return
	}
	if err := s.store.CreateUser(req.Name, req.Password, false); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.audit.Log(userOf(r), clientIP(r), "", "user_created", req.Name)
	writeJSON(w, 200, proto.OK{OK: true})
}

func (s *Server) hUserDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.EqualFold(name, userOf(r)) {
		jsonErr(w, 400, "нельзя удалить самого себя")
		return
	}
	if err := s.store.DeleteUser(name); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.dropSessions(name, "")
	s.audit.Log(userOf(r), clientIP(r), "", "user_deleted", name)
	writeJSON(w, 200, proto.OK{OK: true})
}

// ---------- доступ из интернета ----------

func (s *Server) hInternet(w http.ResponseWriter, r *http.Request) {
	u, _ := s.store.User(userOf(r))
	writeJSON(w, 200, map[string]any{
		"status":      s.net.status(),
		"addrs":       s.hubAddrs(),
		"fingerprint": s.fingerprint,
		"totp":        u.TOTP,
	})
}

func (s *Server) hInternetSet(w http.ResponseWriter, r *http.Request) {
	var req Settings
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	req.PublicHost = strings.TrimSpace(req.PublicHost)
	if req.PublicHost != "" {
		h := normalizeHost(req.PublicHost, 0)
		host, _, _ := net.SplitHostPort(h)
		if host == "" || strings.ContainsAny(host, " /?#@") {
			jsonErr(w, 400, "неверный внешний адрес: укажите домен или IP, например myhome.duckdns.org или 1.2.3.4:8420")
			return
		}
	}
	old := s.store.Settings()
	req.Touched = true
	if err := s.store.SetSettings(req); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	b, _ := json.Marshal(req)
	s.audit.Log(userOf(r), clientIP(r), "", "internet_settings", string(b))
	s.net.refresh()
	if old.PublicHost != req.PublicHost {
		s.broadcastHubs()
	}
	writeJSON(w, 200, proto.OK{OK: true})
}

// ---------- обслуживание ----------

// janitor периодически удаляет просроченные сессии, билеты входа и записи ограничителя.
func (s *Server) janitor() {
	for range time.Tick(10 * time.Minute) {
		now := time.Now()
		s.mu.Lock()
		for k, v := range s.sessions {
			if now.After(v.expires) {
				delete(s.sessions, k)
			}
		}
		for k, v := range s.tickets {
			if now.After(v.expires) {
				delete(s.tickets, k)
			}
		}
		for k, v := range s.pendingTOTP {
			if now.After(v.expires) {
				delete(s.pendingTOTP, k)
			}
		}
		s.mu.Unlock()
		s.limiter.gc()
		s.enrollLimiter.gc()
	}
}
