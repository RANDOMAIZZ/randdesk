package node

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"randdesk/internal/agent"
	"randdesk/internal/hub"
	"randdesk/internal/pair"
	"randdesk/internal/proto"
	"randdesk/web"
)

// Options — где хранить данные и какие порты занимать.
type Options struct {
	Dir     string // C:\ProgramData\RandDesk
	Port    int    // веб-интерфейс (8420)
	UDPPort int    // поиск ПК в сети (8421)
}

type node struct {
	o  Options
	st *store

	mu       sync.Mutex
	link     *agent.Agent // связь этого ПК с главным
	status   string       // для страницы обычного ПК: что сейчас происходит
	lastErr  string
	admin    string // имя главного ПК
	pairing  bool
	switchTo chan struct{}
}

// Run — тело службы: работает до закрытия stop.
func Run(o Options, stop <-chan struct{}) error {
	st, err := openStore(o.Dir)
	if err != nil {
		return err
	}
	n := &node{o: o, st: st, switchTo: make(chan struct{}, 1)}
	for {
		var err error
		switch n.st.get().Role {
		case RoleAdmin:
			err = n.runAdmin(stop)
		case RoleMember:
			err = n.runMember(stop)
		default:
			err = n.runSetup(stop)
		}
		select {
		case <-stop:
			return nil
		default:
		}
		if err != nil {
			log.Printf("RandDesk: %v; повтор через 10 с", err)
			select {
			case <-stop:
				return nil
			case <-time.After(10 * time.Second):
			}
		}
	}
}

// ---------- общие части ----------

func (n *node) loopbackAddr() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(n.o.Port)) }

// localOnly защищает локальную страницу: только с этого ПК и только со своей страницы
// (защита от чужих сайтов в браузере и подмены DNS).
func (n *node) localOnly(h http.Handler) http.Handler {
	port := strconv.Itoa(n.o.Port)
	okHost := map[string]bool{"localhost:" + port: true, "127.0.0.1:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !okHost[strings.ToLower(r.Host)] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !okHost[strings.ToLower(u.Host)] {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "randdesk" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func serve(stop <-chan struct{}, addr string, h http.Handler, done <-chan struct{}) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-stop:
	case <-done:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	return nil
}

// startLink запускает агента этого ПК с конфигурацией связи.
func (n *node) startLink(stop <-chan struct{}) *agent.Agent {
	cfg, err := agent.LoadConfigFile(n.st.peerPath())
	if err != nil {
		return nil
	}
	a := agent.New(cfg)
	a.Session = true
	go a.Run(stop)
	n.mu.Lock()
	n.link = a
	n.mu.Unlock()
	return a
}

func (n *node) getLink() *agent.Agent {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.link
}

// ---------- первая настройка ----------

func (n *node) runSetup(stop <-chan struct{}) error {
	log.Printf("ПК не настроен — откройте http://localhost:%d и нажмите «Настроить ПК»", n.o.Port)
	done := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"version": proto.Version, "node_setup": true, "host": hostname()})
	})
	mux.HandleFunc("POST /api/node/setup", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Role, User, Password string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "неверный запрос"})
			return
		}
		req.User = strings.TrimSpace(req.User)
		if req.User == "" || len(req.User) > 64 || strings.ContainsAny(req.User, "/\\<>\"") {
			writeJSON(w, 400, map[string]string{"error": "укажите логин (до 64 символов)"})
			return
		}
		if len(req.Password) < 8 {
			writeJSON(w, 400, map[string]string{"error": "пароль должен быть не короче 8 символов"})
			return
		}
		if req.Role != RoleAdmin && req.Role != RoleMember {
			writeJSON(w, 400, map[string]string{"error": "выберите, главный это ПК или обычный"})
			return
		}
		acc, err := pair.Derive(req.User, req.Password)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if req.Role == RoleAdmin {
			// пользователь панели администратора создаётся в хабе при его запуске
			pendingAdmin.Store(&adminCreds{req.User, req.Password})
		}
		if err := n.st.set(func(c *Config) { c.Role, c.Account = req.Role, acc }); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("ПК настроен: %s, логин %s", map[string]string{RoleAdmin: "главный", RoleMember: "обычный"}[req.Role], req.User)
		writeJSON(w, 200, map[string]any{"ok": true})
		go func() { time.Sleep(300 * time.Millisecond); once.Do(func() { close(done) }) }()
	})
	mux.Handle("/", http.FileServer(http.FS(web.FS())))
	return serve(stop, n.loopbackAddr(), n.localOnly(mux), done)
}

