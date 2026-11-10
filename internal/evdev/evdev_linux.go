//go:build linux

// Package evdev отключает физические устройства ввода Linux (клавиатуры, мыши, геймпады),
// захватывая их монопольно (EVIOCGRAB): события перестают доходить до системы и программ.
// Удалённый ввод RandDesk (XTest) идёт мимо этих устройств и продолжает работать.
package evdev

import (
	"log"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Grabber держит захваченными устройства нужных типов, подхватывая и вновь подключённые.
type Grabber struct {
	mu   sync.Mutex
	want Kind
	held map[string]*os.File
	stop chan struct{}
}

func NewGrabber() *Grabber { return &Grabber{held: map[string]*os.File{}} }

// Set задаёт, какие типы устройств отключить (0 — включить все обратно).
func (g *Grabber) Set(k Kind) {
	g.mu.Lock()
	g.want = k
	if k != 0 && g.stop == nil {
		g.stop = make(chan struct{})
		go g.loop(g.stop)
	}
	if k == 0 && g.stop != nil {
		close(g.stop)
		g.stop = nil
	}
	g.mu.Unlock()
	g.sync()
}

func (g *Grabber) loop(stop chan struct{}) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			g.sync()
		}
	}
}

const eviocgrab = 0x40044590 // _IOW('E', 0x90, int)

func scan() []device {
	f, err := os.Open("/proc/bus/input/devices")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseDevices(f)
}

func (g *Grabber) sync() {
	g.mu.Lock()
	defer g.mu.Unlock()
	present := map[string]bool{}
	for _, d := range scan() {
		present[d.path] = true
		_, held := g.held[d.path]
		need := g.want&d.kind != 0
		switch {
		case need && !held:
			f, err := os.OpenFile(d.path, os.O_RDONLY|unix.O_NONBLOCK, 0)
			if err != nil {
				continue
			}
			if err := unix.IoctlSetInt(int(f.Fd()), eviocgrab, 1); err != nil {
				f.Close()
				continue
			}
			g.held[d.path] = f
			log.Printf("ввод: отключено устройство «%s»", d.name)
		case !need && held:
			g.release(d.path)
		}
	}
	for p := range g.held {
		if !present[p] {
			g.release(p)
		}
	}
}

func (g *Grabber) release(path string) {
	if f := g.held[path]; f != nil {
		unix.IoctlSetInt(int(f.Fd()), eviocgrab, 0)
		f.Close()
	}
	delete(g.held, path)
}

// Count — сколько устройств сейчас отключено.
func (g *Grabber) Count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.held)
}
