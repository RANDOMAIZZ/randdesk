package hub

import (
	"reflect"
	"testing"

	"randsec/internal/proto"
	"randsec/internal/wol"
)

func TestWakeLAN(t *testing.T) {
	komputer := []proto.NIC{
		{Name: "Radmin VPN", MAC: "02:50:5a:bb:53:08", IPs: []string{"26.177.188.98"}},
		{Name: "Ethernet", MAC: "d8:bb:c1:60:44:d8", IPs: []string{"192.168.40.99"}},
		{Name: "Ethernet 5", MAC: "0a:00:27:00:00:05", IPs: []string{"192.168.56.1"}},
		{Name: "ZeroTier One [6431897bd819c9b0]", MAC: "b2:ad:28:51:00:51", IPs: []string{"10.147.178.106"}},
	}
	oit := []proto.NIC{
		{Name: "Ethernet", MAC: "a8:a1:59:ed:0e:38", IPs: []string{"192.168.20.162"}},
		{Name: "Ethernet 2", MAC: "5c:e9:31:f9:72:1c", IPs: []string{"192.168.10.16"}},
		{Name: "ZeroTier One [6431897bd819c9b0]", MAC: "b2:63:89:d3:12:2e", IPs: []string{"10.147.178.169"}},
	}
	if got := lanIPs(oit); !reflect.DeepEqual(got, []string{"192.168.20.162", "192.168.10.16"}) {
		t.Fatalf("lanIPs = %v", got)
	}
	if got := lanIPs(komputer); !reflect.DeepEqual(got, []string{"192.168.40.99"}) {
		t.Fatalf("lanIPs komputer = %v", got)
	}
	if wol.SameLAN(lanIPs(komputer), lanIPs(oit)) {
		t.Fatal("KOMPUTER и PC-OIT не в одной сети, а ZeroTier не считается")
	}
	if !wol.SameLAN([]string{"192.168.10.5"}, lanIPs(oit)) {
		t.Fatal("ПК из 192.168.10.x — в сети PC-OIT")
	}
}
