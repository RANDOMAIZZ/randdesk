// Package agent — служба на управляемом ПК: держит связь с хабом и выполняет команды.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"randdesk/internal/proto"
	"randdesk/internal/wol"
)

var errUnauthorized = errors.New("хаб отклонил устройство (удалено или ключ устарел) — привяжите ПК заново")

type Agent struct {
	cfg   *Config
	stats *statsCollector
	procs *procCache

	mu  sync.Mutex // защищает cfg.Hubs и cur
	cur string     // адрес хаба, через который установлено текущее соединение
}

func (a *Agent) hubs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.cfg.hubList()...)
}

func (a *Agent) current() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur != "" {
		return a.cur
	}
	if l := a.cfg.hubList(); len(l) > 0 {
		return l[0]
	}
	return ""
}

func New(cfg *Config) *Agent {
	return &Agent{cfg: cfg, stats: newStatsCollector(), procs: newProcCache()}
}

// conn — управляющее соединение с хабом с безопасной записью из нескольких горутин.
type conn struct {
	ws  *websocket.Conn
	wmu sync.Mutex
}

func (c *conn) send(m proto.Msg) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.ws.WriteJSON(m)
}

func (c *conn) sendData(typ string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.send(proto.Msg{Type: typ, Data: b})
}

// Run держит соединение с хабом до закрытия stop, переподключаясь при обрывах.
func (a *Agent) Run(stop <-chan struct{}) {
	backoff := time.Second
	for {
		start := time.Now()
		err := a.session(stop)
		select {
		case <-stop:
			return
		default:
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		if errors.Is(err, errUnauthorized) {
			backoff = time.Minute
		}
		log.Printf("нет связи с хабом: %v; повтор через %s", err, backoff)
		select {
		case <-stop:
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func wsURL(base, path string) string {
	u := base
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return strings.TrimRight(u, "/") + path
}

func (a *Agent) dialURL(base, path string) (*websocket.Conn, error) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+a.cfg.DeviceID+"."+a.cfg.Token)
	d := websocket.Dialer{
		NetDialContext:   netDialer.DialContext,
		TLSClientConfig:  tlsConfig(a.cfg.Fingerprint),
		HandshakeTimeout: 15 * time.Second,
		ReadBufferSize:   32 * 1024,
		WriteBufferSize:  32 * 1024,
	}
	ws, resp, err := d.Dial(wsURL(base, path), h)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, errUnauthorized
		}
		if errors.Is(err, errPin) {
			return nil, errPin
		}
		return nil, err
	}
	return ws, nil
}

// dial открывает поток через тот же адрес хаба, что и управляющее соединение.
func (a *Agent) dial(path string) (*websocket.Conn, error) {
	return a.dialURL(a.current(), path)
}

// connect перебирает адреса хаба по порядку (локальные — первыми) до первого успешного.
func (a *Agent) connect() (*websocket.Conn, error) {
	hubs := a.hubs()
	if len(hubs) == 0 {
		return nil, errors.New("не задан адрес хаба")
	}
	// Пробуем все адреса одновременно: вне дома не ждём таймаута каждого локального адреса.
	// Из успешных берём самый приоритетный (раньше в списке), но ждём его не дольше preferWait.
	type result struct {
		i   int
		ws  *websocket.Conn
		err error
	}
	const preferWait = 1500 * time.Millisecond
	ch := make(chan result, len(hubs))
	for i, u := range hubs {
		go func(i int, u string) {
			ws, err := a.dialURL(u, "/api/agent/ws")
			ch <- result{i, ws, err}
		}(i, u)
	}
	res := make([]*result, len(hubs))
	best := -1
	var deadline <-chan time.Time
	pending := len(hubs)
	// done: решено, если лучший найден и все более приоритетные завершились
	done := func() bool {
		if best < 0 {
			return false
		}
		for j := 0; j < best; j++ {
			if res[j] == nil {
				return false
			}
		}
		return true
	}
	for pending > 0 && !done() {
		select {
		case r := <-ch:
			pending--
			res[r.i] = &r
			if r.err == nil && (best < 0 || r.i < best) {
				if best >= 0 {
					res[best].ws.Close()
				}
				best = r.i
				if deadline == nil {
					deadline = time.After(preferWait)
				}
			} else if r.err == nil {
				r.ws.Close()
			}
		case <-deadline:
			pending = 0
		}
	}
	// опоздавшие соединения закрываем
	go func(left int) {
		for ; left > 0; left-- {
			if r := <-ch; r.err == nil {
				r.ws.Close()
			}
		}
	}(len(hubs) - countDone(res))
	if best >= 0 {
		a.mu.Lock()
		a.cur = hubs[best]
		a.mu.Unlock()
		return res[best].ws, nil
	}
	var errs []string
	for i, r := range res {
		if r == nil {
			continue
		}
		if errors.Is(r.err, errUnauthorized) {
			return nil, r.err
		}
		errs = append(errs, hubs[i]+": "+r.err.Error())
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

func countDone[T any](s []*T) int {
	n := 0
	for _, v := range s {
		if v != nil {
			n++
		}
	}
	return n
}

// updateHubs принимает от хаба актуальный список адресов (по уже проверенному соединению)
// и сохраняет его, чтобы ПК находил хаб и дома, и через интернет.
func (a *Agent) updateHubs(urls []string) {
	if a.cfg.Fingerprint == "" {
		return // без закреплённого отпечатка https-адреса самоподписанного хаба не проверить
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var list []string
	seen := map[string]bool{}
	add := func(u string) {
		u = normalizeHub(u, true)
		if u != "" && !seen[u] && len(list) < 8 {
			seen[u] = true
			list = append(list, u)
		}
	}
	for _, u := range urls {
		if strings.HasPrefix(u, "https://") {
			add(u)
		}
	}
	for _, u := range a.cfg.hubList() { // адреса, заданные вручную (VPN, домен), сохраняем
		add(u)
	}
	if a.cur != "" && !seen[a.cur] {
		list = append(list, a.cur)
	}
	if strings.Join(list, ",") == strings.Join(a.cfg.hubList(), ",") {
		return
	}
	a.cfg.setHubs(list)
	if a.cfg.path != "" {
		if err := saveConfig(a.cfg.path, a.cfg); err != nil {
			log.Printf("не удалось сохранить адреса хаба: %v", err)
			return
		}
	}
	log.Printf("адреса хаба обновлены: %s", strings.Join(list, ", "))
}

func (a *Agent) session(stop <-chan struct{}) error {
	ws, err := a.connect()
	if err != nil {
		return err
	}
	defer ws.Close()
	c := &conn{ws: ws}
	log.Printf("подключено к хабу %s", a.current())

	if err := c.sendData(proto.TypeHello, collectSysInfo()); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			ws.Close()
		case <-done:
		}
	}()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := c.sendData(proto.TypeStats, a.stats.collect()); err != nil {
					ws.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()

	ws.SetReadLimit(4 << 20)
	ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	ws.SetPingHandler(func(data string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second))
		return nil
	})
	for {
		var m proto.Msg
		if err := ws.ReadJSON(&m); err != nil {
			return err
		}
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		switch m.Type {
		case proto.TypeReq:
			go a.handleReq(c, m)
		case proto.TypeStreamOpen:
			var so proto.StreamOpen
			if json.Unmarshal(m.Data, &so) == nil {
				go a.handleStream(so)
			}
		case proto.TypeHubs:
			var hl proto.HubList
			if json.Unmarshal(m.Data, &hl) == nil {
				go a.updateHubs(hl.URLs)
			}
		}
	}
}

