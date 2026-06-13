//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

func setSysProcAttrForFakeUpdate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func setSysProcAttrForDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000,
		HideWindow:    true,
	}
}

func setSysProcAttrForExec(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
