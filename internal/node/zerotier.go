package node

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"randdesk/internal/rendezvous"
	"randdesk/internal/ui"
	"randdesk/internal/zt"
)

// Сеть через интернет на ZeroTier — полностью бесплатно и без аккаунта: сетью управляет
// контроллер, встроенный в ZeroTier One на главном ПК. Номер сети главный ПК оставляет
// в зашифрованном канале (ntfy.sh); обычные ПК находят его по логину и паролю, вступают
// в сеть и присылают заявку, а главный ПК пускает их. Публичные серверы ZeroTier помогают
// ПК найти друг друга и пройти через NAT.

// ZTStatus — состояние сети через интернет для страницы настроек.
type ZTStatus struct {
	Enabled   bool      `json:"enabled"`
	Installed bool      `json:"installed"`
	Network   string    `json:"network"`
	Node      string    `json:"node"`
	Status    string    `json:"status"` // OK — этот ПК в сети
	Addresses []string  `json:"addresses"`
	Members   int       `json:"members"` // участников (главный ПК)
	Step      string    `json:"step"`
	Error     string    `json:"error,omitempty"`
	Updated   time.Time `json:"updated"`
}

type ztManager struct {
	n    *node
	mu   sync.Mutex
	st   ZTStatus
	kick chan struct{}
}

func newZT(n *node) *ztManager { return &ztManager{n: n, kick: make(chan struct{}, 1)} }

func (z *ztManager) status() ZTStatus {
	z.mu.Lock()
	defer z.mu.Unlock()
	s := z.st
	s.Installed = zt.Installed()
	return s
}

func (z *ztManager) set(fn func(s *ZTStatus)) {
	z.mu.Lock()
	fn(&z.st)
	z.st.Updated = time.Now()
	z.mu.Unlock()
}

func (z *ztManager) refresh() {
	select {
	case z.kick <- struct{}{}:
	default:
	}
}

func (z *ztManager) wait(stop <-chan struct{}, d time.Duration) bool {
	select {
	case <-stop:
		return false
	case <-z.kick:
	case <-time.After(d):
	}
	return true
}

