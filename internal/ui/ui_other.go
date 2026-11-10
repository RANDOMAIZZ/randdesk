//go:build !windows

package ui

import (
	"errors"
	"os"
	"os/exec"
)

func OwnConsole() bool { return false }

func IsElevated() bool { return os.Geteuid() == 0 }

func OpenBrowser(url string) { exec.Command("xdg-open", url).Start() }

func RunElevated([]string) error {
	return errors.New("запустите с правами администратора")
}
