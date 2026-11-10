//go:build !windows

package node

import "syscall"

func hiddenAttr() *syscall.SysProcAttr { return nil }
