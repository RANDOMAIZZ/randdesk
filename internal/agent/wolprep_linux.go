//go:build linux

package agent

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// prepareWake включает Wake-on-LAN (magic-пакет) на физических сетевых картах через ethtool.
// Настройка живёт до перезагрузки, поэтому агент повторяет её при каждом запуске.
func prepareWake() {
	eth, err := exec.LookPath("ethtool")
	if err != nil {
		return
	}
	ifs, _ := filepath.Glob("/sys/class/net/*/device")
	for _, d := range ifs {
		name := filepath.Base(filepath.Dir(d))
		if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
			continue
		}
		out, err := exec.Command(eth, name).CombinedOutput()
		if err != nil {
			continue
		}
		var supports, cur string
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			if v, ok := strings.CutPrefix(l, "Supports Wake-on:"); ok {
				supports = strings.TrimSpace(v)
			} else if v, ok := strings.CutPrefix(l, "Wake-on:"); ok {
				cur = strings.TrimSpace(v)
			}
		}
		if strings.Contains(supports, "g") && !strings.Contains(cur, "g") {
			if exec.Command(eth, "-s", name, "wol", "g").Run() == nil {
				log.Printf("Wake-on-LAN включён на %s", name)
			}
		}
	}
}
