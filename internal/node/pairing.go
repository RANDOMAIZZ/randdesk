package node

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"randdesk/internal/agent"
	"randdesk/internal/pair"
)

// probeFingerprint подключается к хабу и узнаёт отпечаток его сертификата.
// Доверять ему заранее не нужно: подлинность хаба проверяется доказательством знания пароля.
func probeFingerprint(hubURL string) (string, error) {
	u, err := url.Parse(hubURL)
	if err != nil {
		return "", err
	}
	d := &net.Dialer{Timeout: 6 * time.Second}
	c, err := tls.DialWithDialer(d, "tcp", u.Host, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if err != nil {
		return "", err
	}
	defer c.Close()
	certs := c.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("хаб не прислал сертификат")
	}
	sum := sha256.Sum256(certs[0].RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}

var errWrongAccount = errors.New("на главном ПК другой логин или пароль")

// pairWith привязывает этот ПК к хабу по общему логину и паролю и возвращает конфигурацию агента.
func pairWith(hubURL string, acc pair.Account, machine string) (*agent.Config, string, error) {
	hubURL = strings.TrimRight(hubURL, "/")
	fp, err := probeFingerprint(hubURL)
	if err != nil {
		return nil, "", err
	}
	host, _ := os.Hostname()
	nb := make([]byte, 16)
	rand.Read(nb)
	req := pair.Request{Group: acc.Group, Machine: machine, Host: host, Nonce: hex.EncodeToString(nb), TS: time.Now().Unix(), FP: fp}
	acc.Sign(&req)
	body, _ := json.Marshal(req)
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: agent.TLSConfig(fp)}}
	r, err := client.Post(hubURL+"/api/agent/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if r.StatusCode == http.StatusForbidden {
		return nil, "", errWrongAccount
	}
	if r.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = r.Status
		}
		return nil, "", fmt.Errorf("главный ПК: %s", e.Error)
	}
	var resp pair.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, "", err
	}
	if !acc.CheckResponse(req.Nonce, resp) {
		return nil, "", errors.New("главный ПК не подтвердил пароль — возможна подмена, подключение отменено")
	}
	cfg := &agent.Config{Hub: hubURL, Hubs: []string{hubURL}, Fingerprint: fp, DeviceID: resp.DeviceID, Token: resp.Token}
	return cfg, resp.Name, nil
}
