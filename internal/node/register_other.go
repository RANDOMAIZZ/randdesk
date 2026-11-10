//go:build !windows

package node

import (
	"os"

	"randdesk/internal/uninstall"
)

// writeUninstaller кладёт файл полного удаления рядом с программой: randdesk-uninstall.
func writeUninstaller(string) (string, error) {
	if err := os.WriteFile(uninstall.LinuxPath, uninstall.Linux(), 0o755); err != nil {
		return "", err
	}
	return uninstall.LinuxPath, nil
}

func uninstallerPath() string {
	if _, err := os.Stat(uninstall.LinuxPath); err != nil {
		return ""
	}
	return uninstall.LinuxPath
}

func removeUninstallEntry() {}
