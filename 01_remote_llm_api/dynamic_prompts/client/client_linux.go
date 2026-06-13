//go:build linux

package main

import (
	"os/exec"
)

func setSysProcAttrForFakeUpdate(cmd *exec.Cmd) {}

func setSysProcAttrForDaemon(cmd *exec.Cmd) {}

func setSysProcAttrForExec(cmd *exec.Cmd) {}
