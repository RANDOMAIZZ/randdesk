//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"randsec/internal/proto"
)

func isElevated() bool { return os.Geteuid() == 0 }

// protectFile оставляет доступ к файлу только владельцу (root).
func protectFile(path string) { os.Chmod(path, 0o600) }

// ---------- процессы ----------

type procCache struct {
	mu    sync.Mutex
	procs map[int32]*process.Process // объекты храним между вызовами — так считается загрузка CPU
}

func newProcCache() *procCache { return &procCache{procs: map[int32]*process.Process{}} }

func (c *procCache) list() ([]proto.Proc, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list, err := process.Processes()
	if err != nil {
		return nil, err
	}
	alive := map[int32]bool{}
	out := make([]proto.Proc, 0, len(list))
	for _, p := range list {
		alive[p.Pid] = true
		if old, ok := c.procs[p.Pid]; ok {
			p = old
		} else {
			c.procs[p.Pid] = p
		}
		name, _ := p.Name()
		ppid, _ := p.Ppid()
		user, _ := p.Username()
		cpu, _ := p.Percent(0)
		var mem uint64
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			mem = mi.RSS
		}
		th, _ := p.NumThreads()
		out = append(out, proto.Proc{PID: uint32(p.Pid), PPID: uint32(ppid), Name: name, User: user, CPU: cpu, Mem: mem, Threads: uint32(th)})
	}
	for pid := range c.procs {
		if !alive[pid] {
			delete(c.procs, pid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

func killProcess(pid uint32, tree bool) error {
	if pid <= 1 {
		return errors.New("системный процесс нельзя завершить")
	}
	if pid == uint32(os.Getpid()) {
		return errors.New("нельзя завершить сам агент RANDSEC")
	}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return err
	}
	if tree {
		var walk func(p *process.Process)
		walk = func(p *process.Process) {
			kids, _ := p.Children()
			for _, k := range kids {
				walk(k)
				k.Kill()
			}
		}
		walk(p)
	}
	return p.Kill()
}

// ---------- службы (systemd) ----------

func listServices() ([]proto.Service, error) {
	out, err := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager").Output()
	if err != nil {
		return nil, fmt.Errorf("systemctl: %w", err)
	}
	var list []proto.Service
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		name := strings.TrimSuffix(f[0], ".service")
		state := "stopped"
		switch f[2] {
		case "active":
			state = "running"
		case "activating":
			state = "starting"
		case "deactivating":
			state = "stopping"
		}
		list = append(list, proto.Service{Name: name, Display: strings.Join(f[4:], " "), State: state})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func controlService(name, action string) error {
	if strings.EqualFold(name, "randdesk") && action != "start" {
		return errors.New("службу RANDSEC нельзя остановить отсюда — связь с ПК пропадёт")
	}
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("неизвестное действие %q", action)
	}
	if strings.ContainsAny(name, " /;&|$`\\") {
		return errors.New("неверное имя службы")
	}
	if out, err := exec.Command("systemctl", action, name+".service").CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------- питание ----------

func power(action string) error {
	var args []string
	switch action {
	case "shutdown":
		args = []string{"shutdown", "-h", "+0", "RANDSEC: удалённое выключение"}
	case "reboot":
		args = []string{"shutdown", "-r", "+0", "RANDSEC: удалённая перезагрузка"}
	case "cancel":
		args = []string{"shutdown", "-c"}
	case "sleep":
		args = []string{"systemctl", "suspend"}
	case "hibernate":
		args = []string{"systemctl", "hibernate"}
	default:
		return fmt.Errorf("неизвестное действие %q", action)
	}
	if action == "sleep" || action == "hibernate" || action == "shutdown" || action == "reboot" {
		go func() { // даём ответу уйти в хаб
			time.Sleep(2 * time.Second)
			exec.Command(args[0], args[1:]...).Run()
		}()
		return nil
	}
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------- выполнение команд ----------

const maxOutput = 2 << 20

type limitedBuf struct {
	buf       strings.Builder
	truncated bool
}

func (l *limitedBuf) Write(p []byte) (int, error) {
	if room := maxOutput - l.buf.Len(); len(p) > room {
		if room > 0 {
			l.buf.Write(p[:room])
		}
		l.truncated = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

// unixShell — оболочка для команд: «powershell» и «cmd» из интерфейса на Linux означают bash/sh.
func unixShell(shell string) string {
	if _, err := os.Stat("/bin/sh"); err != nil { // Android: оболочка лежит в /system/bin
		return "/system/bin/sh"
	}
	if shell == "sh" {
		return "/bin/sh"
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}

func runExec(r proto.ExecReq) proto.ExecResp {
	timeout := time.Duration(r.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	if strings.TrimSpace(r.Command) == "" {
		return proto.ExecResp{Output: "пустая команда", ExitCode: -1}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, unixShell(r.Shell), "-c", r.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) } // вся группа процессов
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8", "TERM=dumb")
	out := &limitedBuf{}
	cmd.Stdout, cmd.Stderr = out, out
	start := time.Now()
	err := cmd.Run()
	res := proto.ExecResp{
		Duration:  time.Since(start).Milliseconds(),
		Truncated: out.truncated,
		TimedOut:  errors.Is(ctx.Err(), context.DeadlineExceeded),
		Output:    strings.ToValidUTF8(out.buf.String(), "�"),
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		res.ExitCode = -1
		res.Output += "\nошибка запуска: " + err.Error()
	}
	return res
}
