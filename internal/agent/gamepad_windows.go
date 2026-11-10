//go:build windows

package agent

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"randsec/internal/session"
)

// Геймпады отключаются как устройства в диспетчере устройств: Xbox-контроллеры
// (классы XnaComposite/XboxComposite) и HID-устройства «игровой контроллер».
// Отключение сохраняется и после перезагрузки, пока администратор не включит их обратно.
const gamepadFilter = `$devs = Get-PnpDevice -ErrorAction SilentlyContinue | Where-Object {
  $_.Class -in 'XnaComposite','XboxComposite' -or
  ($_.Class -eq 'HIDClass' -and $_.FriendlyName -match 'game controller|игров|gamepad|xbox|dualsense|dualshock|wireless controller|joystick') }`

func setGamepads(disable bool) error {
	action := `$devs | Where-Object { $_.Status -eq 'OK' } | ForEach-Object { Disable-PnpDevice -InstanceId $_.InstanceId -Confirm:$false -ErrorAction SilentlyContinue; $n++ }`
	if !disable {
		action = `$devs | Where-Object { $_.Problem -eq 'CM_PROB_DISABLED' -or $_.ConfigManagerErrorCode -eq 22 } | ForEach-Object { Enable-PnpDevice -InstanceId $_.InstanceId -Confirm:$false -ErrorAction SilentlyContinue; $n++ }`
	}
	script := "$n = 0; " + gamepadFilter + "; " + action + "; Write-Output $n"
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePS(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	verb := "включено"
	if disable {
		verb = "отключено"
	}
	log.Printf("геймпады: %s устройств — %s", verb, strings.TrimSpace(string(out)))
	return nil
}

// runSessionHelper — процесс в сеансе пользователя: блокировка ввода и буфер обмена.
func runSessionHelper(cfg *Config) {
	a := New(cfg)
	backoff := 2 * time.Second
	for {
		ws, err := a.connectPath("/api/agent/session")
		if err == nil {
			backoff = 2 * time.Second
			session.Serve(ws)
			ws.Close()
		} else if backoff >= 30*time.Second {
			log.Printf("помощник сеанса: %v", err)
		}
		time.Sleep(backoff)
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}
