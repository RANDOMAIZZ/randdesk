package agent

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

var errPin = errors.New("отпечаток сертификата хаба не совпадает — это не ваш хаб (подмена?) или хаб переустановлен; привяжите ПК заново")

// normFingerprint приводит отпечаток к 64 шестнадцатеричным символам в нижнем регистре.
func normFingerprint(fp string) (string, error) {
	fp = strings.ToLower(strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(fp)))
	if strings.HasPrefix(fp, "sha256") {
		fp = strings.TrimPrefix(fp, "sha256")
	}
	if fp == "" {
		return "", nil
	}
	if b, err := hex.DecodeString(fp); err != nil || len(b) != sha256.Size {
		return "", errors.New("неверный отпечаток -fp: нужно 64 шестнадцатеричных символа")
	}
	return fp, nil
}

// tlsConfig — при заданном отпечатке хаб проверяется по закреплённому открытому ключу
// (самоподписанный сертификат хаба), иначе — обычной проверкой цепочки сертификатов.
func tlsConfig(fp string) *tls.Config {
	if fp == "" {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // цепочку заменяет проверка отпечатка ниже
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errPin
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if hex.EncodeToString(sum[:]) != fp {
				return errPin
			}
			return nil
		},
	}
}

var netDialer = &net.Dialer{Timeout: 6 * time.Second, KeepAlive: 30 * time.Second}

func httpClient(fp string) *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext:         netDialer.DialContext,
			TLSClientConfig:     tlsConfig(fp),
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// parseHubs разбирает список адресов хаба через запятую или пробел.
// Без схемы адрес считается https://, если задан отпечаток, иначе http:// (как в прежних версиях).
func parseHubs(s, fp string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		u := normalizeHub(f, fp != "")
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

func normalizeHub(u string, secure bool) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u != "" && !strings.Contains(u, "://") {
		if secure {
			u = "https://" + u
		} else {
			u = "http://" + u
		}
	}
	return u
}
