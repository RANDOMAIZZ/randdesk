package main

import "testing"

func TestParseInstall(t *testing.T) {
	h, k, f := parseInstall(`.\randdesk.exe agent install -hub https://192.168.1.5:8420,https://x.duckdns.org:8420 -key RD-AAAA-BBBB-CCCC-DDDD -fp abc123`)
	if h != "https://192.168.1.5:8420,https://x.duckdns.org:8420" || k != "RD-AAAA-BBBB-CCCC-DDDD" || f != "abc123" {
		t.Fatalf("%q %q %q", h, k, f)
	}
	if h, k, _ := parseInstall("192.168.1.5:8420"); h != "192.168.1.5:8420" || k != "" {
		t.Fatalf("адрес: %q %q", h, k)
	}
}
