package hub

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"randsec/internal/pair"
	"randsec/internal/proto"
	"randsec/internal/ui"
)

// Options — параметры запуска хаба.
type Options struct {
	Listen     string
	DataDir    string
	CertFile   string
	KeyFile    string
	HTTPPolicy string     // lan | all | off
	Node       *NodeHooks // режим «узла»: ПК связываются по общему логину и паролю
}

// NodeHooks связывает хаб со службой узла.
type NodeHooks struct {
	// Account — текущая учётная запись (секреты из логина и пароля).
	Account func() pair.Account
	// SetAccount вызывается при первой настройке ПК и смене пароля.
	SetAccount func(login, password string) error
	// SelfMachine — идентификатор самого главного ПК (его ввод нельзя отключить).
	SelfMachine string
	// ZeroTier — состояние сети через интернет; SetZeroTier — включить ("on") или выключить ("off").
	ZeroTier    func() any
	SetZeroTier func(token string) error
}

// Hub — запущенный хаб.
type Hub struct {
	Fingerprint string
	Port        int
	srv         *Server
	errc        chan error
}

func Main(args []string) {
	fs := flag.NewFlagSet("hub", flag.ExitOnError)
	o := Options{}
	fs.StringVar(&o.Listen, "listen", ":8420", "адрес и порт для прослушивания (HTTP и HTTPS на одном порту)")
	fs.StringVar(&o.DataDir, "data", defaultDataDir(), "папка с данными хаба")
	fs.StringVar(&o.CertFile, "tls-cert", "", "свой TLS-сертификат (по умолчанию создаётся самоподписанный)")
	fs.StringVar(&o.KeyFile, "tls-key", "", "ключ к -tls-cert")
	fs.StringVar(&o.HTTPPolicy, "http", "lan", "откуда разрешён незашифрованный HTTP: lan (локальная сеть и VPN), all, off (только localhost)")
	fs.Parse(args)

	h, err := Start(o)
	if err != nil {
		ui.Fatal(err)
	}
	if ui.OwnConsole() {
		log.Printf("хаб работает — не закрывайте это окно (закрытие окна остановит хаб)")
		ui.OpenBrowser("http://localhost:" + strconv.Itoa(h.Port))
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Print("остановка хаба…")
		h.Stop()
		os.Exit(0)
	}()
	ui.Fatal(h.Wait())
}

// Start запускает хаб и возвращается сразу после того, как порт занят.
func Start(o Options) (*Hub, error) {
	if o.Listen == "" {
		o.Listen = ":8420"
	}
	if o.HTTPPolicy == "" {
		o.HTTPPolicy = "lan"
	}
	switch o.HTTPPolicy {
	case "lan", "all", "off":
	default:
		return nil, fmt.Errorf("неверный -http %q: допустимо lan, all, off", o.HTTPPolicy)
	}
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("не удалось создать папку данных: %w", err)
	}
	store, err := OpenStore(filepath.Join(o.DataDir, "hub.json"))
	if err != nil {
		return nil, fmt.Errorf("хранилище: %w", err)
	}
	audit, err := OpenAudit(filepath.Join(o.DataDir, "audit.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("журнал: %w", err)
	}
	_, portStr, err := net.SplitHostPort(o.Listen)
	if err != nil {
		return nil, fmt.Errorf("неверный -listen: %w", err)
	}
	port, _ := strconv.Atoi(portStr)

	var cert tls.Certificate
	fingerprint := ""
	if o.CertFile != "" {
		if cert, err = tls.LoadX509KeyPair(o.CertFile, o.KeyFile); err != nil {
			return nil, fmt.Errorf("TLS-сертификат: %w", err)
		}
	} else {
		if cert, err = loadOrCreateCert(o.DataDir); err != nil {
			return nil, fmt.Errorf("самоподписанный сертификат: %w", err)
		}
		if fingerprint, err = spkiFingerprint(cert); err != nil {
			return nil, fmt.Errorf("отпечаток сертификата: %w", err)
		}
	}

	ln, err := net.Listen("tcp", o.Listen)
	if err != nil {
		return nil, fmt.Errorf("не удалось занять порт %s: %v\nВозможно, RANDSEC уже запущен (проверьте http://localhost:%s) или порт занят другой программой", portStr, err, portStr)
	}

	srv := NewServer(store, audit, portStr, fingerprint)
	srv.node = o.Node
	srv.net = newInternet(store, port, srv.broadcastHubs)
	go srv.net.run()
	go srv.janitor()

	log.Printf("RANDSEC hub %s, данные: %s", proto.Version, o.DataDir)
	log.Printf("веб-интерфейс на этом ПК: http://localhost:%s", portStr)
	if fingerprint != "" {
		log.Printf("отпечаток сертификата хаба: %s", fingerprint)
	}
	for _, a := range srv.hubAddrs() {
		log.Printf("адрес для агентов/браузера (%s): %s", a.Kind, a.URL)
	}
	if !store.HasUsers() && o.Node == nil {
		log.Printf("ПК ещё не настроен — откройте http://localhost:%s на этом ПК", portStr)
	}

	tlsL, plainL := splitListener(ln)
	handler := srv.Handler()
	newSrv := func(h http.Handler) *http.Server {
		return &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			// HTTP/2 отключён: WebSocket (терминал, рабочий стол) работает только поверх HTTP/1.1
			TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		}
	}
	hsTLS := newSrv(handler)
	hsTLS.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	hsPlain := newSrv(plainGuard(o.HTTPPolicy, handler))

	h := &Hub{Fingerprint: fingerprint, Port: port, srv: srv, errc: make(chan error, 2)}
	go func() { h.errc <- hsPlain.Serve(plainL) }()
	go func() { h.errc <- hsTLS.ServeTLS(tlsL, "", "") }()
	return h, nil
}

// Wait ждёт аварийной остановки сервера.
func (h *Hub) Wait() error {
	err := <-h.errc
	if err == nil {
		err = errors.New("сервер остановлен")
	}
	return err
}

// Stop снимает проброс порта на роутере (вызывается при остановке).
func (h *Hub) Stop() { h.srv.net.shutdown() }

// EnableInternet включает UPnP-проброс, если пользователь ещё не выбирал вручную.
func (h *Hub) EnableInternet() {
	st := h.srv.store.Settings()
	if !st.UPnP && !st.Touched {
		st.UPnP = true
		st.Touched = true
		h.srv.store.SetSettings(st)
		h.srv.net.refresh()
	}
}

func defaultDataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "data"
	}
	return filepath.Join(filepath.Dir(exe), "data")
}

// CreateAdmin создаёт первого пользователя панели (при настройке главного ПК).
func (h *Hub) CreateAdmin(login, password string) error {
	return h.srv.store.CreateUser(login, password, true)
}

// URLs — адреса этого хаба (локальные и внешние).
func (h *Hub) URLs() []string { return h.srv.hubURLs() }
