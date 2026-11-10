// Package uninstall — файлы полного удаления RandDesk, которые установщик кладёт рядом с программой.
package uninstall

import (
	_ "embed"
	"strings"
)

//go:embed uninstall-randdesk.cmd
var windowsScript string

//go:embed uninstall-randdesk.sh
var linuxScript string

// WindowsName и LinuxName — имена файлов удаления.
const (
	WindowsName = "uninstall-randdesk.cmd"
	LinuxPath   = "/usr/local/bin/randdesk-uninstall"
)

// Windows — сценарий для Windows (строки CRLF, как требует cmd.exe).
func Windows() []byte {
	s := strings.ReplaceAll(windowsScript, "\r\n", "\n")
	return []byte(strings.ReplaceAll(s, "\n", "\r\n"))
}

// Linux — сценарий для Linux (строки LF).
func Linux() []byte { return []byte(strings.ReplaceAll(linuxScript, "\r\n", "\n")) }
