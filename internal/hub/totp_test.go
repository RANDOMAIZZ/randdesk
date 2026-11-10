package hub

import (
	"path/filepath"
	"testing"
	"time"
)

// Тестовые векторы RFC 6238 (SHA-1), последние 6 цифр.
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"}
	for ts, want := range cases {
		if got := totpAt(secret, ts/totpPeriod); got != want {
			t.Errorf("t=%d: %s, want %s", ts, got, want)
		}
	}
}

func TestCheckTOTPReplayAndSkew(t *testing.T) {
	sec := newTOTPSecret()
	raw, _ := b32.DecodeString(sec)
	now := time.Unix(1_700_000_000, 0)
	step := now.Unix() / totpPeriod
	code := totpAt(raw, step)
	got, ok := checkTOTP(sec, code, 0, now)
	if !ok || got != step {
		t.Fatalf("валидный код не принят")
	}
	if _, ok := checkTOTP(sec, code, step, now); ok {
		t.Fatal("повтор кода принят")
	}
	if _, ok := checkTOTP(sec, totpAt(raw, step-1), 0, now); !ok {
		t.Fatal("код предыдущего шага должен приниматься")
	}
	if _, ok := checkTOTP(sec, totpAt(raw, step-3), 0, now); ok {
		t.Fatal("устаревший код принят")
	}
}

func TestSecondFactorStore(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "hub.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("admin", "password1", true); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("other", "password1", true); err == nil {
		t.Fatal("второй «первый» пользователь создан")
	}
	sec := newTOTPSecret()
	codes, hashes := newRecoveryCodes()
	if err := st.EnableTOTP("admin", sec, hashes); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.CheckSecondFactor("admin", "000000"); ok {
		t.Fatal("неверный код принят") // вероятность совпадения пренебрежимо мала
	}
	if ok, rec := st.CheckSecondFactor("ADMIN", codes[0]); !ok || !rec {
		t.Fatal("резервный код не принят")
	}
	if ok, _ := st.CheckSecondFactor("admin", codes[0]); ok {
		t.Fatal("резервный код принят повторно")
	}
	raw, _ := b32.DecodeString(sec)
	code := totpAt(raw, time.Now().Unix()/totpPeriod+1)
	if ok, rec := st.CheckSecondFactor("admin", code); !ok || rec {
		t.Fatal("код приложения не принят")
	}
	if u, _ := st.User("admin"); !u.TOTP || u.RecoveryLeft != 9 {
		t.Fatalf("состояние пользователя: %+v", u)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"myhome.duckdns.org":    "myhome.duckdns.org:8420",
		"https://1.2.3.4:9000/": "1.2.3.4:9000",
		"example.com/path":      "example.com:8420",
		"":                      "",
	}
	for in, want := range cases {
		if got := normalizeHost(in, 8420); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
