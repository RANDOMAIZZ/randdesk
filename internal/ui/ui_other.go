//go:build !windows

package ui

import (
	"errors"
	"os"
	"os/exec"
)

func OwnConsole() bool { return false }

// IsTerminal — программа запущена в терминале (а не службой).
func IsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func IsElevated() bool { return os.Geteuid() == 0 }

// OpenBrowser открывает адрес в браузере пользователя; от root — от имени того, кто вызвал sudo.
func OpenBrowser(url string) {
	if u := os.Getenv("SUDO_USER"); os.Geteuid() == 0 && u != "" {
		exec.Command("sudo", "-u", u, "xdg-open", url).Start()
		return
	}
	exec.Command("xdg-open", url).Start()
}

// RunElevated повторно запускает программу через sudo в этом же терминале и ждёт её завершения.
func RunElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return errors.New("нужны права root: запустите программу от root")
	}
	cmd := exec.Command("sudo", append([]string{exe}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
