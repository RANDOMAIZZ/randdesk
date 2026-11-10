//go:build windows

package agent

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// protectFile оставляет доступ к файлу с токеном только SYSTEM и администраторам
// (если агент запущен с повышенными правами).
func protectFile(path string) {
	if !isElevated() {
		return
	}
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*S-1-5-18:F", "*S-1-5-32-544:F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Run()
}
