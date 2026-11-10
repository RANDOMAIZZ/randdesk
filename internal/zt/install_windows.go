//go:build windows

package zt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// install скачивает официальный установщик ZeroTier One и ставит его без вопросов.
func install(ctx context.Context) error {
	msi, err := download(ctx, "https://download.zerotier.com/dist/ZeroTier%20One.msi", "ZeroTierOne.msi")
	if err != nil {
		return err
	}
	defer os.RemoveAll(filepath.Dir(msi))
	cmd := exec.CommandContext(ctx, "msiexec.exe", "/i", msi, "/qn", "/norestart")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("msiexec: %v %s", err, out)
	}
	return nil
}
