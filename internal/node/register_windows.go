//go:build windows

package node

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"

	"randdesk/internal/proto"
	"randdesk/internal/uninstall"
)

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\RandDesk`

// writeUninstaller кладёт файл полного удаления в папку программы и добавляет RandDesk
// в «Параметры → Приложения» (там его можно удалить обычной кнопкой «Удалить»).
func writeUninstaller(exe string) (string, error) {
	dir := filepath.Dir(exe)
	script := filepath.Join(dir, uninstall.WindowsName)
	if err := os.WriteFile(script, uninstall.Windows(), 0o644); err != nil {
		return "", err
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return script, err
	}
	defer k.Close()
	k.SetStringValue("DisplayName", "RandDesk")
	k.SetStringValue("DisplayVersion", proto.Version)
	k.SetStringValue("Publisher", "RandDesk")
	k.SetStringValue("DisplayIcon", exe)
	k.SetStringValue("InstallLocation", dir)
	k.SetStringValue("UninstallString", `"`+script+`"`)
	k.SetStringValue("URLInfoAbout", "https://github.com/RANDOMAIZZ/randdesk")
	k.SetDWordValue("NoModify", 1)
	k.SetDWordValue("NoRepair", 1)
	if fi, err := os.Stat(exe); err == nil {
		k.SetDWordValue("EstimatedSize", uint32(fi.Size()/1024))
	}
	return script, nil
}

// uninstallerPath — установленный файл удаления (если есть).
func uninstallerPath() string {
	p := filepath.Join(filepath.Dir(installedExe()), uninstall.WindowsName)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func removeUninstallEntry() { registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey) }
