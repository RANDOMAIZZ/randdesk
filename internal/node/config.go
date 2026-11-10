// Package node — RANDSEC «одной кнопкой»: служба на каждом ПК.
//
// Главный (админский) ПК держит панель администратора (хаб). Обычные ПК, настроенные тем же
// логином и паролем, сами находят главный ПК в сети, подключаются к нему и показывают
// локальную страницу с устройствами, которыми администратор разрешил им управлять.
package node

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"randsec/internal/agent"
	"randsec/internal/pair"
)

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Config — node.json: роль ПК и секреты учётной записи.
type Config struct {
	Role    string       `json:"role"` // "" — ещё не настроен
	Account pair.Account `json:"account"`
	Machine string       `json:"machine"` // постоянный идентификатор этого ПК

	// Сеть через интернет (ZeroTier): ID сети и выключатель. На главном ПК сеть своя,
	// управляется встроенным контроллером ZeroTier — аккаунт и ключи не нужны.
	ZTNetwork  string `json:"zt_network,omitempty"`
	ZTDisabled bool   `json:"zt_disabled,omitempty"`
	ZTServer   string `json:"zt_server,omitempty"` // свой сервер ntfy (по умолчанию ntfy.sh)
}

type store struct {
	dir string
	mu  sync.Mutex
	cfg Config
}

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &store{dir: dir}
	b, err := os.ReadFile(s.path())
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, err
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if s.cfg.Machine == "" {
		b := make([]byte, 16)
		rand.Read(b)
		s.cfg.Machine = hex.EncodeToString(b)
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *store) path() string     { return filepath.Join(s.dir, "node.json") }
func (s *store) hubDir() string   { return filepath.Join(s.dir, "hub") }
func (s *store) peerPath() string { return filepath.Join(s.dir, "link.json") }

func (s *store) get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *store) set(fn func(c *Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	return s.saveLocked()
}

func (s *store) saveLocked() error {
	b, _ := json.MarshalIndent(s.cfg, "", "  ")
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path()); err != nil {
		return err
	}
	agent.ProtectFile(s.path())
	return nil
}
