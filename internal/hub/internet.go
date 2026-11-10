package hub

import (
	"context"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"randdesk/internal/upnp"
)

// InternetStatus — состояние доступа к хабу из интернета.
type InternetStatus struct {
	UPnP       bool      `json:"upnp"`        // включён ли автоматический проброс
	Active     bool      `json:"active"`      // проброс сейчас действует
	ExternalIP string    `json:"external_ip"` // внешний адрес роутера
	LocalIP    string    `json:"local_ip"`
	Gateway    string    `json:"gateway"`
	CGNAT      bool      `json:"cgnat"` // внешний адрес роутера «серый» — проброс не поможет
	Error      string    `json:"error,omitempty"`
	Updated    time.Time `json:"updated"`
	PublicHost string    `json:"public_host"`
	Port       int       `json:"port"`
}

type internet struct {
	store    *Store
	port     int
	onChange func()
	kick     chan struct{}
	stop     chan struct{}
	stopped  chan struct{}

	mu  sync.Mutex
	st  InternetStatus
	igd *upnp.IGD
}

const mappingLease = 3600 // секунд; продлеваем каждые 20 минут

func newInternet(store *Store, port int, onChange func()) *internet {
	return &internet{
		store: store, port: port, onChange: onChange,
		kick: make(chan struct{}, 1), stop: make(chan struct{}), stopped: make(chan struct{}),
	}
}

func (n *internet) status() InternetStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.st
	set := n.store.Settings()
	st.UPnP, st.PublicHost, st.Port = set.UPnP, set.PublicHost, n.port
	return st
}

// refresh — применить изменившиеся настройки немедленно.
func (n *internet) refresh() {
	select {
	case n.kick <- struct{}{}:
	default:
	}
}

func (n *internet) run() {
	defer close(n.stopped)
	for {
		wait := n.step()
		select {
		case <-n.kick:
		case <-time.After(wait):
		case <-n.stop:
			n.unmap()
			return
		}
	}
}

func (n *internet) step() time.Duration {
	set := n.store.Settings()
	if !set.UPnP {
		n.unmap()
		n.mu.Lock()
		changed := n.st.Active || n.st.ExternalIP != ""
		n.st = InternetStatus{Updated: time.Now()}
		n.mu.Unlock()
		if changed && n.onChange != nil {
			n.onChange()
		}
		return time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st := InternetStatus{Updated: time.Now()}
	n.mu.Lock()
	igd := n.igd
	prevIP, prevActive := n.st.ExternalIP, n.st.Active
	n.mu.Unlock()

	var err error
	if igd == nil {
		igd, err = upnp.Discover(ctx)
	}
	if err == nil {
		err = igd.AddMapping(ctx, n.port, n.port, "RandDesk Hub", mappingLease)
		if err != nil {
			// роутер мог смениться или перезагрузиться — попробуем найти заново
			if g2, derr := upnp.Discover(ctx); derr == nil {
				igd = g2
				err = igd.AddMapping(ctx, n.port, n.port, "RandDesk Hub", mappingLease)
			}
		}
	}
	retry := 20 * time.Minute
	if err != nil {
		st.Error = err.Error()
		igd = nil
		retry = 2 * time.Minute
	} else {
		st.Active = true
		st.LocalIP, st.Gateway = igd.LocalIP, igd.Gateway()
		if ip, e := igd.ExternalIP(ctx); e == nil {
			st.ExternalIP = ip
			if p := net.ParseIP(ip); p != nil && (p.IsPrivate() || cgnatNet.Contains(p)) {
				st.CGNAT = true
			}
		} else {
			st.Error = e.Error()
		}
	}
	n.mu.Lock()
	n.igd = igd
	n.st = st
	n.mu.Unlock()
	if prevIP != st.ExternalIP || prevActive != st.Active {
		if st.Active {
			log.Printf("доступ из интернета: порт %d проброшен на роутере %s, внешний адрес %s", n.port, st.Gateway, st.ExternalIP)
		} else {
			log.Printf("доступ из интернета: %s", st.Error)
		}
		if n.onChange != nil {
			n.onChange()
		}
	}
	return retry
}

func (n *internet) unmap() {
	n.mu.Lock()
	igd := n.igd
	n.igd = nil
	n.mu.Unlock()
	if igd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := igd.DeleteMapping(ctx, n.port); err == nil {
		log.Printf("проброс порта %d на роутере удалён", n.port)
	}
}

// shutdown удаляет проброс при остановке хаба.
func (n *internet) shutdown() {
	select {
	case <-n.stop:
	default:
		close(n.stop)
	}
	select {
	case <-n.stopped:
	case <-time.After(6 * time.Second):
	}
}

// publicURLs — адреса хаба в интернете.
func (n *internet) publicURLs() []string {
	if n == nil {
		return nil
	}
	st := n.status()
	var out []string
	if h := normalizeHost(st.PublicHost, n.port); h != "" {
		out = append(out, "https://"+h)
	}
	if st.Active && st.ExternalIP != "" && !st.CGNAT {
		u := "https://" + net.JoinHostPort(st.ExternalIP, strconv.Itoa(n.port))
		if len(out) == 0 || out[0] != u {
			out = append(out, u)
		}
	}
	return out
}

// normalizeHost приводит «host», «host:port» или «https://host:port/» к виду host:port.
func normalizeHost(h string, port int) string {
	h = strings.TrimSpace(h)
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	h = strings.TrimRight(h, "/")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(h); err != nil {
		h = net.JoinHostPort(strings.Trim(h, "[]"), strconv.Itoa(port))
	}
	return h
}