type adminCreds struct{ user, pass string }

var pendingAdmin atomic.Pointer[adminCreds]

// ---------- главный ПК ----------

func (n *node) runAdmin(stop <-chan struct{}) error {
	cfg := n.st.get()
	hooks := &hub.NodeHooks{
		Account: func() pair.Account { return n.st.get().Account },
		SetAccount: func(login, password string) error {
			acc, err := pair.Derive(login, password)
			if err != nil {
				return err
			}
			return n.st.set(func(c *Config) { c.Account = acc })
		},
		SelfMachine: cfg.Machine,
	}
	h, err := hub.Start(hub.Options{Listen: ":" + strconv.Itoa(n.o.Port), DataDir: n.st.hubDir(), Node: hooks})
	if err != nil {
		return err
	}
	defer h.Stop()
	if c := pendingAdmin.Swap(nil); c != nil {
		if err := h.CreateAdmin(c.user, c.pass); err != nil {
			log.Printf("администратор: %v", err)
		}
		h.EnableInternet()
	}
	go announcer(stop, n.o.UDPPort, func() beacon {
		acc := n.st.get().Account
		return beacon{RD: 1, Type: "here", Group: acc.Group, Login: loginHash(acc.Login), FP: h.Fingerprint, Port: h.Port, Name: hostname()}
	})
	// главный ПК тоже виден в своей панели как устройство
	go func() {
		self := "https://" + n.loopbackAddr()
		for {
			if n.startLink(stop) != nil {
				return
			}
			acfg, _, err := pairWith(self, n.st.get().Account, cfg.Machine)
			if err == nil {
				err = agent.SaveConfigFile(n.st.peerPath(), acfg)
			}
			if err != nil {
				log.Printf("подключение главного ПК к своей панели: %v", err)
				select {
				case <-stop:
					return
				case <-time.After(10 * time.Second):
				}
			}
		}
	}()
	errc := make(chan error, 1)
	go func() { errc <- h.Wait() }()
	select {
	case <-stop:
		return nil
	case err := <-errc:
		return err
	}
}

// ---------- обычный ПК ----------

func (n *node) setStatus(status, errText string) {
	n.mu.Lock()
	n.status, n.lastErr = status, errText
	n.mu.Unlock()
}

// tryPair привязывает ПК к найденному главному ПК (одна попытка за раз).
func (n *node) tryPair(stop <-chan struct{}, hubURL, name string) error {
	n.mu.Lock()
	if n.pairing || n.link != nil {
		n.mu.Unlock()
		return nil
	}
	n.pairing = true
	n.mu.Unlock()
	defer func() { n.mu.Lock(); n.pairing = false; n.mu.Unlock() }()
	n.setStatus("Подключение к главному ПК "+name+"…", "")
	cfg := n.st.get()
	acfg, adminName, err := pairWith(hubURL, cfg.Account, cfg.Machine)
	if err != nil {
		n.setStatus("Поиск главного ПК…", err.Error())
		return err
	}
	if err := agent.SaveConfigFile(n.st.peerPath(), acfg); err != nil {
		n.setStatus("Поиск главного ПК…", err.Error())
		return err
	}
	n.mu.Lock()
	n.admin = adminName
	n.mu.Unlock()
	log.Printf("подключено к главному ПК %s (%s)", adminName, hubURL)
	n.startLink(stop)
	return nil
}

