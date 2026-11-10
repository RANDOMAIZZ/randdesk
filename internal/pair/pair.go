// Package pair — привязка ПК друг к другу по общему логину и паролю.
//
// Из логина и пароля (scrypt) получается секрет учётной записи. Из него:
//   - Group — открытый идентификатор группы, по нему ПК узнают «своих» в сети;
//   - Auth  — ключ, которым обе стороны доказывают знание пароля, не передавая его.
//
// Привязка: агент шлёт хабу доказательство (HMAC) с одноразовым номером и отпечатком
// сертификата, который видит; хаб проверяет и в ответ доказывает, что тоже знает пароль.
// Подменить хаб или подслушать пароль нельзя: пароль по сети не передаётся, а отпечаток
// входит в доказательство.
package pair

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// Account — секреты учётной записи, общие для всех ПК с одинаковыми логином и паролем.
type Account struct {
	Login string `json:"login"`
	Group string `json:"group"` // hex, открытый
	Auth  string `json:"auth"`  // hex, секрет
}

// Derive вычисляет секреты учётной записи (≈0,2 с — защита от перебора пароля).
func Derive(login, password string) (Account, error) {
	login = strings.TrimSpace(login)
	salt := []byte("randdesk-account-v1:" + strings.ToLower(login))
	master, err := scrypt.Key([]byte(password), salt, 1<<16, 8, 1, 32)
	if err != nil {
		return Account{}, err
	}
	return Account{
		Login: login,
		Group: hex.EncodeToString(mac(master, "group")[:16]),
		Auth:  hex.EncodeToString(mac(master, "auth")),
	}, nil
}

func mac(key []byte, parts ...string) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write([]byte(p))
		m.Write([]byte{0})
	}
	return m.Sum(nil)
}

func (a Account) key() []byte {
	k, _ := hex.DecodeString(a.Auth)
	return k
}

// Valid — учётная запись настроена.
func (a Account) Valid() bool { return a.Group != "" && len(a.key()) == 32 }

// Request — запрос агента на привязку к хабу.
type Request struct {
	Group   string `json:"group"`
	Machine string `json:"machine"` // постоянный идентификатор ПК (чтобы не плодить дубли)
	Host    string `json:"host"`
	Nonce   string `json:"nonce"`
	TS      int64  `json:"ts"`
	FP      string `json:"fp"` // отпечаток сертификата хаба, каким его видит агент
	Proof   string `json:"proof"`
}

// Response — ответ хаба: токен устройства и доказательство знания пароля.
type Response struct {
	DeviceID string `json:"device_id"`
	Token    string `json:"token"`
	Name     string `json:"name"` // имя ПК с хабом
	Proof    string `json:"proof"`
}

func (a Account) reqProof(r Request) string {
	return hex.EncodeToString(mac(a.key(), "pair-req", r.Group, r.Machine, r.Host, r.Nonce, strconv.FormatInt(r.TS, 10), r.FP))
}

func (a Account) respProof(nonce string, r Response) string {
	return hex.EncodeToString(mac(a.key(), "pair-resp", nonce, r.DeviceID, r.Token, r.Name))
}

// Sign заполняет доказательство в запросе.
func (a Account) Sign(r *Request) { r.Proof = a.reqProof(*r) }

// CheckRequest — запрос сделан тем, кто знает пароль.
func (a Account) CheckRequest(r Request) bool {
	return r.Group == a.Group && hmac.Equal([]byte(r.Proof), []byte(a.reqProof(r)))
}

// SignResponse и CheckResponse — то же для ответа хаба.
func (a Account) SignResponse(nonce string, r *Response) { r.Proof = a.respProof(nonce, *r) }

func (a Account) CheckResponse(nonce string, r Response) bool {
	return hmac.Equal([]byte(r.Proof), []byte(a.respProof(nonce, r)))
}

// Subkey — отдельный ключ для другой цели (шифрование точки встречи и т. п.), из секрета учётной записи.
func (a Account) Subkey(purpose string) []byte { return mac(a.key(), "subkey", purpose) }
