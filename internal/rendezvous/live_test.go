package rendezvous

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"randdesk/internal/pair"
)

// RANDDESK_NTFY_TEST=1 go test ./internal/rendezvous -run Live -v
func TestLiveNtfy(t *testing.T) {
	if os.Getenv("RANDDESK_NTFY_TEST") == "" {
		t.Skip("задайте RANDDESK_NTFY_TEST=1 для проверки с ntfy.sh")
	}
	b := make([]byte, 16)
	rand.Read(b)
	acc, _ := pair.Derive("test-"+hex.EncodeToString(b), hex.EncodeToString(b))
	c, _ := New(acc, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got := make(chan Msg, 1)
	go c.Subscribe(ctx, func(m Msg) { got <- m })
	time.Sleep(2 * time.Second)
	if err := c.Publish(ctx, Msg{Type: "join", Node: "0123456789"}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if m.Node != "0123456789" {
			t.Fatalf("неверное сообщение %+v", m)
		}
	case <-ctx.Done():
		t.Fatal("сообщение не пришло по подписке")
	}
	// ntfy.sh записывает сообщения в хранилище с задержкой в несколько секунд
	var list []Msg
	var err error
	for range 10 {
		if list, err = c.Poll(ctx); err == nil && len(list) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil || len(list) == 0 {
		t.Fatalf("poll: %v %d", err, len(list))
	}
}
