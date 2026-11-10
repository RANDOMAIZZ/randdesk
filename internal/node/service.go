package node

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"randdesk/internal/proto"
	"randdesk/internal/ui"
)

const serviceName = "RandDesk"

func dataDir() string {
	if runtime.GOOS != "windows" {
		return "/var/lib/randdesk"
	}
	d := os.Getenv("ProgramData")
	if d == "" {
		d = `C:\ProgramData`
	}
	return filepath.Join(d, "RandDesk")
}

func installedExe() string {
	return filepath.Join(os.Getenv("ProgramFiles"), "RandDesk", "randdesk.exe")
}

type program struct {
	o    Options
	stop chan struct{}
	done chan struct{}
}

func (p *program) Start(service.Service) error {
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(p.done)
		if err := Run(p.o, p.stop); err != nil {
			log.Printf("RandDesk: %v", err)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	close(p.stop)
	select {
	case <-p.done:
	case <-time.After(8 * time.Second):
	}
	return nil
}

func svcConfig(exe string) *service.Config {
	return &service.Config{
		Name:        serviceName,
		DisplayName: "RandDesk",
		Description: "RandDesk — удалённое управление своими ПК",
		Executable:  exe,
		Arguments:   []string{"node", "run"},
		Option: service.KeyValue{
			"StartType":              "automatic",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
			"OnFailureResetPeriod":   60,
		},
	}
}

// Installed — установлена ли служба RandDesk на этом ПК.
func Installed() bool {
	s, err := service.New(&program{}, svcConfig(""))
	if err != nil {
		return false
	}
	st, err := s.Status()
	return err == nil && st != service.StatusUnknown
}

// Main — команда «randdesk node …».
func Main(args []string) {
	sub := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("node "+sub, flag.ExitOnError)
	o := Options{}
	fs.StringVar(&o.Dir, "dir", dataDir(), "папка с данными")
	fs.IntVar(&o.Port, "port", 8420, "порт веб-интерфейса")
	fs.IntVar(&o.UDPPort, "udp", 8421, "порт UDP для поиска ПК в сети")
	fs.Parse(args)
	switch sub {
	case "run":
		prg := &program{o: o}
		s, err := service.New(prg, svcConfig(""))
		if err != nil {
			ui.Fatal(err)
		}
		if service.Interactive() {
			runConsole(o)
			return
		}
		setupLog(o.Dir)
		log.Printf("служба RandDesk %s запущена", proto.Version)
		if err := s.Run(); err != nil {
			log.Fatal(err)
		}
	case "console":
		runConsole(o)
	case "install":
		Install()
	case "uninstall":
		Uninstall()
	default:
		ui.Fatalf("неизвестная команда node %q", sub)
	}
}

func runConsole(o Options) {
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; close(stop) }()
	if err := Run(o, stop); err != nil {
		ui.Fatal(err)
	}
}

func setupLog(dir string) {
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "randdesk.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		os.Rename(path, path+".old")
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(f)
	}
}

func hidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = hiddenAttr()
	return cmd.Run()
}

// Install копирует программу в Program Files, ставит службу, открывает доступ в брандмауэре
// и открывает страницу настройки в браузере. Требует прав администратора.
func Install() {
	if !ui.IsElevated() {
		ui.Fatal("установка требует прав администратора")
	}
	fmt.Println("Установка RandDesk…")
	dst := installedExe()
	s, err := service.New(&program{}, svcConfig(dst))
	if err != nil {
		ui.Fatal(err)
	}
	if st, err := s.Status(); err == nil && st != service.StatusUnknown {
		s.Stop()
		time.Sleep(time.Second)
		s.Uninstall()
	}
	// прежняя версия ставила отдельную службу агента — убираем, чтобы не мешала
	if old, err := service.New(&program{}, &service.Config{Name: "RandDeskAgent"}); err == nil {
		if st, err := old.Status(); err == nil && st != service.StatusUnknown {
			old.Stop()
			old.Uninstall()
		}
	}
	self, err := os.Executable()
	if err != nil {
		ui.Fatal(err)
	}
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(dst)) {
		if err := copyFile(self, dst); err != nil {
			ui.Fatalf("копирование в %s: %v", dst, err)
		}
	}
	hidden("netsh", "advfirewall", "firewall", "delete", "rule", "name=RandDesk")
	if err := hidden("netsh", "advfirewall", "firewall", "add", "rule", "name=RandDesk", "dir=in", "action=allow",
		"program="+dst, "enable=yes", "profile=any"); err != nil {
		log.Printf("брандмауэр: %v (разрешите RandDesk вручную, если другие ПК его не видят)", err)
	}
	if err := s.Install(); err != nil {
		ui.Fatalf("установка службы: %v", err)
	}
	if err := s.Start(); err != nil {
		ui.Fatalf("запуск службы: %v", err)
	}
	fmt.Println("Служба RandDesk установлена и запущена.")
	url := "http://localhost:8420"
	for range 40 { // ждём, пока страница начнёт отвечать
		if r, err := http.Get(url + "/api/state"); err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	ui.OpenBrowser(url)
	fmt.Println("\nГотово! В открывшемся окне браузера нажмите «Настроить ПК».")
	fmt.Println("Если браузер не открылся, откройте адрес " + url)
	ui.Pause()
}

// Uninstall удаляет службу, правило брандмауэра и файлы программы (данные остаются).
func Uninstall() {
	if !ui.IsElevated() {
		ui.Fatal("удаление требует прав администратора")
	}
	s, err := service.New(&program{}, svcConfig(""))
	if err != nil {
		ui.Fatal(err)
	}
	s.Stop()
	if err := s.Uninstall(); err != nil {
		log.Printf("удаление службы: %v", err)
	}
	hidden("netsh", "advfirewall", "firewall", "delete", "rule", "name=RandDesk")
	fmt.Println("RandDesk удалён с этого ПК. Настройки остались в " + dataDir() + " — удалите папку, если они не нужны.")
	ui.Pause()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
