// Package wol отправляет Wake-on-LAN magic-пакеты.
package wol

import (
	"fmt"
	"net"
	"strings"
)

// Send рассылает magic-пакет для MAC во все локальные широковещательные адреса, а также на
// последние известные адреса выключенного ПК (hints) и широковещательные адреса их подсетей /24 —
// так пакет может дойти и через маршрутизатор, если тот ещё помнит MAC по IP или пропускает
// направленную рассылку.
func Send(mac string, hints ...string) error {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return fmt.Errorf("неверный MAC-адрес %q", mac)
	}
	pkt := make([]byte, 102)
	for i := 0; i < 6; i++ {
		pkt[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(pkt[i*6:], hw)
	}

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return err
	}
	defer conn.Close()

	sent := 0
	var lastErr error
	for _, target := range targets(hints) {
		for _, port := range []int{9, 7} {
			addr := &net.UDPAddr{IP: target, Port: port}
			if _, err := conn.WriteTo(pkt, addr); err != nil {
				lastErr = err
				continue
			}
			sent++
		}
	}
	if sent == 0 {
		return fmt.Errorf("не удалось отправить magic-пакет: %v", lastErr)
	}
	return nil
}

func targets(hints []string) []net.IP {
	out := broadcastAddrs()
	seen := map[string]bool{}
	for _, ip := range out {
		seen[ip.String()] = true
	}
	add := func(ip net.IP) {
		if !seen[ip.String()] {
			seen[ip.String()] = true
			out = append(out, ip)
		}
	}
	for _, h := range hints {
		ip := net.ParseIP(strings.TrimSpace(h)).To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || !ip.IsPrivate() {
			continue
		}
		add(ip)
		add(net.IPv4(ip[0], ip[1], ip[2], 255).To4())
	}
	return out
}

// SameLAN сообщает, есть ли у двух наборов адресов общая частная подсеть /24
// (виртуальные сети ZeroTier и т. п. не в счёт — через них magic-пакет не проходит).
func SameLAN(a, b []string) bool {
	nets := map[string]bool{}
	for _, s := range a {
		if k := lanKey(s); k != "" {
			nets[k] = true
		}
	}
	for _, s := range b {
		if k := lanKey(s); k != "" && nets[k] {
			return true
		}
	}
	return false
}

func lanKey(s string) string {
	ip := net.ParseIP(strings.TrimSpace(s)).To4()
	if ip == nil || !ip.IsPrivate() {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", ip[0], ip[1], ip[2])
}

// broadcastAddrs — общая рассылка и широковещательные адреса всех поднятых интерфейсов.
func broadcastAddrs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
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
			ip4 := ipn.IP.To4()
			if ip4 == nil || len(ipn.Mask) != 4 {
				continue
			}
			if ones, _ := ipn.Mask.Size(); ones >= 31 {
				continue
			}
			bc := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				bc[i] = ip4[i] | ^ipn.Mask[i]
			}
			out = append(out, bc)
		}
	}
	return out
}
