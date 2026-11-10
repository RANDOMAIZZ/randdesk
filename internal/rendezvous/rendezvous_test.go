package rendezvous

import (
	"testing"
	"time"

	"randdesk/internal/pair"
)

func TestSealOpen(t *testing.T) {
	a, _ := pair.Derive("boss", "password123")
	b, _ := pair.Derive("BOSS", "password123")
	other, _ := pair.Derive("boss", "password999")
	ca, _ := New(a, "")
	cb, _ := New(b, "")
	co, _ := New(other, "")
	if ca.topic != cb.topic || ca.topic == co.topic {
		t.Fatal("имя канала должно зависеть от логина и пароля")
	}
	s := ca.seal(Msg{Type: "net", Network: "1234567890abcdef", TS: time.Now().Unix()})
	if m, ok := cb.open(s); !ok || m.Network != "1234567890abcdef" {
		t.Fatal("ПК с тем же паролем не прочитал сообщение")
	}
	if _, ok := co.open(s); ok {
		t.Fatal("чужой пароль прочитал сообщение")
	}
	old := ca.seal(Msg{Type: "net", TS: time.Now().Add(-48 * time.Hour).Unix()})
	if _, ok := cb.open(old); ok {
		t.Fatal("устаревшее сообщение принято")
	}
}
