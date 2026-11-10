package node

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Поиск главного ПК в локальной сети (и в VPN вроде Radmin, где работает широковещание).
// Обычный ПК рассылает «кто здесь главный для группы G?», главный ПК с той же группой
// отвечает своим портом. Группа — открытый хэш из логина и пароля; сам пароль не передаётся.

type beacon struct {
	RD    int    `json:"rd"`
	Type  string `json:"t"` // probe | here | mismatch
	Group string `json:"g"`
	Login string `json:"l,omitempty"` // хэш логина: чтобы подсказать «пароль не совпадает»
	FP    string `json:"fp,omitempty"`
	Port  int    `json:"p,omitempty"`
	Name  string `json:"n,omitempty"`
	// Addrs — настоящие адреса главного ПК: если между подсетями стоит роутер с NAT,
	// ответ приходит с адреса роутера, а подключаться нужно к одному из этих.
	Addrs []string `json:"a,omitempty"`
}

// broadcastAddrs — широковещательные адреса всех сетей этого ПК.
func broadcastAddrs(port int) []*net.UDPAddr {
	out := []*net.UDPAddr{
		{IP: net.IPv4bcast, Port: port},
		{IP: net.IPv4(127, 0, 0, 1), Port: port},
	}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || len(ipn.Mask) < 4 {
				continue
			}
			ip, m := ipn.IP.To4(), ipn.Mask[len(ipn.Mask)-4:]
			b := make(net.IP, 4)
			for i := range 4 {
				b[i] = ip[i] | ^m[i]
			}
			out = append(out, &net.UDPAddr{IP: b, Port: port})
		}
	}
	return out
}