func (a *Agent) handleReq(c *conn, m proto.Msg) {
	resp := proto.Msg{Type: proto.TypeResp, ID: m.ID}
	func() {
		defer func() {
			if r := recover(); r != nil {
				resp.Error = fmt.Sprintf("внутренняя ошибка агента: %v", r)
			}
		}()
		res, err := a.dispatch(m.Method, m.Data)
		if err != nil {
			resp.Error = err.Error()
			return
		}
		b, err := json.Marshal(res)
		if err != nil {
			resp.Error = err.Error()
			return
		}
		resp.Data = b
	}()
	if err := c.send(resp); err != nil {
		log.Printf("не удалось отправить ответ: %v", err)
	}
}

func decode[T any](data json.RawMessage) (T, error) {
	var v T
	if len(data) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("неверные параметры: %w", err)
	}
	return v, nil
}

var ok = proto.OK{OK: true}

func (a *Agent) dispatch(method string, data json.RawMessage) (any, error) {
	switch method {
	case "procs":
		return a.procs.list()
	case "kill":
		r, err := decode[proto.KillReq](data)
		if err != nil {
			return nil, err
		}
		return ok, killProcess(r.PID, r.Tree)
	case "services":
		return listServices()
	case "service":
		r, err := decode[proto.ServiceReq](data)
		if err != nil {
			return nil, err
		}
		return ok, controlService(r.Name, r.Action)
	case "exec":
		r, err := decode[proto.ExecReq](data)
		if err != nil {
			return nil, err
		}
		log.Printf("выполнение команды (%s): %s", r.Shell, r.Command)
		return runExec(r), nil
	case "power":
		r, err := decode[proto.PowerReq](data)
		if err != nil {
			return nil, err
		}
		log.Printf("команда питания: %s", r.Action)
		return ok, power(r.Action)
	case "wol":
		r, err := decode[proto.WolReq](data)
		if err != nil {
			return nil, err
		}
		return ok, wol.Send(r.MAC)
	}
	return nil, fmt.Errorf("неизвестный метод %q", method)
}

// handleStream открывает отдельное WebSocket-соединение для потока (терминал, рабочий стол).
func (a *Agent) handleStream(so proto.StreamOpen) {
	if so.Kind == "desktop" {
		// рабочий стол обслуживает отдельный процесс в сеансе пользователя
		log.Printf("запрос удалённого рабочего стола")
		if err := startDesktopHelper(a.cfg.path, so.Session, a.current()); err != nil {
			log.Printf("рабочий стол: %v", err)
			if ws, derr := a.dial("/api/agent/stream?session=" + so.Session); derr == nil {
				b, _ := json.Marshal(proto.TermControl{Type: "error", Message: "не удалось запустить захват экрана: " + err.Error()})
				ws.WriteMessage(websocket.TextMessage, b)
				ws.Close()
			}
		}
		return
	}
	ws, err := a.dial("/api/agent/stream?session=" + so.Session)
	if err != nil {
		log.Printf("поток %s: %v", so.Kind, err)
		return
	}
	defer ws.Close()
	switch so.Kind {
	case "terminal":
		log.Printf("открыт терминал (%s)", so.Shell)
		runTerminal(ws, so)
		log.Printf("терминал закрыт")
	default:
		b, _ := json.Marshal(proto.TermControl{Type: "error", Message: "неизвестный тип потока"})
		ws.WriteMessage(websocket.TextMessage, b)
	}
}
