package upnp

import (
	"context"
	"os"
	"testing"
	"time"
)

// Проверка на реальном роутере, только чтение (поиск и внешний адрес, без проброса):
//
//	RANDDESK_UPNP_TEST=1 go test ./internal/upnp -run Discover -v
func TestDiscoverLive(t *testing.T) {
	if os.Getenv("RANDDESK_UPNP_TEST") == "" {
		t.Skip("задайте RANDDESK_UPNP_TEST=1 для проверки на реальном роутере")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	g, err := Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("роутер %s, служба %s, наш адрес %s", g.Gateway(), g.ServiceType, g.LocalIP)
	ip, err := g.ExternalIP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("внешний адрес: %s", ip)
}
