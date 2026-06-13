//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func isAdmin() bool {
	return os.Geteuid() == 0
}

func relaunchAsAdmin() {}

func setSysProcAttrForFakeUpdate(cmd *exec.Cmd) {}

func setSysProcAttrForDaemon(cmd *exec.Cmd) {}

func setCmdSysProcAttr(cmd *exec.Cmd) {}

func ensureCurl() {
	_, err := exec.LookPath("curl")
	if err == nil {
		return
	}
	fmt.Println("curl not found, installing...")
	cmd := exec.Command("apt", "install", "curl", "-y")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("Failed to install curl:", err)
	}
}