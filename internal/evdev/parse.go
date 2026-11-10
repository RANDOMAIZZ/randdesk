package evdev

import (
	"bufio"
	"io"
	"path/filepath"
	"strings"
)

type Kind int

const (
	Keyboard Kind = 1 << iota
	Mouse
	Gamepad
)

type device struct {
	path string
	kind Kind
	name string
}

// parseDevices разбирает /proc/bus/input/devices и определяет тип каждого устройства.
func parseDevices(f io.Reader) []device {
	var out []device
	var name, handlers, keyBits, relBits, absBits string
	flush := func() {
		defer func() { name, handlers, keyBits, relBits, absBits = "", "", "", "", "" }()
		var ev string
		for _, h := range strings.Fields(handlers) {
			if strings.HasPrefix(h, "event") {
				ev = h
			}
		}
		if ev == "" {
			return
		}
		var k Kind
		hs := " " + handlers + " "
		switch {
		case strings.Contains(hs, " js"):
			k = Gamepad
		case strings.Contains(hs, " kbd ") && hasBit(keyBits, 30): // есть клавиша A — настоящая клавиатура, а не кнопка питания
			k = Keyboard
		case strings.Contains(hs, " mouse") || (relBits != "" && hasBit(relBits, 0) && hasBit(relBits, 1)):
			k = Mouse
		case absBits != "" && hasBit(keyBits, 0x130): // BTN_GAMEPAD без драйвера js
			k = Gamepad
		default:
			return
		}
		out = append(out, device{path: filepath.Join("/dev/input", ev), kind: k, name: name})
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "N: Name="):
			name = strings.Trim(strings.TrimPrefix(line, "N: Name="), `"`)
		case strings.HasPrefix(line, "H: Handlers="):
			handlers = strings.TrimPrefix(line, "H: Handlers=")
		case strings.HasPrefix(line, "B: KEY="):
			keyBits = strings.TrimPrefix(line, "B: KEY=")
		case strings.HasPrefix(line, "B: REL="):
			relBits = strings.TrimPrefix(line, "B: REL=")
		case strings.HasPrefix(line, "B: ABS="):
			absBits = strings.TrimPrefix(line, "B: ABS=")
		}
	}
	flush()
	return out
}

// hasBit — установлен ли бит n в битовой маске вида "a b c" (слова по 64 бита, старшие — слева).
func hasBit(mask string, n int) bool {
	words := strings.Fields(mask)
	idx := len(words) - 1 - n/64
	if idx < 0 || idx >= len(words) {
		return false
	}
	var v uint64
	for _, c := range words[idx] {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint64(c-'a') + 10
		}
	}
	return v&(1<<(uint(n)%64)) != 0
}
