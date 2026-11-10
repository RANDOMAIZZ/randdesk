//go:build !windows

package zt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// install ставит ZeroTier One официальным сценарием установки (нужны права root)
// и включает его службу.
func install(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("нужны права root")
	}
	script, err := download(ctx, "https://install.zerotier.com", "install.sh")
	if err != nil {
		return err
	}
	defer os.RemoveAll(filepath.Dir(script))
	if out, err := exec.CommandContext(ctx, "bash", script).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, lastLines(string(out)))
	}
	exec.CommandContext(ctx, "systemctl", "enable", "--now", "zerotier-one").Run()
	return nil
}

func lastLines(s string) string {
	if len(s) > 600 {
		s = "…" + s[len(s)-600:]
	}
	return s
}