// local ставит ZeroTier при необходимости и возвращает клиент службы и ID этого узла.
func (z *ztManager) local(ctx context.Context) (*zt.Local, string, error) {
	if !ui.IsElevated() {
		return nil, "", fmt.Errorf("RandDesk запущен без прав администратора и не может поставить ZeroTier — установите RandDesk службой (двойной щелчок по randdesk.exe → «Да»)")
	}
	if !zt.Installed() {
		z.set(func(s *ZTStatus) { s.Step = "Установка ZeroTier…" })
	}
	if err := zt.EnsureInstalled(ctx); err != nil {
		return nil, "", err
	}
	l, err := zt.OpenLocal()
	if err != nil {
		return nil, "", err
	}
	var st zt.Status
	for range 30 { // служба могла только что запуститься — ждём, пока ответит
		if st, err = l.Status(ctx); err == nil && st.Address != "" {
			return l, st.Address, nil
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, "", fmt.Errorf("служба ZeroTier не отвечает: %v", err)
}

// ---------- главный ПК ----------

func (z *ztManager) runAdmin(stop <-chan struct{}) {
	var lastPublish time.Time
	var subCancel context.CancelFunc
	defer func() {
		if subCancel != nil {
			subCancel()
		}
	}()
	for {
		cfg := z.n.st.get()
		if cfg.ZTDisabled {
			if subCancel != nil {
				subCancel()
				subCancel = nil
			}
			z.set(func(s *ZTStatus) { *s = ZTStatus{} })
			if !z.wait(stop, time.Hour) {
				return
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		err := z.adminStep(ctx, cfg, &lastPublish, &subCancel, stop)
		cancel()
		retry := 5 * time.Minute
		if err != nil {
			log.Printf("ZeroTier: %v", err)
			z.set(func(s *ZTStatus) { s.Enabled, s.Error, s.Step = true, err.Error(), "" })
			retry = 2 * time.Minute
		}
		if !z.wait(stop, retry) {
			return
		}
	}
}

func (z *ztManager) adminStep(ctx context.Context, cfg Config, lastPublish *time.Time, subCancel *context.CancelFunc, stop <-chan struct{}) error {
	z.set(func(s *ZTStatus) { s.Enabled = true })
	l, node, err := z.local(ctx)
	if err != nil {
		return err
	}
	// своя сеть: её ID начинается с адреса узла-контроллера
	nwid := cfg.ZTNetwork
	if !strings.HasPrefix(nwid, node) {
		nwid = ""
		if ids, err := l.ControllerNetworks(ctx); err == nil {
			for _, id := range ids {
				if strings.HasPrefix(id, node) {
					nwid = id
					break
				}
			}
		}
		if nwid == "" {
			z.set(func(s *ZTStatus) { s.Step = "Создание сети…" })
			b := make([]byte, 1)
			rand.Read(b)
			if nwid, err = l.CreateNetwork(ctx, node, "RandDesk "+cfg.Account.Login, 10+b[0]%200); err != nil {
				return fmt.Errorf("создание сети: %w", err)
			}
			log.Printf("ZeroTier: создана сеть %s", nwid)
		}
		if err := z.n.st.set(func(c *Config) { c.ZTNetwork = nwid }); err != nil {
			return err
		}
		*lastPublish = time.Time{}
		if *subCancel != nil {
			(*subCancel)()
			*subCancel = nil
		}
	}
	if err := l.AuthorizeMember(ctx, nwid, node); err != nil {
		return err
	}
	if err := l.Join(ctx, nwid); err != nil {
		return err
	}
	ch, err := rendezvous.New(cfg.Account, cfg.ZTServer)
	if err != nil {
		return err
	}
	if *subCancel == nil {
		sctx, cancel := context.WithCancel(context.Background())
		*subCancel = cancel
		go z.listenJoins(sctx, stop, ch, l, nwid)
	}
	if time.Since(*lastPublish) > time.Hour {
		if err := ch.Publish(ctx, rendezvous.Msg{Type: "net", Network: nwid, Host: hostname()}); err != nil {
			return fmt.Errorf("точка встречи: %w", err)
		}
		*lastPublish = time.Now()
	}
	net, _ := l.Network(ctx, nwid)
	members, _ := l.ControllerMembers(ctx, nwid)
	z.set(func(s *ZTStatus) {
		s.Enabled, s.Network, s.Node, s.Status, s.Addresses = true, nwid, node, net.Status, net.AssignedAddresses
		s.Members, s.Error, s.Step = members, "", ""
	})
	return nil
}

// listenJoins пускает в сеть ПК, приславшие заявку через зашифрованную точку встречи
// (заявку может составить только тот, кто знает логин и пароль).
func (z *ztManager) listenJoins(ctx context.Context, stop <-chan struct{}, ch *rendezvous.Channel, l *zt.Local, nwid string) {
	seen := map[string]time.Time{}
	handle := func(m rendezvous.Msg) {
		if m.Type != "join" || !zt.ValidNodeID(m.Node) || (m.Network != "" && m.Network != nwid) {
			return
		}
		if t, ok := seen[m.Node]; ok && time.Since(t) < 2*time.Minute {
			return
		}
		seen[m.Node] = time.Now()
		actx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := l.AuthorizeMember(actx, nwid, m.Node); err != nil {
			log.Printf("ZeroTier: не удалось пустить %s: %v", m.Host, err)
			delete(seen, m.Node)
			return
		}
		log.Printf("ZeroTier: в сеть пущен ПК %s (%s)", m.Host, m.Node)
		z.refresh()
	}
	for {
		// заявки, пришедшие, пока главный ПК был выключен
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if list, err := ch.Poll(pctx); err == nil {
			for _, m := range list {
				handle(m)
			}
		}
		cancel()
		ch.Subscribe(ctx, handle)
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-time.After(10 * time.Second): // обрыв подписки — переподключаемся
		}
	}
}

// ---------- обычный ПК ----------

func (z *ztManager) runMember(stop <-chan struct{}) {
	var lastJoinReq time.Time
	for {
		cfg := z.n.st.get()
		wait := time.Minute
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		err := z.memberStep(ctx, cfg, &lastJoinReq)
		cancel()
		if err != nil {
			log.Printf("ZeroTier: %v", err)
			z.set(func(s *ZTStatus) { s.Error, s.Step = err.Error(), "" })
			wait = 2 * time.Minute
		}
		if st := z.status(); st.Status == "OK" {
			wait = 10 * time.Minute
		}
		if !z.wait(stop, wait) {
			return
		}
	}
}

func (z *ztManager) memberStep(ctx context.Context, cfg Config, lastJoinReq *time.Time) error {
	ch, err := rendezvous.New(cfg.Account, cfg.ZTServer)
	if err != nil {
		return err
	}
	// главный ПК периодически сообщает номер своей сети; берём самый свежий
	nwid := cfg.ZTNetwork
	if list, err := ch.Poll(ctx); err == nil {
		var newest int64
		for _, m := range list {
			if m.Type == "net" && zt.ValidNetworkID(m.Network) && m.TS >= newest {
				nwid, newest = m.Network, m.TS
			}
		}
	} else if nwid == "" {
		return fmt.Errorf("точка встречи: %w", err)
	}
	if nwid == "" {
		z.set(func(s *ZTStatus) {
			s.Step = "Главный ПК ещё не включил сеть через интернет"
		})
		return nil
	}
	l, node, err := z.local(ctx)
	if err != nil {
		return err
	}
	if nwid != cfg.ZTNetwork {
		if cfg.ZTNetwork != "" {
			l.Leave(ctx, cfg.ZTNetwork) // главный ПК сменил сеть — старая не нужна
		}
		z.n.st.set(func(c *Config) { c.ZTNetwork = nwid })
		*lastJoinReq = time.Time{}
		log.Printf("ZeroTier: сеть главного ПК %s", nwid)
	}
	if err := l.Join(ctx, nwid); err != nil {
		return err
	}
	net, _ := l.Network(ctx, nwid)
	if net.Status != "OK" && time.Since(*lastJoinReq) > 2*time.Minute {
		if err := ch.Publish(ctx, rendezvous.Msg{Type: "join", Network: nwid, Node: node, Host: hostname(), Machine: cfg.Machine}); err != nil {
			return fmt.Errorf("точка встречи: %w", err)
		}
		*lastJoinReq = time.Now()
		log.Printf("ZeroTier: отправлена заявка на вступление (%s)", node)
	}
	step := ""
	if net.Status != "OK" {
		step = "Ждём, пока главный ПК пустит этот ПК в сеть…"
	}
	z.set(func(s *ZTStatus) {
		s.Enabled, s.Network, s.Node, s.Status, s.Addresses, s.Error, s.Step = true, nwid, node, net.Status, net.AssignedAddresses, "", step
	})
	return nil
}
