// Package zt — автоматическая работа с ZeroTier: установка, вступление в сеть
// (локальная служба ZeroTier One) и собственный контроллер сети на главном ПК.
package zt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 20 * time.Second}

// ---------- локальная служба ZeroTier One (127.0.0.1:9993) ----------

// Local — клиент локального API службы ZeroTier.
type Local struct{ token string }

// Status — сведения об этом узле ZeroTier.
type Status struct {
	Address string `json:"address"` // ID узла (10 шестнадцатеричных символов)
	Online  bool   `json:"online"`
	Version string `json:"version"`
}

// Network — состояние членства в сети.
type Network struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Status            string   `json:"status"` // OK | REQUESTING_CONFIGURATION | ACCESS_DENIED | NOT_FOUND ...
	AssignedAddresses []string `json:"assignedAddresses"`
	PortDeviceName    string   `json:"portDeviceName"`
}

// OpenLocal читает ключ доступа к локальной службе (нужны права SYSTEM/root).
func OpenLocal() (*Local, error) {
	b, err := os.ReadFile(authTokenPath())
	if err != nil {
		return nil, fmt.Errorf("ZeroTier не установлен или не запущен: %w", err)
	}
	return &Local{token: strings.TrimSpace(string(b))}, nil
}

func (l *Local) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:9993"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("X-ZT1-Auth", l.token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ZeroTier: %s %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (l *Local) Status(ctx context.Context) (Status, error) {
	var s Status
	err := l.do(ctx, http.MethodGet, "/status", nil, &s)
	return s, err
}

// Join вступает в сеть (повторный вызов безопасен).
func (l *Local) Join(ctx context.Context, nwid string) error {
	return l.do(ctx, http.MethodPost, "/network/"+nwid, map[string]any{}, nil)
}

func (l *Local) Network(ctx context.Context, nwid string) (Network, error) {
	var n Network
	err := l.do(ctx, http.MethodGet, "/network/"+nwid, nil, &n)
	return n, err
}

// ValidNodeID — 10 шестнадцатеричных символов.
func ValidNodeID(s string) bool {
	if len(s) != 10 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ValidNetworkID — 16 шестнадцатеричных символов.
func ValidNetworkID(s string) bool {
	if len(s) != 16 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ---------- собственный контроллер сети (встроен в ZeroTier One) ----------
//
// Сетью управляет сам главный ПК: аккаунт ZeroTier, ключи API и платные тарифы не нужны,
// ограничений на число устройств нет. Публичные серверы ZeroTier по-прежнему помогают ПК
// найти друг друга и пройти через NAT.

// CreateNetwork создаёт на этом узле закрытую сеть с автоматической раздачей адресов.
func (l *Local) CreateNetwork(ctx context.Context, node, name string, subnet byte) (string, error) {
	pool := fmt.Sprintf("10.147.%d", subnet)
	body := map[string]any{
		"name":              name,
		"private":           true,
		"enableBroadcast":   true,
		"v4AssignMode":      map[string]bool{"zt": true},
		"ipAssignmentPools": []map[string]string{{"ipRangeStart": pool + ".1", "ipRangeEnd": pool + ".254"}},
		"routes":            []map[string]any{{"target": pool + ".0/24", "via": nil}},
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := l.do(ctx, http.MethodPost, "/controller/network/"+node+"______", body, &created); err != nil {
		return "", err
	}
	if !ValidNetworkID(created.ID) {
		return "", errors.New("контроллер ZeroTier не вернул ID сети")
	}
	return created.ID, nil
}

// ControllerNetworks — сети, которыми управляет этот узел.
func (l *Local) ControllerNetworks(ctx context.Context) ([]string, error) {
	var ids []string
	err := l.do(ctx, http.MethodGet, "/controller/network", nil, &ids)
	return ids, err
}

// ControllerMembers — участники сети этого контроллера.
func (l *Local) ControllerMembers(ctx context.Context, nwid string) (int, error) {
	var m map[string]any
	if err := l.do(ctx, http.MethodGet, "/controller/network/"+nwid+"/member", nil, &m); err != nil {
		return 0, err
	}
	return len(m), nil
}

// AuthorizeMember пускает узел в сеть этого контроллера.
func (l *Local) AuthorizeMember(ctx context.Context, nwid, node string) error {
	return l.do(ctx, http.MethodPost, "/controller/network/"+nwid+"/member/"+node, map[string]any{"authorized": true}, nil)
}

// Leave выходит из сети.
func (l *Local) Leave(ctx context.Context, nwid string) error {
	return l.do(ctx, http.MethodDelete, "/network/"+nwid, nil, nil)
}
