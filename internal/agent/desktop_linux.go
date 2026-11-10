//go:build linux

package agent

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"randsec/internal/desktop"
	"randsec/internal/evdev"
	"randsec/internal/session"
)

// display — рабочий стол пользователя, к которому подключаются помощники.
type display struct {
	Display    string
	XAuthority string
	Wayland    bool
}

func (d display) key() string { return d.Display + "|" + d.XAuthority }

// findDisplay ищет графический сеанс пользователя по окружению его процессов:
// служба работает от root вне сеанса и сама переменной DISPLAY не имеет.
func findDisplay() (display, error) {
	if v := os.Getenv("DISPLAY"); v != "" { // агент запущен прямо в сеансе (консольный режим)
		return display{Display: v, XAuthority: os.Getenv("XAUTHORITY"), Wayland: os.Getenv("WAYLAND_DISPLAY") != ""}, nil
	}
	entries, _ := os.ReadDir("/proc")
	var best display
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid < 2 {
			continue
		}
		var st syscall.Stat_t
		if syscall.Stat("/proc/"+e.Name(), &st) != nil || st.Uid < 1000 {
			continue // только процессы обычных пользователей
		}
		env, err := os.ReadFile("/proc/" + e.Name() + "/environ")
		if err != nil {
			continue
		}
		var d display
		var home string
		for _, kv := range bytes.Split(env, []byte{0}) {
			k, v, _ := strings.Cut(string(kv), "=")
			switch k {
			case "DISPLAY":
				d.Display = v
			case "XAUTHORITY":
				d.XAuthority = v
			case "WAYLAND_DISPLAY":
				d.Wayland = v != ""
			case "HOME":
				home = v
			}
		}
		if d.Display == "" {
			continue
		}
		if d.XAuthority == "" {
			for _, p := range []string{
				filepath.Join("/run/user", strconv.Itoa(int(st.Uid)), "gdm", "Xauthority"),
				filepath.Join(home, ".Xauthority"),
			} {
				if _, err := os.Stat(p); err == nil {
					d.XAuthority = p
					break
				}
			}
		}
		if best.Display == "" || (best.XAuthority == "" && d.XAuthority != "") {
			best = d
		}
		if best.XAuthority != "" {
			break
		}
	}
	if best.Display == "" {
		return best, errors.New("нет графического сеанса пользователя (никто не вошёл в систему или рабочий стол без X11)")
	}
	return best, nil
}

type helperProc struct {
	key  string
	done chan struct{}
	kill func()
}

func (h *helperProc) exited() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// startHelper запускает этот же исполняемый файл с доступом к рабочему столу пользователя.
func startHelper(args []string) (*helperProc, error) {
	d, err := findDisplay()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+d.Display)
	if d.XAuthority != "" {
		cmd.Env = append(cmd.Env, "XAUTHORITY="+d.XAuthority)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} // завершается вместе с агентом
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	hp := &helperProc{key: d.key(), done: make(chan struct{}), kill: func() { cmd.Process.Kill() }}
	go func() { cmd.Wait(); close(hp.done) }()
	return hp, nil
}

func startDesktopHelper(cfgPath, session, hub, indicator string) error {
	_, err := startHelper([]string{"agent", "desktop-helper", "-config", cfgPath, "-session", session, "-hub", hub, "-indicator", indicator})
	return err
}

// superviseSessionHelper держит помощника сеанса (буфер обмена, блокировка ввода) запущенным
// и перезапускает его при смене пользователя и сбоях.
func (a *Agent) superviseSessionHelper(stop <-chan struct{}) {
	var hp *helperProc
	defer func() {
		if hp != nil && !hp.exited() {
			hp.kill()
		}
	}()
	fails := 0
	for {
		d, err := findDisplay()
		if hp != nil && (hp.exited() || (err == nil && hp.key != d.key())) {
			if !hp.exited() {
				hp.kill()
			}
			hp = nil
		}
		// без рабочего стола помощник всё равно нужен: он отключает клавиатуру и мышь (evdev)
		if hp == nil && a.cfg.path != "" {
			p, serr := startHelperOrPlain([]string{"agent", "session-helper", "-config", a.cfg.path, "-hub", a.current()}, err == nil)
			if serr != nil {
				if fails++; fails%20 == 1 {
					log.Printf("помощник сеанса: %v", serr)
				}
			} else {
				hp, fails = p, 0
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func startHelperOrPlain(args []string, withDisplay bool) (*helperProc, error) {
	if withDisplay {
		return startHelper(args)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	hp := &helperProc{key: "|", done: make(chan struct{}), kill: func() { cmd.Process.Kill() }}
	go func() { cmd.Wait(); close(hp.done) }()
	return hp, nil
}

// runDesktopHelper — процесс с доступом к рабочему столу: обслуживает удалённый рабочий стол.
func runDesktopHelper(cfg *Config, sessionID string) {
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(cfg.path), "desktop.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
	ws, err := New(cfg).dial("/api/agent/stream?session=" + url.QueryEscape(sessionID))
	if err != nil {
		log.Fatalf("рабочий стол: подключение к хабу: %v", err)
	}
	defer ws.Close()
	log.Printf("рабочий стол: сеанс начат (pid %d, DISPLAY=%s)", os.Getpid(), os.Getenv("DISPLAY"))
	if err := desktop.Serve(ws); err != nil {
		log.Printf("рабочий стол: %v", err)
	}
	log.Printf("рабочий стол: сеанс завершён")
}

// runSessionHelper — процесс помощника сеанса: блокировка ввода и буфер обмена.
func runSessionHelper(cfg *Config) {
	a := New(cfg)
	backoff := 2 * time.Second
	for {
		ws, err := a.connectPath("/api/agent/session")
		if err == nil {
			backoff = 2 * time.Second
			session.Serve(ws)
			ws.Close()
		} else if backoff >= 30*time.Second {
			log.Printf("помощник сеанса: %v", err)
		}
		time.Sleep(backoff)
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// ---------- геймпады ----------

var (
	padOnce sync.Once
	pads    *evdev.Grabber
)

// setGamepads отключает геймпады захватом устройств (агент работает от root).
// После перезапуска агента хаб присылает команду заново.
func setGamepads(disable bool) error {
	if !isElevated() {
		return errors.New("нужны права root")
	}
	padOnce.Do(func() { pads = evdev.NewGrabber() })
	if disable {
		pads.Set(evdev.Gamepad)
		log.Printf("геймпады: отключено устройств — %d", pads.Count())
	} else {
		pads.Set(0)
		log.Printf("геймпады: включены")
	}
	return nil
}
