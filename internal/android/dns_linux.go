//go:build linux

package android

import (
	"context"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata" // в Android база часовых поясов лежит не там, где её ищет Go для Linux
)

// В Android нет /etc/resolv.conf, и встроенный в Go DNS без cgo обращается к 127.0.0.1:53.
// Приложение передаёт DNS-серверы сети в RANDSEC_DNS; запасные — публичные.
func init() {
	if !On() {
		return
	}
	var servers []string
	for _, s := range strings.FieldsFunc(os.Getenv("RANDSEC_DNS"), func(r rune) bool { return r == ',' || r == ' ' }) {
		if ip := net.ParseIP(strings.Trim(s, "[]")); ip != nil {
			servers = append(servers, net.JoinHostPort(ip.String(), "53"))
		}
	}
	servers = append(servers, "1.1.1.1:53", "8.8.8.8:53")
	var next atomic.Uint32
	d := &net.Dialer{Timeout: 3 * time.Second}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var last error
			start := int(next.Add(1))
			for i := range servers {
				c, err := d.DialContext(ctx, network, servers[(start+i)%len(servers)])
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
}
