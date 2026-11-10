package agent

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/kardianos/service"

	"randsec/internal/desktop"
	"randsec/internal/proto"
	"randsec/internal/ui"
)

type Config struct {
	Hub         string   `json:"hub"`                   // основной адрес (для совместимости с прежними версиями)
	Hubs        []string `json:"hubs,omitempty"`        // все адреса хаба по порядку предпочтения
	Fingerprint string   `json:"fingerprint,omitempty"` // закреплённый отпечаток сертификата хаба
	DeviceID    string   `json:"device_id"`
	Token       string   `json:"token"`
	path        string   // откуда загружена (не сохраняется)
}

func (c *Config) hubList() []string {
	if len(c.Hubs) > 0 {
		return c.Hubs
	}
	if c.Hub != "" {
		return []string{c.Hub}
	}
	return nil
}

func (c *Config) setHubs(h []string) {
	c.Hubs = h
	if len(h) > 0 {
		c.Hub = h[0]
	}
}

func Main(args []string) {
	sub := "console"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("agent "+sub, flag.ExitOnError)
	hubURL := fs.String("hub", "", "адрес(а) хаба через запятую, например https://192.168.1.5:8420,https://myhome.duckdns.org:8420")
	key := fs.String("key", "", "ключ привязки из веб-интерфейса хаба")
	fpFlag := fs.String("fp", "", "отпечаток сертификата хаба (из веб-интерфейса) — защита от подмены хаба")
	cfgPath := fs.String("config", defaultConfigPath(), "файл конфигурации агента")
	sessionID := fs.String("session", "", "служебный параметр помощника рабочего стола")
	indicator := fs.String("indicator", "top", "служебный параметр: положение плашки на экране")
	fs.Parse(args)
	if abs, err := filepath.Abs(*cfgPath); err == nil {
		*cfgPath = abs
	}
	fp, err := normFingerprint(*fpFlag)
	if err != nil {
		ui.Fatal(err)
	}

	switch sub {
	case "desktop-helper":
		cfg, err := loadConfig(*cfgPath)
		if err != nil {
			ui.Fatalf("конфигурация %s: %v", *cfgPath, err)
		}
		cfg.path = *cfgPath
		if *hubURL != "" { // адрес, через который сейчас подключён агент, — первым
			pref := normalizeHub(*hubURL, cfg.Fingerprint != "")
			hubs := []string{pref}
			for _, u := range cfg.hubList() {
				if u != pref {
					hubs = append(hubs, u)
				}
			}
			cfg.Hubs = hubs
		}
		desktop.SetIndicatorPos(*indicator)
		runDesktopHelper(cfg, *sessionID)
		return
	case "session-helper":
		cfg, err := loadConfig(*cfgPath)
		if err != nil {
			ui.Fatalf("конфигурация %s: %v", *cfgPath, err)
		}
		cfg.path = *cfgPath
		if *hubURL != "" {
			pref := normalizeHub(*hubURL, cfg.Fingerprint != "")
			hubs := []string{pref}
			for _, u := range cfg.hubList() {
				if u != pref {
					hubs = append(hubs, u)
				}
			}
			cfg.Hubs = hubs
		}
		runSessionHelper(cfg)
		return
	case "console":
		cfg := ensureConfig(*cfgPath, *hubURL, *key, fp)
		runConsole(cfg)
	case "run":
		cfg := ensureConfig(*cfgPath, *hubURL, *key, fp)
		runService(cfg, *cfgPath)
	case "install":
		if !isElevated() {
			ui.Fatal("установка службы требует прав администратора: запустите PowerShell от имени администратора")
		}
		cfg := ensureConfig(*cfgPath, *hubURL, *key, fp)
		installService(cfg, *cfgPath)
	case "uninstall":
		uninstallService()
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда agent %q\n", sub)
		os.Exit(2)
	}
}

// LoadConfigFile загружает конфигурацию агента и запоминает её путь (для сохранения изменений).
func LoadConfigFile(path string) (*Config, error) {
	c, err := loadConfig(path)
	if err != nil {
		return nil, err
	}
	c.path = path
	return c, nil
}

// SaveConfigFile сохраняет конфигурацию агента с защитой доступа (SYSTEM и администраторы).
func SaveConfigFile(path string, c *Config) error {
	c.path = path
	return saveConfig(path, c)
}

// TLSConfig — проверка хаба по закреплённому отпечатку.
func TLSConfig(fp string) *tls.Config { return tlsConfig(fp) }

// ProtectFile оставляет доступ к файлу только SYSTEM и администраторам.
func ProtectFile(path string) { protectFile(path) }

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func saveConfig(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	protectFile(path)
	return nil
}

// ensureConfig загружает конфигурацию или привязывает ПК к хабу по ключу.
func ensureConfig(path, hubArg, key, fp string) *Config {
	cfg := ensureConfigInner(path, hubArg, key, fp)
	cfg.path = path
	return cfg
}

