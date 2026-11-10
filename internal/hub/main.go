package hub

import (
	"crypto/tls"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"randdesk/internal/proto"
)

func Main(args []string) {
	fs := flag.NewFlagSet("hub", flag.ExitOnError)
	listen := fs.String("listen", ":8420", "адрес и порт для прослушивания (HTTP и HTTPS на одном порту)")
	dataDir := fs.String("data", defaultDataDir(), "папка с данными хаба")
	certFile := fs.String("tls-cert", "", "свой TLS-сертификат (по умолчанию создаётся самоподписанный)")
	keyFile := fs.String("tls-key", "", "ключ к -tls-cert")
	httpPolicy := fs.String("http", "lan", "откуда разрешён незашифрованный HTTP: lan (локальная сеть и VPN), all, off (только localhost)")
	fs.Parse(args)

	switch *httpPolicy {
	case "lan", "all", "off":
	default:
		log.Fatalf("неверный -http %q: допустимо lan, all, off", *httpPolicy)
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatalf("не удалось создать папку данных: %v", err)
	}
	store, err := OpenStore(filepath.Join(*dataDir, "hub.json"))
	if err != nil {
		log.Fatalf("хранилище: %v", err)
	}
	audit, err := OpenAudit(filepath.Join(*dataDir, "audit.jsonl"))
	if err != nil {
		log.Fatalf("журнал: %v", err)
	}
	_, portStr, err := net.SplitHostPort(*listen)
	if err != nil {
		log.Fatalf("неверный -listen: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	var cert tls.Certificate
	fingerprint := ""
	if *certFile != "" {
		if cert, err = tls.LoadX509KeyPair(*certFile, *keyFile); err != nil {
			log.Fatalf("TLS-сертификат: %v", err)
		}
	} else {
		if cert, err = loadOrCreateCert(*dataDir); err != nil {
			log.Fatalf("самоподписанный сертификат: %v", err)
		}
		if fingerprint, err = spkiFingerprint(cert); err != nil {
			log.Fatalf("отпечаток сертификата: %v", err)
		}
	}

	srv := NewServer(store, audit, portStr, fingerprint)
	srv.net = newInternet(store, port, srv.broadcastHubs)
	go srv.net.run()
	go srv.janitor()

	log.Printf("RandDesk hub %s, данные: %s", proto.Version, *dataDir)
	log.Printf("веб-интерфейс на этом ПК: http://localhost:%s", portStr)
	if fingerprint != "" {
		log.Printf("отпечаток сертификата хаба: %s", fingerprint)
	}
	for _, a := range srv.hubAddrs() {
		log.Printf("адрес для агентов/браузера (%s): %s", a.Kind, a.URL)
	}
	if !store.HasUsers() {
		log.Printf("администратор ещё не создан — откройте http://localhost:%s на этом ПК", portStr)
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
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
	hsPlain := newSrv(plainGuard(*httpPolicy, handler))

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Print("остановка хаба…")
		srv.net.shutdown()
		os.Exit(0)
	}()
	go func() { log.Fatal(hsPlain.Serve(plainL)) }()
	log.Fatal(hsTLS.ServeTLS(tlsL, "", ""))
}

func defaultDataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "data"
	}
	return filepath.Join(filepath.Dir(exe), "data")
}
