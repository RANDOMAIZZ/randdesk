package hub

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// loadOrCreateCert загружает самоподписанный сертификат хаба из папки данных или создаёт новый.
// Агенты проверяют хаб по отпечатку открытого ключа (закрепление), поэтому
// доверенный центр сертификации не нужен.
func loadOrCreateCert(dataDir string) (tls.Certificate, error) {
	certPath := filepath.Join(dataDir, "hub-cert.pem")
	keyPath := filepath.Join(dataDir, "hub-key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(randBytes(16)),
		Subject:               pkix.Name{CommonName: "RandDesk Hub " + host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	if host != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}

// spkiFingerprint — SHA-256 открытого ключа сертификата (то, что закрепляют агенты).
func spkiFingerprint(c tls.Certificate) (string, error) {
	if len(c.Certificate) == 0 {
		return "", errors.New("пустой сертификат")
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}

// ---------- HTTP и HTTPS на одном порту ----------

// chanListener — net.Listener, в который соединения подаются извне.
type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{addr: addr, ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

func (l *chanListener) push(c net.Conn) {
	select {
	case l.ch <- c:
	case <-l.done:
		c.Close()
	}
}

// peekConn отдаёт сначала подсмотренные байты, затем остальное соединение.
type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// splitListener по первому байту отделяет TLS-соединения (0x16 — ClientHello) от обычного HTTP.
func splitListener(ln net.Listener) (tlsL, plainL *chanListener) {
	tlsL, plainL = newChanListener(ln.Addr()), newChanListener(ln.Addr())
	go func() {
		defer tlsL.Close()
		defer plainL.Close()
		for {
			c, err := ln.Accept()
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					time.Sleep(50 * time.Millisecond)
					continue
				}
				return
			}
			go func(c net.Conn) {
				c.SetReadDeadline(time.Now().Add(10 * time.Second))
				br := bufio.NewReader(c)
				b, err := br.Peek(1)
				if err != nil {
					c.Close()
					return
				}
				c.SetReadDeadline(time.Time{})
				pc := &peekConn{Conn: c, r: br}
				if b[0] == 0x16 {
					tlsL.push(pc)
				} else {
					plainL.push(pc)
				}
			}(c)
		}
	}()
	return tlsL, plainL
}

// ---------- политика для незашифрованного HTTP ----------

var (
	cgnatNet  = mustCIDR("100.64.0.0/10") // CGNAT провайдеров, Tailscale
	radminNet = mustCIDR("26.0.0.0/8")    // RadminVPN
	benchNet  = mustCIDR("198.18.0.0/15") // TUN-адаптеры VPN/прокси-клиентов
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// isLocalNet — адрес из локальной сети или распространённых VPN.
func isLocalNet(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnatNet.Contains(ip) || radminNet.Contains(ip) || benchNet.Contains(ip)
}

func addrKind(ip net.IP) string {
	switch {
	case cgnatNet.Contains(ip) || radminNet.Contains(ip) || benchNet.Contains(ip):
		return "vpn"
	case ip.IsPrivate() || ip.IsLinkLocalUnicast():
		return "lan"
	default:
		return "public"
	}
}

// isZeroTierIface — сетевой адаптер ZeroTier («ZeroTier One [сеть]» на Windows, zt* на Linux).
func isZeroTierIface(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "zerotier") || (strings.HasPrefix(n, "zt") && len(n) > 4)
}

// routeOf — тип связи с ПК по его адресу: прямая локальная сеть, VPN, ZeroTier или интернет.
func routeOf(ip net.IP) string {
	if ip == nil {
		return "internet"
	}
	if ip.IsLoopback() {
		return "lan"
	}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.Contains(ip) {
				if isZeroTierIface(ifc.Name) {
					return "zt"
				}
				if addrKind(ip) == "vpn" {
					return "vpn"
				}
				return "lan"
			}
		}
	}
	switch addrKind(ip) { // за роутером, но не в наших подсетях
	case "lan":
		return "lan"
	case "vpn":
		return "vpn"
	}
	return "internet"
}

// plainGuard пропускает незашифрованный HTTP только из разрешённых сетей,
// остальным браузерам предлагает HTTPS.
func plainGuard(policy string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := net.ParseIP(clientIP(r))
		allowed := false
		switch policy {
		case "all":
			allowed = true
		case "off":
			allowed = ip != nil && ip.IsLoopback()
		default: // lan
			allowed = ip != nil && isLocalNet(ip)
		}
		if allowed {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && r.Header.Get("Upgrade") == "" {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			return
		}
		jsonErr(w, http.StatusForbidden, fmt.Sprintf("незашифрованное подключение из интернета запрещено — используйте https://%s", r.Host))
	})
}
