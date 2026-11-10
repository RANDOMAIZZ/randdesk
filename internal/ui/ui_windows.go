//go:build windows

// Package ui — поведение консольного окна при запуске двойным щелчком.
package ui

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")

// OwnConsole — true, если консольное окно создано для этой программы
// (запуск двойным щелчком из Проводника), а не открыто заранее (PowerShell, cmd).
// После выхода такое окно сразу закрывается, поэтому сообщения нужно задержать.
func OwnConsole() bool {
	var ids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	return n == 1
}

// IsElevated — запущено ли с правами администратора.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// OpenBrowser открывает адрес в браузере по умолчанию.
func OpenBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Start()
}

// RunElevated перезапускает программу с правами администратора (запрос UAC) с указанными аргументами.
func RunElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = windows.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	dir, _ := os.Getwd()
	wd, _ := windows.UTF16PtrFromString(dir)
	return windows.ShellExecute(0, verb, file, params, wd, windows.SW_NORMAL)
}

// IsTerminal — на Windows меню показывается при запуске двойным щелчком (см. OwnConsole).
func IsTerminal() bool { return false }
