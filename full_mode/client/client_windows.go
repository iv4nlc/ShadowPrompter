//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	return err == nil && member
}

func relaunchAsAdmin() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Println("Error getting executable path:", err)
		fmt.Println("Press Enter to exit...")
		fmt.Scanln()
		os.Exit(1)
	}
	verb := "runas"
	cwd, _ := os.Getwd()
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.Dir = cwd
	cmd.Args = os.Args
	cmd.Env = os.Environ()
	err = windows.ShellExecute(0, windows.StringToUTF16Ptr(verb),
		windows.StringToUTF16Ptr(exe),
		windows.StringToUTF16Ptr(""),
		windows.StringToUTF16Ptr(cwd),
		windows.SW_SHOWNORMAL)
	if err != nil {
		fmt.Println("Error requesting admin privileges:", err)
		fmt.Println("Press Enter to exit...")
		fmt.Scanln()
		os.Exit(1)
	}
	os.Exit(0)
}

func setSysProcAttrForFakeUpdate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func setSysProcAttrForDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000,
		HideWindow:    true,
	}
}

func setCmdSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func ensureCurl() {}