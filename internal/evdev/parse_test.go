package evdev

import (
	"strings"
	"testing"
)

const sample = `I: Bus=0019 Vendor=0000 Product=0001 Version=0000
N: Name="Power Button"
H: Handlers=kbd event0
B: EV=3
B: KEY=10000000000000 0

I: Bus=0011 Vendor=0001 Product=0001 Version=ab41
N: Name="AT Translated Set 2 keyboard"
H: Handlers=sysrq kbd event3 leds
B: EV=120013
B: KEY=402000000 3803078f800d001 feffffdfffefffff fffffffffffffffe

I: Bus=0003 Vendor=046d Product=c077 Version=0111
N: Name="Logitech USB Optical Mouse"
H: Handlers=mouse0 event5
B: EV=17
B: KEY=70000 0 0 0 0
B: REL=903

I: Bus=0003 Vendor=045e Product=028e Version=0114
N: Name="Microsoft X-Box 360 pad"
H: Handlers=event7 js0
B: EV=20000b
B: KEY=7cdb000000000000 0 0 0 0
B: ABS=3003f
`

func TestParseDevices(t *testing.T) {
	got := map[string]Kind{}
	for _, d := range parseDevices(strings.NewReader(sample)) {
		got[d.name] = d.kind
	}
	want := map[string]Kind{
		"AT Translated Set 2 keyboard": Keyboard,
		"Logitech USB Optical Mouse":   Mouse,
		"Microsoft X-Box 360 pad":      Gamepad,
	}
	for n, k := range want {
		if got[n] != k {
			t.Errorf("%s: %v, want %v", n, got[n], k)
		}
	}
	if _, ok := got["Power Button"]; ok {
		t.Error("кнопку питания нельзя считать клавиатурой")
	}
}
