// Package rendezvous — «точка встречи» ПК одной учётной записи через публичный сервис
// сообщений ntfy.sh. Имя канала и содержимое сообщений получаются из логина и пароля
// (AES-GCM): сервис видит только случайное имя и зашифрованные данные, а подделать или
// прочитать сообщения без пароля нельзя.
package rendezvous

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"randsec/internal/pair"
)

// DefaultServer — общедоступный сервер ntfy.
const DefaultServer = "https://ntfy.sh"

// Msg — сообщение точки встречи.
type Msg struct {
	Type    string `json:"t"`              // net — главный ПК сообщает сеть; join — ПК просит пустить
	Network string `json:"nwid,omitempty"` // ID сети ZeroTier
	Node    string `json:"node,omitempty"` // ID узла ZeroTier вступающего ПК
	Host    string `json:"host,omitempty"`
	Machine string `json:"machine,omitempty"`
	// адреса хаба главного ПК в сети ZeroTier (https://10.147.…:8420) — по ним подключаются
	// устройства, которые вступили в сеть сами, через приложение ZeroTier (Android)
	Addrs []string `json:"addrs,omitempty"`
	TS    int64    `json:"ts"`
}

// Channel — зашифрованный канал учётной записи.
type Channel struct {
	server string
	topic  string
	aead   cipher.AEAD
	hc     *http.Client
}

func New(acc pair.Account, server string) (*Channel, error) {
	if !acc.Valid() {
		return nil, errors.New("учётная запись не настроена")
	}
	if server == "" {
		server = DefaultServer
	}
	block, err := aes.NewCipher(acc.Subkey("rendezvous-key"))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Channel{
		server: strings.TrimRight(server, "/"),
		topic:  "randdesk-" + hex.EncodeToString(acc.Subkey("rendezvous-topic"))[:32],
		aead:   aead,
		hc:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *Channel) seal(m Msg) string {
	b, _ := json.Marshal(m)
	nonce := make([]byte, c.aead.NonceSize())
	rand.Read(nonce)
	return base64.RawURLEncoding.EncodeToString(c.aead.Seal(nonce, nonce, b, []byte(c.topic)))
}

func (c *Channel) open(s string) (Msg, bool) {
	var m Msg
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	ns := c.aead.NonceSize()
	if err != nil || len(b) < ns {
		return m, false
	}
	plain, err := c.aead.Open(nil, b[:ns], b[ns:], []byte(c.topic))
	if err != nil || json.Unmarshal(plain, &m) != nil {
		return m, false
	}
	if d := time.Since(time.Unix(m.TS, 0)); d > 24*time.Hour || d < -time.Hour {
		return m, false // устаревшее или повторно отправленное сообщение
	}
	return m, true
}

// Publish отправляет сообщение в канал.
func (c *Channel) Publish(ctx context.Context, m Msg) error {
	m.TS = time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+"/"+c.topic, strings.NewReader(c.seal(m)))
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy: %s", resp.Status)
	}
	return nil
}

type event struct {
	Event   string `json:"event"`
	Message string `json:"message"`
}

func (c *Channel) read(r io.Reader, fn func(Msg)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Event != "message" {
			continue
		}
		if m, ok := c.open(e.Message); ok {
			fn(m)
		}
	}
	return sc.Err()
}

// Poll возвращает все сообщения, которые хранит сервер (на ntfy.sh — около 12 часов).
func (c *Channel) Poll(ctx context.Context) ([]Msg, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+"/"+c.topic+"/json?poll=1&since=all", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ntfy: %s", resp.Status)
	}
	var out []Msg
	err = c.read(resp.Body, func(m Msg) { out = append(out, m) })
	return out, err
}

// Subscribe получает новые сообщения в реальном времени, пока не отменён ctx.
func (c *Channel) Subscribe(ctx context.Context, fn func(Msg)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+"/"+c.topic+"/json", nil)
	if err != nil {
		return err
	}
	hc := &http.Client{} // без общего таймаута: соединение долгое
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ntfy: %s", resp.Status)
	}
	return c.read(resp.Body, fn)
}
