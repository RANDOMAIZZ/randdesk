package node

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// openFirewall разрешает входящие подключения RandDesk (TCP 8420 — страница и связь с ПК,
// UDP 8421 — поиск ПК в сети).
func openFirewall(exe string) error {
	if runtime.GOOS == "windows" {
		hidden("netsh", "advfirewall", "firewall", "delete", "rule", "name=RandDesk")
		return hidden("netsh", "advfirewall", "firewall", "add", "rule", "name=RandDesk", "dir=in", "action=allow",
			"program="+exe, "enable=yes", "profile=any")
	}
	var errs []string
	if active("ufw", "status") {
		for _, p := range []string{"8420/tcp", "8421/udp"} {
			if err := exec.Command("ufw", "allow", p).Run(); err != nil {
				errs = append(errs, "ufw "+p+": "+err.Error())
			}
		}
	}
	if active("firewall-cmd", "--state") {
		if err := exec.Command("firewall-cmd", "--permanent", "--add-port=8420/tcp", "--add-port=8421/udp").Run(); err != nil {
			errs = append(errs, "firewalld: "+err.Error())
		} else {
			exec.Command("firewall-cmd", "--reload").Run()
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func closeFirewall() {
	if runtime.GOOS == "windows" {
		hidden("netsh", "advfirewall", "firewall", "delete", "rule", "name=RandDesk")
		return
	}
	if active("ufw", "status") {
		exec.Command("ufw", "delete", "allow", "8420/tcp").Run()
		exec.Command("ufw", "delete", "allow", "8421/udp").Run()
	}
	if active("firewall-cmd", "--state") {
		exec.Command("firewall-cmd", "--permanent", "--remove-port=8420/tcp", "--remove-port=8421/udp").Run()
		exec.Command("firewall-cmd", "--reload").Run()
	}
}

// active — установлен ли и включён ли этот брандмауэр.
func active(name string, args ...string) bool {
	if _, err := exec.LookPath(name); err != nil {
		return false
	}
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return false
	}
	s := strings.ToLower(string(out))
	return strings.Contains(s, "active") && !strings.Contains(s, "inactive") || strings.Contains(s, "running")
}
