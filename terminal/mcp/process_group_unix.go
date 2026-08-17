//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func killProcessTree(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}
