//go:build windows

package agent

import (
	"errors"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"randsec/internal/desktop"
)

// runDesktopHelper — отдельный процесс в сеансе пользователя: подключается к хабу
// по одноразовому идентификатору потока и обслуживает удалённый рабочий стол.
func runDesktopHelper(cfg *Config, session string) {
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(cfg.path), "desktop.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
	ws, err := New(cfg).dial("/api/agent/stream?session=" + url.QueryEscape(session))
	if err != nil {
		log.Fatalf("рабочий стол: подключение к хабу: %v", err)
	}
	defer ws.Close()
	log.Printf("рабочий стол: сеанс начат (pid %d)", os.Getpid())
	if err := desktop.Serve(ws); err != nil {
		log.Printf("рабочий стол: %v", err)
	}
	log.Printf("рабочий стол: сеанс завершён")
}

var (
	jobOnce sync.Once
	job     windows.Handle
)

// helperJob — объект задания: помощники завершаются вместе с агентом.
func helperJob() windows.Handle {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			return
		}
		job = h
	})
	return job
}

func currentSessionID() uint32 {
	var s uint32
	windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &s)
	return s
}

func activeSessionID() (uint32, error) {
	if id := windows.WTSGetActiveConsoleSessionId(); id != 0xFFFFFFFF {
		return id, nil
	}
	var p *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &p, &n); err != nil {
		return 0, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(p)))
	for _, s := range unsafe.Slice(p, n) {
		if s.State == windows.WTSActive && s.SessionID != 0 {
			return s.SessionID, nil
		}
	}
	return 0, errors.New("нет активного сеанса пользователя")
}

func startDesktopHelper(cfgPath, session, hub, indicator string) error {
	_, err := startHelper([]string{"agent", "desktop-helper", "-config", cfgPath, "-session", session, "-hub", hub, "-indicator", indicator})
	return err
}

// helperProc — запущенный процесс-помощник; done закрывается при его завершении.
type helperProc struct {
	session uint32
	done    chan struct{}
	kill    func()
}

func (h *helperProc) exited() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// startHelper запускает этот же exe с аргументами в активном сеансе пользователя.
func startHelper(args []string) (*helperProc, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	hp := &helperProc{done: make(chan struct{})}

	if cur := currentSessionID(); cur != 0 {
		// агент запущен в сеансе пользователя (консольный режим) — обычный дочерний процесс
		cmd := exec.Command(exe, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		if j := helperJob(); j != 0 {
			if h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
				windows.AssignProcessToJobObject(j, h)
				windows.CloseHandle(h)
			}
		}
		hp.session = cur
		hp.kill = func() { cmd.Process.Kill() }
		go func() { cmd.Wait(); close(hp.done) }()
		return hp, nil
	}

	// служба в сеансе 0: запускаем помощника от SYSTEM в активном сеансе пользователя —
	// так доступен и обычный рабочий стол, и экран входа/UAC.
	sid, err := activeSessionID()
	if err != nil {
		return nil, err
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ALL_ACCESS, &tok); err != nil {
		return nil, err
	}
	defer tok.Close()
	var dup windows.Token
	if err := windows.DuplicateTokenEx(tok, windows.MAXIMUM_ALLOWED, nil, windows.SecurityImpersonation, windows.TokenPrimary, &dup); err != nil {
		return nil, err
	}
	defer dup.Close()
	if err := windows.SetTokenInformation(dup, windows.TokenSessionId, (*byte)(unsafe.Pointer(&sid)), 4); err != nil {
		return nil, err
	}
	parts := make([]string, 0, len(args)+1)
	for _, a := range append([]string{exe}, args...) {
		parts = append(parts, windows.EscapeArg(a))
	}
	cmdline, _ := windows.UTF16PtrFromString(strings.Join(parts, " "))
	desk, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desk}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcessAsUser(dup, nil, cmdline, nil, nil, false,
		windows.CREATE_NO_WINDOW|windows.CREATE_UNICODE_ENVIRONMENT, nil, nil, &si, &pi); err != nil {
		return nil, err
	}
	windows.CloseHandle(pi.Thread)
	if j := helperJob(); j != 0 {
		windows.AssignProcessToJobObject(j, pi.Process)
	}
	hp.session = sid
	proc := pi.Process
	hp.kill = func() { windows.TerminateProcess(proc, 1) }
	go func() {
		windows.WaitForSingleObject(proc, windows.INFINITE)
		windows.CloseHandle(proc)
		close(hp.done)
	}()
	return hp, nil
}

// superviseSessionHelper держит помощник сеанса (буфер обмена, блокировка ввода)
// запущенным в активном сеансе пользователя: перезапускает при выходе из системы,
// смене пользователя и сбоях.
func (a *Agent) superviseSessionHelper(stop <-chan struct{}) {
	var hp *helperProc
	defer func() {
		if hp != nil && !hp.exited() {
			hp.kill()
		}
	}()
	fails := 0
	for {
		want, err := activeSessionID()
		if currentSessionID() != 0 {
			want, err = currentSessionID(), nil
		}
		if hp != nil && (hp.exited() || (err == nil && hp.session != want)) {
			if !hp.exited() {
				hp.kill()
			}
			hp = nil
		}
		if hp == nil && err == nil && a.cfg.path != "" {
			p, serr := startHelper([]string{"agent", "session-helper", "-config", a.cfg.path, "-hub", a.current()})
			if serr != nil {
				fails++
				if fails%20 == 1 {
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
