// Package agent — служба на управляемом ПК: держит связь с хабом и выполняет команды.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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

	// Session — держать в сеансе пользователя помощника (буфер обмена, блокировка ввода).
	Session bool
	online  atomic.Bool

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
	if a.Session {
		go a.superviseSessionHelper(stop)
	}
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

// connectPath подключается к пути на хабе, перебирая адреса по порядку.
func (a *Agent) connectPath(path string) (*websocket.Conn, error) {
	var last error = errors.New("не задан адрес хаба")
	for _, u := range a.hubs() {
		ws, err := a.dialURL(u, path)
		if err == nil {
			return ws, nil
		}
		last = err
	}
	return nil, last
}

// Online — есть ли сейчас связь с хабом.
func (a *Agent) Online() bool { return a.online.Load() }

// Current — адрес хаба, через который идёт связь.
func (a *Agent) Current() string { return a.current() }

// Config — конфигурация агента (адреса, отпечаток, токен устройства).
func (a *Agent) Config() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := *a.cfg
	c.Hubs = append([]string(nil), c.Hubs...)
	return c
}

// AddHubs добавляет найденные адреса хаба в начало списка (локальная сеть важнее).
func (a *Agent) AddHubs(urls ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	list := a.cfg.hubList()
	changed := false
	for i := len(urls) - 1; i >= 0; i-- {
		u := normalizeHub(urls[i], true)
		found := false
		for _, x := range list {
			if x == u {
				found = true
			}
		}
		if !found {
			list = append([]string{u}, list...)
			changed = true
		}
	}
	if !changed {
		return
	}
	if len(list) > 8 {
		list = list[:8]
	}
	a.cfg.setHubs(list)
	if a.cfg.path != "" {
		saveConfig(a.cfg.path, a.cfg)
	}
}

// dial открывает поток через тот же адрес хаба, что и управляющее соединение.
func (a *Agent) dial(path string) (*websocket.Conn, error) {
	return a.dialURL(a.current(), path)
}

// connect выбирает адрес хаба и открывает управляющее соединение.
// Доступность всех адресов проверяется одновременно (только TCP), чтобы вне дома не ждать
// таймаута каждого локального адреса. Соединение с хабом открывается одно — по самому
// приоритетному доступному адресу: несколько соединений одного ПК хаб считал бы заменой друг друга.
func (a *Agent) connect() (*websocket.Conn, error) {
	hubs := a.hubs()
	if len(hubs) == 0 {
		return nil, errors.New("не задан адрес хаба")
	}
	order := reachableOrder(hubs)
	var errs []string
	for _, i := range order {
		u := hubs[i]
		ws, err := a.dialURL(u, "/api/agent/ws")
		if err == nil {
			a.mu.Lock()
			a.cur = u
			a.mu.Unlock()
			return ws, nil
		}
		if errors.Is(err, errUnauthorized) {
			return nil, err
		}
		errs = append(errs, u+": "+err.Error())
	}
	if len(errs) == 0 {
		errs = append(errs, "ни один адрес хаба не отвечает: "+strings.Join(hubs, ", "))
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

// reachableOrder возвращает индексы адресов, до которых удалось установить TCP-соединение,
// по порядку приоритета. Более приоритетные ждём не дольше 1,5 с после первого ответившего.
func reachableOrder(hubs []string) []int {
	const preferWait = 1500 * time.Millisecond
	ch := make(chan int, len(hubs))
	for i, u := range hubs {
		go func(i int, u string) {
			host := u
			if p, err := url.Parse(u); err == nil && p.Host != "" {
				host = p.Host
				if p.Port() == "" {
					if p.Scheme == "https" {
						host += ":443"
					} else {
						host += ":80"
					}
				}
			}
			c, err := netDialer.Dial("tcp", host)
			if err != nil {
				ch <- -1 - i
				return
			}
			c.Close()
			ch <- i
		}(i, u)
	}
	ok := make([]bool, len(hubs))
	done := make([]bool, len(hubs))
	var deadline <-chan time.Time
	for left := len(hubs); left > 0; {
		select {
		case r := <-ch:
			left--
			if r >= 0 {
				ok[r], done[r] = true, true
				if deadline == nil {
					deadline = time.After(preferWait)
				}
			} else {
				done[-1-r] = true
			}
			// лучший доступный найден, а все более приоритетные уже ответили — дальше не ждём
			best := -1
			for i := range ok {
				if ok[i] {
					best = i
					break
				}
			}
			if best >= 0 {
				all := true
				for i := 0; i < best; i++ {
					all = all && done[i]
				}
				if all {
					left = 0
				}
			}
		case <-deadline:
			left = 0
		}
	}
	var order []int
	for i := range ok {
		if ok[i] {
			order = append(order, i)
		}
	}
	return order
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
	a.online.Store(true)
	defer a.online.Store(false)
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
	case "gamepad":
		r, err := decode[proto.GamepadReq](data)
		if err != nil {
			return nil, err
		}
		return ok, setGamepads(r.Disable)
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
