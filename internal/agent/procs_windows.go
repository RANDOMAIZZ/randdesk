//go:build windows

package agent

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"randdesk/internal/proto"
)

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessMemoryInfo = modkernel32.NewProc("K32GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

type cpuSample struct {
	cpu     uint64 // 100-нс интервалы (kernel+user)
	created uint64
}

type userEntry struct {
	created uint64
	name    string
}

// procCache хранит предыдущие замеры, чтобы считать загрузку CPU по процессам.
type procCache struct {
	mu    sync.Mutex
	prev  map[uint32]cpuSample
	users map[uint32]userEntry
	sids  map[string]string
	lastT time.Time
}

func newProcCache() *procCache {
	return &procCache{prev: map[uint32]cpuSample{}, users: map[uint32]userEntry{}, sids: map[string]string{}}
}

func ft64(ft windows.Filetime) uint64 { return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime) }

type snapEntry struct {
	pid, ppid, threads uint32
	name               string
}

func snapshot() ([]snapEntry, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []snapEntry
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		out = append(out, snapEntry{pe.ProcessID, pe.ParentProcessID, pe.Threads, windows.UTF16ToString(pe.ExeFile[:])})
	}
	return out, nil
}

func (c *procCache) list() ([]proto.Proc, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := snapshot()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	elapsed := uint64(now.Sub(c.lastT).Nanoseconds() / 100)
	first := c.lastT.IsZero()
	ncpu := uint64(runtime.NumCPU())
	next := make(map[uint32]cpuSample, len(entries))
	out := make([]proto.Proc, 0, len(entries))

	for _, e := range entries {
		p := proto.Proc{PID: e.pid, PPID: e.ppid, Name: e.name, Threads: e.threads}
		if e.pid == 0 {
			p.Name = "System Idle Process"
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.pid)
		if err == nil {
			var ct, et, kt, ut windows.Filetime
			if windows.GetProcessTimes(h, &ct, &et, &kt, &ut) == nil {
				s := cpuSample{cpu: ft64(kt) + ft64(ut), created: ft64(ct)}
				next[e.pid] = s
				if pr, ok := c.prev[e.pid]; ok && !first && elapsed > 0 && pr.created == s.created && s.cpu >= pr.cpu {
					p.CPU = float64(s.cpu-pr.cpu) / float64(elapsed*ncpu) * 100
				}
				p.User = c.user(h, e.pid, s.created)
			}
			var mc processMemoryCounters
			mc.CB = uint32(unsafe.Sizeof(mc))
			if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mc)), uintptr(mc.CB)); r != 0 {
				p.Mem = uint64(mc.WorkingSetSize)
			}
			windows.CloseHandle(h)
		}
		out = append(out, p)
	}
	for pid := range c.users {
		if _, ok := next[pid]; !ok {
			delete(c.users, pid)
		}
	}
	c.prev, c.lastT = next, now
	return out, nil
}

func (c *procCache) user(h windows.Handle, pid uint32, created uint64) string {
	if u, ok := c.users[pid]; ok && u.created == created {
		return u.name
	}
	name := ""
	var tok windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok) == nil {
		if tu, err := tok.GetTokenUser(); err == nil {
			sid := tu.User.Sid.String()
			if n, ok := c.sids[sid]; ok {
				name = n
			} else if acc, _, _, err := tu.User.Sid.LookupAccount(""); err == nil {
				name = acc
				c.sids[sid] = acc
			}
		}
		tok.Close()
	}
	c.users[pid] = userEntry{created: created, name: name}
	return name
}

func killProcess(pid uint32, tree bool) error {
	if pid == 0 || pid == 4 {
		return errors.New("системный процесс нельзя завершить")
	}
	if pid == uint32(os.Getpid()) {
		return errors.New("нельзя завершить сам агент RandDesk")
	}
	targets := []uint32{pid}
	if tree {
		entries, err := snapshot()
		if err != nil {
			return err
		}
		children := map[uint32][]uint32{}
		for _, e := range entries {
			if e.pid != e.ppid {
				children[e.ppid] = append(children[e.ppid], e.pid)
			}
		}
		seen := map[uint32]bool{pid: true}
		for i := 0; i < len(targets); i++ {
			for _, ch := range children[targets[i]] {
				if !seen[ch] && ch != uint32(os.Getpid()) {
					seen[ch] = true
					targets = append(targets, ch)
				}
			}
		}
	}
	var firstErr error
	for i := len(targets) - 1; i >= 0; i-- { // сначала потомки
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, targets[i])
		if err == nil {
			err = windows.TerminateProcess(h, 1)
			windows.CloseHandle(h)
		}
		if err != nil && targets[i] == pid {
			firstErr = fmt.Errorf("не удалось завершить PID %d: %w", pid, err)
		}
	}
	return firstErr
}
