//go:build windows

package node

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func hiddenAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
