// Package wol отправляет Wake-on-LAN magic-пакеты.
package wol

import (
	"fmt"
	"net"
)

// Send рассылает magic-пакет для MAC во все локальные широковещательные адреса.
func Send(mac string) error {
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
	for _, target := range broadcastAddrs() {
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