func (n *node) runMember(parent <-chan struct{}) error {
	// своя остановка: «Изменить логин и пароль» возвращает ПК к первой настройке
	stop := make(chan struct{})
	var once sync.Once
	reset := func() { once.Do(func() { close(stop) }) }
	go func() {
		select {
		case <-parent:
			reset()
		case <-stop:
		}
	}()
	defer reset()
	defer func() { n.mu.Lock(); n.link = nil; n.mu.Unlock() }()
	n.setStatus("Поиск главного ПК…", "")
	n.startLink(stop)
	acc := n.st.get().Account
	var lastTry sync.Map
	go seeker(stop, n.o.UDPPort, acc.Group, loginHash(acc.Login),
		func() bool { return n.getLink() == nil },
		func(u, fp, name string) {
			if a := n.getLink(); a != nil {
				if c := a.Config(); fp != "" && fp == c.Fingerprint {
					a.AddHubs(u) // главный ПК найден в этой сети — используем прямой адрес
				}
				return
			}
			if t, ok := lastTry.Load(u); ok && time.Since(t.(time.Time)) < 10*time.Second {
				return
			}
			lastTry.Store(u, time.Now())
			go n.tryPair(stop, u, name)
		},
		func(name string) {
			if n.getLink() == nil {
				n.setStatus("Поиск главного ПК…", "Найден главный ПК «"+name+"», но пароль не совпадает. Нажмите «Изменить логин и пароль» и введите те же, что на главном ПК.")
			}
		})
	return serve(parent, n.loopbackAddr(), n.localOnly(n.memberHandler(stop, reset)), stop)
}

// memberHandler — локальная страница обычного ПК: показывает разрешённые администратором ПК,
// пересылая запросы на главный ПК от имени этого ПК.
func (n *node) memberHandler(stop <-chan struct{}, reset func()) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/node/reset", func(w http.ResponseWriter, r *http.Request) {
		if a := n.getLink(); a != nil && a.Online() {
			writeJSON(w, 409, map[string]string{"error": "ПК уже подключён к главному"})
			return
		}
		os.Remove(n.st.peerPath())
		if err := n.st.set(func(c *Config) { c.Role, c.Account = "", pair.Account{} }); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("настройки сброшены — ПК снова ждёт «Настроить ПК»")
		writeJSON(w, 200, map[string]any{"ok": true})
		go func() { time.Sleep(300 * time.Millisecond); reset() }()
	})
	static := http.FileServer(http.FS(web.FS()))
	mux.HandleFunc("GET /api/node/status", func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		resp := map[string]any{"status": n.status, "error": n.lastErr, "admin": n.admin, "login": n.st.get().Account.Login}
		n.mu.Unlock()
		a := n.getLink()
		resp["linked"] = a != nil
		resp["online"] = a != nil && a.Online()
		writeJSON(w, 200, resp)
	})
	mux.HandleFunc("POST /api/node/connect", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Address string }
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		addr := strings.TrimSpace(req.Address)
		if addr == "" {
			writeJSON(w, 400, map[string]string{"error": "укажите адрес главного ПК"})
			return
		}
		if !strings.Contains(addr, "://") {
			addr = "https://" + addr
		}
		u, err := url.Parse(addr)
		if err != nil || u.Hostname() == "" {
			writeJSON(w, 400, map[string]string{"error": "неверный адрес"})
			return
		}
		if u.Port() == "" {
			u.Host = net.JoinHostPort(u.Hostname(), "8420")
		}
		u.Scheme, u.Path = "https", ""
		if err := n.tryPair(stop, u.String(), u.Hostname()); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			a := n.getLink()
			c := a.Config()
			target, _ := url.Parse(a.Current())
			pr.SetURL(target)
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Set("Authorization", "Bearer "+c.DeviceID+"."+c.Token)
			pr.Out.Header.Set("X-RandDesk-Local", "1")
		},
		Transport: &dynTransport{n: n},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "нет связи с главным ПК"})
		},
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		a := n.getLink()
		if a == nil || !a.Online() {
			if r.URL.Path == "/api/state" {
				writeJSON(w, 200, map[string]any{"version": proto.Version, "node_member": true, "searching": true})
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "нет связи с главным ПК"})
			return
		}
		proxy.ServeHTTP(w, r)
	})
	mux.Handle("/", static)
	return mux
}

// dynTransport — HTTPS к главному ПК с проверкой его отпечатка (отпечаток может появиться после привязки).
type dynTransport struct {
	n  *node
	mu sync.Mutex
	fp string
	t  *http.Transport
}

func (d *dynTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	a := d.n.getLink()
	if a == nil {
		return nil, errors.New("нет связи")
	}
	fp := a.Config().Fingerprint
	d.mu.Lock()
	if d.t == nil || d.fp != fp {
		d.t = &http.Transport{TLSClientConfig: agent.TLSConfig(fp), ForceAttemptHTTP2: false,
			TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
		d.fp = fp
	}
	t := d.t
	d.mu.Unlock()
	return t.RoundTrip(r)
}
