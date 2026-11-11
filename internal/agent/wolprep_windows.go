//go:build windows

package agent

import (
	"log"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// wolPrepScript включает пробуждение по magic-пакету на всех физических сетевых адаптерах.
// Меняет только то, что выключено, и без перезапуска адаптера (-NoRestart), чтобы не рвать связь;
// драйвер применит настройку при следующем запуске/выключении.
const wolPrepScript = `
$ErrorActionPreference = 'SilentlyContinue'; $ProgressPreference = 'SilentlyContinue'
foreach ($a in Get-NetAdapter -Physical) {
  $pm = Get-NetAdapterPowerManagement -Name $a.Name
  if ($pm -and "$($pm.WakeOnMagicPacket)" -eq 'Disabled') {
    Set-NetAdapterPowerManagement -Name $a.Name -WakeOnMagicPacket Enabled -NoRestart
    "$($a.Name):magic"
  }
  # 1 — нужно включить; 0 — выключить («энергосбережение при выключении» Realtek/Intel мешает WoL)
  $want = @{ '*WakeOnMagicPacket'='1'; 'WakeOnMagicPacket'='1'; 'S5WakeOnLan'='1'; 'EnablePME'='1'; '*DeviceSleepOnDisconnect'='0' }
  foreach ($kw in $want.Keys) {
    $p = Get-NetAdapterAdvancedProperty -Name $a.Name -RegistryKeyword $kw
    if ($p -and "$($p.RegistryValue)" -ne '' -and "$($p.RegistryValue)" -ne $want[$kw]) {
      Set-NetAdapterAdvancedProperty -Name $a.Name -RegistryKeyword $kw -RegistryValue $want[$kw] -NoRestart
      "$($a.Name):$kw"
    }
  }
  powercfg /deviceenablewake "$($a.InterfaceDescription)" | Out-Null
}
`

// prepareWake готовит ПК к включению по сети: Wake-on-LAN на адаптерах и выключенный
// «быстрый запуск» (с ним Windows при выключении уходит в гибернацию и сетевая карта часто не ждёт пакет).
func prepareWake() {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Power`, registry.QUERY_VALUE|registry.SET_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("HiberbootEnabled"); err != nil || v != 0 {
			if k.SetDWordValue("HiberbootEnabled", 0) == nil {
				log.Printf("Wake-on-LAN: выключен быстрый запуск Windows")
			}
		}
		k.Close()
	}
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePS(wolPrepScript))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	b, err := cmd.Output() // только stdout: в stderr PowerShell пишет служебный CLIXML
	out := string(b)
	if err != nil {
		log.Printf("Wake-on-LAN: настройка адаптеров: %v", err)
		return
	}
	if s := strings.TrimSpace(out); s != "" {
		log.Printf("Wake-on-LAN включён: %s", strings.Join(strings.Fields(s), ", "))
	}
}
