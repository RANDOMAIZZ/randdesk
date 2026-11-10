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
func seeker(stop <-chan struct{}, udpPort int, group, login string, fast func() bool, found func(url, fp, name string), mismatch func(name string)) {
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
			found("https://"+net.JoinHostPort(from.IP.String(), strconv.Itoa(b.Port)), b.FP, b.Name)
		}
	}
	go handle(conn)
	probe, _ := json.Marshal(beacon{RD: 1, Type: "probe", Group: group, Login: login})
	for {
		for _, a := range broadcastAddrs(udpPort) {
			conn.WriteToUDP(probe, a)
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

// loginHash — короткий хэш логина для подсказки о несовпадающем пароле.
func loginHash(login string) string {
	s := sha256.Sum256([]byte("randdesk-login:" + strings.ToLower(strings.TrimSpace(login))))
	return hex.EncodeToString(s[:8])
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
