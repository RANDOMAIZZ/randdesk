// Package android — то, что нужно RANDSEC внутри приложения для Android: имя устройства,
// сведения о телефоне (их пишет Java-часть приложения) и DNS (в Android нет /etc/resolv.conf).
// На других системах функции ничего не меняют.
package android

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// On — RANDSEC работает внутри приложения для Android.
func On() bool { return runtime.GOOS == "android" || os.Getenv("RANDSEC_ANDROID") == "1" }

// Hostname — имя устройства: на Android его передаёт приложение (в системе hostname — «localhost»).
func Hostname() string {
	if h := strings.TrimSpace(os.Getenv("RANDSEC_HOSTNAME")); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

// Info — сведения о телефоне, которые приложение периодически записывает в файл.
type Info struct {
	Model        string  `json:"model"`
	Manufacturer string  `json:"manufacturer"`
	Android      string  `json:"android"` // версия Android, например «14»
	SDK          int     `json:"sdk"`
	Battery      int     `json:"battery"` // 0..100, -1 — неизвестно
	Charging     bool    `json:"charging"`
	Plugged      string  `json:"plugged,omitempty"` // ac, usb, wireless
	Temp         float64 `json:"temp,omitempty"`    // °C
	Screen       string  `json:"screen,omitempty"`  // разрешение экрана
	Rx           uint64  `json:"rx"`                // весь трафик телефона (TrafficStats), байт
	Tx           uint64  `json:"tx"`
	T            int64   `json:"t"` // когда записано, unix мс
}

// ReadInfo читает сведения, записанные приложением (nil — не Android или файла ещё нет).
func ReadInfo() *Info {
	p := os.Getenv("RANDSEC_ANDROID_INFO")
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var i Info
	if json.Unmarshal(b, &i) != nil {
		return nil
	}
	return &i
}

// CPUIdle — суммарное время простоя (мкс) по всем ядрам и число учтённых ядер.
// В Android приложениям закрыт /proc/stat, но время простоя ядер (cpuidle) открыто:
// загрузка = 1 − Δпростоя / (Δвремени × ядер).
func CPUIdle() (idleUS uint64, cores int) {
	dirs, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*")
	for _, d := range dirs {
		states, _ := filepath.Glob(filepath.Join(d, "cpuidle", "state[0-9]*", "time"))
		if len(states) == 0 {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(d, "online")); err == nil && strings.TrimSpace(string(b)) == "0" {
			continue
		}
		var sum uint64
		ok := false
		for _, f := range states {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
				sum += v
				ok = true
			}
		}
		if ok {
			idleUS += sum
			cores++
		}
	}
	return
}
