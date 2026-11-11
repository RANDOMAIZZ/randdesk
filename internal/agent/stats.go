package agent

import (
	"net"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"randsec/internal/android"
	"randsec/internal/proto"
)

type statsCollector struct {
	// Android: загрузка по времени простоя ядер и трафик из сведений приложения
	idlePrev    uint64
	idleCores   int
	idleT       time.Time
	aRx, aTx    uint64
	aT          int64
	lastNetRate [2]float64

	lastRx, lastTx uint64
	lastNetT       time.Time
	disks          []proto.Disk
	disksT         time.Time
}

func newStatsCollector() *statsCollector {
	cpu.Percent(0, false) // первый замер — точка отсчёта
	return &statsCollector{}
}

func (c *statsCollector) collect() proto.Stats {
	var st proto.Stats
	if p, err := cpu.Percent(0, false); err == nil && len(p) > 0 {
		st.CPU = p[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		st.MemUsed, st.MemTotal = vm.Used, vm.Total
	}
	if android.On() {
		// трафик и память телефона — в androidStats: системные счётчики приложениям закрыты
	} else if n, err := gnet.IOCounters(false); err == nil && len(n) > 0 {
		now := time.Now()
		rx, tx := n[0].BytesRecv, n[0].BytesSent
		if !c.lastNetT.IsZero() {
			if dt := now.Sub(c.lastNetT).Seconds(); dt > 0 {
				if rx >= c.lastRx {
					st.NetRx = float64(rx-c.lastRx) / dt
				}
				if tx >= c.lastTx {
					st.NetTx = float64(tx-c.lastTx) / dt
				}
			}
		}
		c.lastRx, c.lastTx, c.lastNetT = rx, tx, now
	}
	if time.Since(c.disksT) > 30*time.Second {
		c.disks = collectDisks()
		c.disksT = time.Now()
	}
	st.Disks = c.disks
	if up, err := host.Uptime(); err == nil {
		st.Uptime = up
	}
	if pids, err := process.Pids(); err == nil {
		st.Procs = len(pids)
	}
	if android.On() {
		c.androidStats(&st)
	}
	return st
}

func collectDisks() []proto.Disk {
	if android.On() { // разделы Android приложению не видны — показываем память телефона
		if u, err := disk.Usage(os.Getenv("HOME")); err == nil && u.Total > 0 {
			return []proto.Disk{{Mount: "Память", FS: "", Used: u.Used, Total: u.Total}}
		}
		return nil
	}
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil
	}
	var out []proto.Disk
	for _, p := range parts {
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		out = append(out, proto.Disk{Mount: p.Mountpoint, FS: p.Fstype, Used: u.Used, Total: u.Total})
	}
	return out
}

func collectSysInfo() proto.SysInfo {
	info := proto.SysInfo{AgentVersion: proto.Version}
	if h, err := host.Info(); err == nil {
		info.Hostname = h.Hostname
		info.OS = h.OS
		info.Platform = h.Platform
		info.PlatformVersion = h.PlatformVersion
		info.Arch = h.KernelArch
		info.BootTime = h.BootTime
	}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		info.CPUModel = strings.TrimSpace(ci[0].ModelName)
	}
	info.CPUCores, _ = cpu.Counts(false)
	info.CPUThreads, _ = cpu.Counts(true)
	if vm, err := mem.VirtualMemory(); err == nil {
		info.MemTotal = vm.Total
	}
	if u, err := user.Current(); err == nil {
		info.AgentUser = u.Username
	}
	if android.On() {
		info.Hostname = android.Hostname()
		info.OS = "android"
		if ai := android.ReadInfo(); ai != nil {
			info.Platform = strings.TrimSpace(ai.Manufacturer + " " + ai.Model)
			info.PlatformVersion = "Android " + ai.Android
		}
	}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) != 6 {
			continue
		}
		nic := proto.NIC{Name: ifc.Name, MAC: ifc.HardwareAddr.String()}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				nic.IPs = append(nic.IPs, ipn.IP.String())
			}
		}
		info.NICs = append(info.NICs, nic)
	}
	return info
}

func (c *statsCollector) androidStats(st *proto.Stats) {
	if idle, cores := android.CPUIdle(); cores > 0 {
		now := time.Now()
		if !c.idleT.IsZero() && cores == c.idleCores && idle >= c.idlePrev {
			if total := float64(now.Sub(c.idleT).Microseconds()) * float64(cores); total > 0 {
				st.CPU = max(0, min(100, 100*(1-float64(idle-c.idlePrev)/total)))
			}
		}
		c.idlePrev, c.idleCores, c.idleT = idle, cores, now
	}
	ai := android.ReadInfo()
	if ai == nil {
		return
	}
	if ai.Battery >= 0 {
		st.Battery = &proto.Battery{Level: ai.Battery, Charging: ai.Charging, Temp: ai.Temp}
	}
	if ai.T > c.aT && c.aT > 0 && ai.Rx >= c.aRx && ai.Tx >= c.aTx {
		dt := float64(ai.T-c.aT) / 1000
		st.NetRx, st.NetTx = float64(ai.Rx-c.aRx)/dt, float64(ai.Tx-c.aTx)/dt
		c.lastNetRate = [2]float64{st.NetRx, st.NetTx}
	} else if ai.T == c.aT {
		st.NetRx, st.NetTx = c.lastNetRate[0], c.lastNetRate[1] // приложение ещё не обновило счётчики
	}
	if ai.T != c.aT {
		c.aRx, c.aTx, c.aT = ai.Rx, ai.Tx, ai.T
	}
}