func ensureConfigInner(path, hubArg, key, fp string) *Config {
	cfg, err := loadConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		ui.Fatalf("не удалось прочитать %s: %v", path, err)
	}
	if fp == "" && cfg != nil && key == "" {
		fp = cfg.Fingerprint
	}
	hubs := parseHubs(hubArg, fp)
	if key != "" {
		if len(hubs) == 0 && cfg != nil {
			hubs = cfg.hubList()
		}
		if len(hubs) == 0 {
			ui.Fatal("укажите адрес хаба: -hub https://IP:8420")
		}
		var resp *proto.EnrollResp
		var errs []string
		for _, u := range hubs {
			if resp, err = enroll(u, key, fp); err == nil {
				break
			}
			errs = append(errs, u+": "+err.Error())
		}
		if resp == nil {
			ui.Fatalf("привязка не удалась:\n  %s", strings.Join(errs, "\n  "))
		}
		cfg = &Config{Fingerprint: fp, DeviceID: resp.DeviceID, Token: resp.Token}
		cfg.setHubs(hubs)
		if err := saveConfig(path, cfg); err != nil {
			ui.Fatalf("не удалось сохранить конфигурацию: %v", err)
		}
		log.Printf("ПК привязан к хабу %s (id %s), конфигурация: %s", strings.Join(hubs, ", "), cfg.DeviceID, path)
		if fp == "" {
			for _, u := range hubs {
				if strings.HasPrefix(u, "http://") {
					log.Printf("внимание: %s — незашифрованное подключение; через интернет используйте https:// и -fp", u)
					break
				}
			}
		}
		return cfg
	}
	if cfg == nil {
		ui.Fatalf("ПК ещё не привязан: укажите -hub и -key (конфигурация %s не найдена)", path)
	}
	changed := false
	if len(hubs) > 0 && strings.Join(hubs, ",") != strings.Join(cfg.hubList(), ",") {
		cfg.setHubs(hubs)
		changed = true
	}
	if fp != cfg.Fingerprint {
		cfg.Fingerprint = fp
		changed = true
	}
	if changed {
		if err := saveConfig(path, cfg); err != nil {
			ui.Fatalf("не удалось сохранить конфигурацию: %v", err)
		}
		log.Printf("адреса хаба обновлены: %s", strings.Join(cfg.hubList(), ", "))
	}
	return cfg
}

func enroll(hubURL, key, fp string) (*proto.EnrollResp, error) {
	host, _ := os.Hostname()
	body, _ := json.Marshal(proto.EnrollReq{Key: key, Hostname: host})
	r, err := httpClient(fp).Post(hubURL+"/api/agent/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		if errors.Is(err, errPin) {
			return nil, errPin
		}
		return nil, err
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if r.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = r.Status
		}
		return nil, errors.New(e.Error)
	}
	var resp proto.EnrollResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func runConsole(cfg *Config) {
	log.Printf("RANDSEC agent %s, хаб %s. Ctrl+C — выход.", proto.Version, strings.Join(cfg.hubList(), ", "))
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; close(stop) }()
	New(cfg).Run(stop)
}

// ---------- служба Windows ----------

type program struct {
	cfg  *Config
	stop chan struct{}
	done chan struct{}
}

func (p *program) Start(service.Service) error {
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	go func() {
		New(p.cfg).Run(p.stop)
		close(p.done)
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	close(p.stop)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

func serviceConfig(cfgPath, exe string) *service.Config {
	return &service.Config{
		Name:        "RandDeskAgent",
		DisplayName: "RANDSEC Agent",
		Description: "Агент удалённого управления RANDSEC",
		Executable:  exe,
		Arguments:   []string{"agent", "run", "-config", cfgPath},
		Option: service.KeyValue{
			"StartType":              "automatic",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "10s",
			"OnFailureResetPeriod":   60,
		},
	}
}

func runService(cfg *Config, cfgPath string) {
	prg := &program{cfg: cfg}
	s, err := service.New(prg, serviceConfig(cfgPath, ""))
	if err != nil {
		ui.Fatal(err)
	}
	if service.Interactive() {
		runConsole(cfg)
		return
	}
	setupServiceLog()
	log.Printf("служба RANDSEC agent %s запущена, хаб %s", proto.Version, strings.Join(cfg.hubList(), ", "))
	if err := s.Run(); err != nil {
		ui.Fatal(err)
	}
}

func setupServiceLog() {
	path := filepath.Join(programDataDir(), "RandDesk", "agent.log")
	os.MkdirAll(filepath.Dir(path), 0o700)
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		os.Rename(path, path+".old")
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(f)
	}
}

func installService(cfg *Config, cfgPath string) {
	dst := filepath.Join(os.Getenv("ProgramFiles"), "RandDesk", "randdesk.exe")
	s, err := service.New(&program{cfg: cfg}, serviceConfig(cfgPath, dst))
	if err != nil {
		ui.Fatal(err)
	}
	if st, err := s.Status(); err == nil && st != service.StatusUnknown {
		s.Stop()
		time.Sleep(time.Second)
		if err := s.Uninstall(); err != nil {
			log.Printf("удаление старой службы: %v", err)
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
	if err := s.Install(); err != nil {
		ui.Fatalf("установка службы: %v", err)
	}
	if err := s.Start(); err != nil {
		ui.Fatalf("запуск службы: %v", err)
	}
	log.Printf("служба RANDSECAgent установлена и запущена (%s)", dst)
	ui.Pause()
}

func uninstallService() {
	s, err := service.New(&program{}, serviceConfig(defaultConfigPath(), ""))
	if err != nil {
		ui.Fatal(err)
	}
	s.Stop()
	if err := s.Uninstall(); err != nil {
		ui.Fatalf("удаление службы: %v", err)
	}
	log.Print("служба RANDSECAgent удалена")
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

func defaultConfigPath() string {
	if isElevated() {
		return filepath.Join(programDataDir(), "RandDesk", "agent.json")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "RandDesk", "agent.json")
}

func programDataDir() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}
