// Package upnp — проброс порта на домашнем роутере через UPnP IGD.
package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// IGD — найденный интернет-шлюз (роутер).
type IGD struct {
	Location    string // адрес описания устройства
	ControlURL  string
	ServiceType string
	LocalIP     string // наш адрес в сети роутера
}

var searchTargets = []string{
	"urn:schemas-upnp-org:device:InternetGatewayDevice:1",
	"urn:schemas-upnp-org:device:InternetGatewayDevice:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

var client = &http.Client{Timeout: 5 * time.Second}

// localIPv4 — адреса физических локальных интерфейсов (VPN/TUN-адаптеры могут
// перехватывать multicast, поэтому запрос отправляется с каждого интерфейса отдельно).
func localIPv4() []net.IP {
	var out []net.IP
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && ipn.IP.IsPrivate() {
				out = append(out, ipn.IP.To4())
			}
		}
	}
	return out
}

// Discover ищет роутер с поддержкой UPnP в локальной сети.
func Discover(ctx context.Context) (*IGD, error) {
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	locs := make(chan string, 32)
	var conns []net.PacketConn
	binds := append([]net.IP{nil}, localIPv4()...) // nil — маршрут по умолчанию
	dst := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	for _, ip := range binds {
		addr := ":0"
		if ip != nil {
			addr = net.JoinHostPort(ip.String(), "0")
		}
		conn, err := net.ListenPacket("udp4", addr)
		if err != nil {
			continue
		}
		conns = append(conns, conn)
		for _, st := range searchTargets {
			msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nST: " + st +
				"\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\n\r\n"
			conn.WriteTo([]byte(msg), dst)
		}
		conn.SetReadDeadline(deadline)
		go func(c net.PacketConn) {
			buf := make([]byte, 4096)
			for {
				n, _, err := c.ReadFrom(buf)
				if err != nil {
					return
				}
				if loc := header(string(buf[:n]), "location"); loc != "" {
					select {
					case locs <- loc:
					default:
					}
				}
			}
		}(conn)
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	if len(conns) == 0 {
		return nil, errors.New("не удалось открыть UDP-сокет для поиска роутера")
	}
	// описания устройств запрашиваем параллельно: в сети бывают медленные UPnP-устройства (ТВ, NAS)
	type res struct {
		g   *IGD
		err error
	}
	results := make(chan res, 32)
	seen := map[string]bool{}
	running := 0
	var lastErr error
	timer := time.NewTimer(time.Until(deadline) + 4*time.Second)
	defer timer.Stop()
	searchDone := time.After(time.Until(deadline))
	searching := true
	for {
		if !searching && running == 0 {
			break
		}
		select {
		case loc := <-locs:
			if seen[loc] {
				continue
			}
			seen[loc] = true
			running++
			go func(loc string) {
				g, err := describe(ctx, loc)
				results <- res{g, err}
			}(loc)
		case r := <-results:
			running--
			if r.err == nil {
				return r.g, nil
			}
			lastErr = r.err
		case <-searchDone:
			searching = false
		case <-timer.C:
			running, searching = 0, false
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("роутер с UPnP не найден (ответили только другие устройства: %w)", lastErr)
	}
	return nil, errors.New("роутер с UPnP не найден (UPnP выключен в настройках роутера или не поддерживается)")
}

func header(resp, name string) string {
	for _, line := range strings.Split(resp, "\r\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

type xmlService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

type xmlDevice struct {
	Services []xmlService `xml:"serviceList>service"`
	Devices  []xmlDevice  `xml:"deviceList>device"`
}

type xmlRoot struct {
	URLBase string    `xml:"URLBase"`
	Device  xmlDevice `xml:"device"`
}

func findService(d xmlDevice) *xmlService {
	for i, s := range d.Services {
		if strings.Contains(s.ServiceType, "WANIPConnection") || strings.Contains(s.ServiceType, "WANPPPConnection") {
			return &d.Services[i]
		}
	}
	for _, sub := range d.Devices {
		if s := findService(sub); s != nil {
			return s
		}
	}
	return nil
}

func describe(ctx context.Context, loc string) (*IGD, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var root xmlRoot
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&root); err != nil {
		return nil, err
	}
	svc := findService(root.Device)
	if svc == nil {
		return nil, errors.New("нет службы WANIPConnection")
	}
	base := loc
	if root.URLBase != "" {
		base = root.URLBase
	}
	bu, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	cu, err := bu.Parse(svc.ControlURL)
	if err != nil {
		return nil, err
	}
	g := &IGD{Location: loc, ControlURL: cu.String(), ServiceType: svc.ServiceType}
	// локальный адрес, которым мы видны роутеру
	lu, _ := url.Parse(loc)
	if c, err := net.Dial("udp4", lu.Host); err == nil {
		g.LocalIP = c.LocalAddr().(*net.UDPAddr).IP.String()
		c.Close()
	}
	return g, nil
}

// Gateway — адрес роутера.
func (g *IGD) Gateway() string {
	if u, err := url.Parse(g.Location); err == nil {
		return u.Hostname()
	}
	return ""
}

type soapFault struct {
	Code string `xml:"Body>Fault>detail>UPnPError>errorCode"`
	Desc string `xml:"Body>Fault>detail>UPnPError>errorDescription"`
}

func (g *IGD) soap(ctx context.Context, action string, args [][2]string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	fmt.Fprintf(&b, `<u:%s xmlns:u="%s">`, action, g.ServiceType)
	for _, a := range args {
		b.WriteString("<" + a[0] + ">")
		xml.EscapeText(&b, []byte(a[1]))
		b.WriteString("</" + a[0] + ">")
	}
	fmt.Fprintf(&b, `</u:%s></s:Body></s:Envelope>`, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.ControlURL, strings.NewReader(b.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+g.ServiceType+"#"+action+`"`)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var f soapFault
		xml.Unmarshal(body, &f)
		if f.Code != "" {
			return nil, fmt.Errorf("роутер отказал в %s: %s %s", action, f.Code, f.Desc)
		}
		return nil, fmt.Errorf("роутер отказал в %s: %s", action, resp.Status)
	}
	return body, nil
}

// ExternalIP — внешний (интернет) адрес роутера.
func (g *IGD) ExternalIP(ctx context.Context) (string, error) {
	body, err := g.soap(ctx, "GetExternalIPAddress", nil)
	if err != nil {
		return "", err
	}
	var r struct {
		IP string `xml:"Body>GetExternalIPAddressResponse>NewExternalIPAddress"`
	}
	if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&r); err != nil {
		return "", err
	}
	if net.ParseIP(r.IP) == nil {
		return "", errors.New("роутер не сообщил внешний адрес (нет подключения к интернету?)")
	}
	return r.IP, nil
}

// AddMapping пробрасывает внешний TCP-порт на наш локальный порт.
// lease — срок в секундах; если роутер не поддерживает срок, пробуем бессрочно.
func (g *IGD) AddMapping(ctx context.Context, extPort, intPort int, desc string, lease int) error {
	if g.LocalIP == "" {
		return errors.New("не удалось определить локальный адрес")
	}
	args := func(l int) [][2]string {
		return [][2]string{
			{"NewRemoteHost", ""},
			{"NewExternalPort", fmt.Sprint(extPort)},
			{"NewProtocol", "TCP"},
			{"NewInternalPort", fmt.Sprint(intPort)},
			{"NewInternalClient", g.LocalIP},
			{"NewEnabled", "1"},
			{"NewPortMappingDescription", desc},
			{"NewLeaseDuration", fmt.Sprint(l)},
		}
	}
	_, err := g.soap(ctx, "AddPortMapping", args(lease))
	if err != nil && lease != 0 {
		if _, err2 := g.soap(ctx, "AddPortMapping", args(0)); err2 == nil {
			return nil
		}
	}
	return err
}

// DeleteMapping удаляет проброс порта.
func (g *IGD) DeleteMapping(ctx context.Context, extPort int) error {
	_, err := g.soap(ctx, "DeletePortMapping", [][2]string{
		{"NewRemoteHost", ""},
		{"NewExternalPort", fmt.Sprint(extPort)},
		{"NewProtocol", "TCP"},
	})
	return err
}