// announcer — главный ПК: отвечает обычным ПК своей группы, где его искать.
func announcer(stop <-chan struct{}, udpPort int, me func() beacon) {
	var conn *net.UDPConn
	for warned := false; ; {
		var err error
		if conn, err = net.ListenUDP("udp4", &net.UDPAddr{Port: udpPort}); err == nil {
			break
		}
		if !warned {
			log.Printf("поиск ПК: порт UDP %d занят (%v), повторяю попытки", udpPort, err)
			warned = true
		}
		select {
		case <-stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
	defer conn.Close()
	go func() { <-stop; conn.Close() }()
	send := func(to *net.UDPAddr) {
		b, _ := json.Marshal(me())
		conn.WriteToUDP(b, to)
	}
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var b beacon
		if json.Unmarshal(buf[:n], &b) != nil || b.RD != 1 || b.Type != "probe" {
			continue
		}
		mb := me()
		switch {
		case mb.Group != "" && b.Group == mb.Group:
			send(from)
		case b.Login != "" && b.Login == mb.Login:
			// логин тот же, а пароль другой — подсказываем, но ничего не раскрываем
			x, _ := json.Marshal(beacon{RD: 1, Type: "mismatch", Name: mb.Name})
			conn.WriteToUDP(x, from)
		}
	}
}

// seeker — обычный ПК: ищет главный ПК своей группы; found вызывается с адресом хаба.
func seeker(stop <-chan struct{}, udpPort int, group, login string, fast func() bool, found func(urls []string, fp, name string), mismatch func(name string)) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		log.Printf("поиск ПК: %v", err)
		return
	}
	defer conn.Close()
	go func() { <-stop; conn.Close() }()
	handle := func(conn *net.UDPConn) {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var b beacon
			if json.Unmarshal(buf[:n], &b) != nil || b.RD != 1 {
				continue
			}
			if b.Type == "mismatch" {
				mismatch(b.Name)
				continue
			}
			if b.Type != "here" || b.Group != group || b.Port == 0 {
				continue
			}
			urls := []string{"https://" + net.JoinHostPort(from.IP.String(), strconv.Itoa(b.Port))}
			for _, a := range b.Addrs {
				if strings.HasPrefix(a, "https://") && a != urls[0] && len(urls) < 10 {
					urls = append(urls, a)
				}
			}
			found(urls, b.FP, b.Name)
		}
	}
	go handle(conn)
	probe, _ := json.Marshal(beacon{RD: 1, Type: "probe", Group: group, Login: login})
	var sweeping atomic.Bool
	var lastSweep time.Time
	for round := 0; ; round++ {
		for _, a := range broadcastAddrs(udpPort) {
			conn.WriteToUDP(probe, a)
		}
		// широковещание не проходит между подсетями (192.168.10.x ↔ 192.168.40.x):
		// если главный ПК не ответил, опрашиваем соседние подсети своей частной сети
		if fast() && round >= 3 && !sweeping.Load() && time.Since(lastSweep) > 5*time.Minute {
			lastSweep = time.Now()
			sweeping.Store(true)
			go func() {
				defer sweeping.Store(false)
				sweep(stop, conn, probe, udpPort, fast)
			}()
		}
		wait := 30 * time.Second
		if fast() {
			wait = 3 * time.Second
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// sweepNets — подсети /24 для поиска главного ПК: все /24 в пределах /16 каждой частной сети
// этого ПК, ближайшие к своей — первыми.
func sweepNets() [][3]byte {
	type pref struct{ a, b, c byte }
	var own []pref
	seen16 := map[[2]byte]bool{}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue // только 10/8, 172.16/12, 192.168/16 — не VPN и не интернет
			}
			own = append(own, pref{ip[0], ip[1], ip[2]})
		}
	}
	var out [][3]byte
	seen := map[[3]byte]bool{}
	for _, p := range own {
		if seen16[[2]byte{p.a, p.b}] {
			continue
		}
		seen16[[2]byte{p.a, p.b}] = true
		for d := 0; d < 256; d++ { // от своей подсети в обе стороны
			for _, c := range []int{int(p.c) - d, int(p.c) + d} {
				if c < 0 || c > 255 {
					continue
				}
				k := [3]byte{p.a, p.b, byte(c)}
				if !seen[k] {
					seen[k] = true
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// sweep рассылает запрос каждому адресу соседних подсетей (≈4000 пакетов в секунду, до 65 тыс.
// адресов на одну частную сеть). Останавливается, как только главный ПК найден.
func sweep(stop <-chan struct{}, conn *net.UDPConn, probe []byte, udpPort int, active func() bool) {
	nets := sweepNets()
	if len(nets) == 0 {
		return
	}
	log.Printf("поиск главного ПК в соседних подсетях (%d подсетей)…", len(nets))
	sent := 0
	for _, n := range nets {
		for d := 1; d < 255; d++ {
			conn.WriteToUDP(probe, &net.UDPAddr{IP: net.IPv4(n[0], n[1], n[2], byte(d)), Port: udpPort})
			sent++
			if sent%16 == 0 {
				select {
				case <-stop:
					return
				case <-time.After(4 * time.Millisecond):
				}
				if !active() {
					return
				}
			}
		}
	}
}

// firstReachable — первый по порядку адрес, до которого доходит TCP-соединение
// (все проверяются одновременно, не дольше 4 с).
func firstReachable(urls []string) string {
	type res struct {
		i  int
		ok bool
	}
	ch := make(chan res, len(urls))
	for i, u := range urls {
		go func(i int, u string) {
			host := strings.TrimPrefix(u, "https://")
			c, err := net.DialTimeout("tcp", host, 4*time.Second)
			if err == nil {
				c.Close()
			}
			ch <- res{i, err == nil}
		}(i, u)
	}
	ok := make([]bool, len(urls))
	for range urls {
		r := <-ch
		ok[r.i] = r.ok
		if r.ok && r.i == 0 {
			break
		}
	}
	for i := range ok {
		if ok[i] {
			return urls[i]
		}
	}
	return ""
}

// loginHash — короткий хэш логина для подсказки о несовпадающем пароле.
func loginHash(login string) string {
	s := sha256.Sum256([]byte("randdesk-login:" + strings.ToLower(strings.TrimSpace(login))))
	return hex.EncodeToString(s[:8])
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
