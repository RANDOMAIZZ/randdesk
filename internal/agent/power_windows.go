//go:build windows

package agent

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

var procSetSuspendState = windows.NewLazySystemDLL("powrprof.dll").NewProc("SetSuspendState")

func runHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", name, msg)
	}
	return nil
}

func enablePrivilege(name string) error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return err
	}
	defer tok.Close()
	var luid windows.LUID
	np, _ := windows.UTF16PtrFromString(name)
	if err := windows.LookupPrivilegeValue(nil, np, &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	return windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
}

func power(action string) error {
	switch action {
	case "shutdown":
		return runHidden("shutdown.exe", "/s", "/t", "5", "/c", "RandDesk: удалённое выключение")
	case "reboot":
		return runHidden("shutdown.exe", "/r", "/t", "5", "/c", "RandDesk: удалённая перезагрузка")
	case "cancel":
		return runHidden("shutdown.exe", "/a")
	case "sleep", "hibernate":
		if err := enablePrivilege("SeShutdownPrivilege"); err != nil {
			return err
		}
		hib := uintptr(0)
		if action == "hibernate" {
			hib = 1
		}
		go func() { // даём ответу уйти в хаб до засыпания
			time.Sleep(2 * time.Second)
			procSetSuspendState.Call(hib, 0, 0)
		}()
		return nil
	}
	return fmt.Errorf("неизвестное действие %q", action)
}
