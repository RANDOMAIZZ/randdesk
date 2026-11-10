package hub

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP по RFC 6238: SHA-1, 6 цифр, шаг 30 секунд — совместимо с Google Authenticator,
// Microsoft Authenticator, Яндекс Ключом, Aegis, 1Password и др.
const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1 // допускаем ±1 шаг (расхождение часов до 30 с)
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string { return b32.EncodeToString(randBytes(20)) }

func totpAt(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

// checkTOTP возвращает шаг, на котором код совпал, если он новее last (защита от повтора).
func checkTOTP(secretB32, code string, last int64, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	secret, err := b32.DecodeString(strings.ToUpper(secretB32))
	if err != nil {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		st := cur + d
		if st <= last {
			continue
		}
		if hmac.Equal([]byte(totpAt(secret, st)), []byte(code)) {
			return st, true
		}
	}
	return 0, false
}

func totpURI(user, secret string) string {
	label := url.PathEscape("RandDesk:" + user)
	return "otpauth://totp/" + label + "?secret=" + secret + "&issuer=RandDesk&algorithm=SHA1&digits=6&period=30"
}

// ---------- резервные коды ----------

func normRecovery(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(c)))
}

func hashRecovery(c string) string {
	s := sha256.Sum256([]byte("randdesk-recovery:" + normRecovery(c)))
	return hex.EncodeToString(s[:])
}

// newRecoveryCodes — 10 одноразовых кодов вида ABCD-EFGH и их хэши для хранения.
func newRecoveryCodes() (codes, hashes []string) {
	for range 10 {
		raw := b32.EncodeToString(randBytes(5)) // 8 символов
		c := raw[:4] + "-" + raw[4:]
		codes = append(codes, c)
		hashes = append(hashes, hashRecovery(c))
	}
	return codes, hashes
}
