//go:build windows

package main

import (
	"os"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) error {
	// Windows process-tree cleanup is handled by the process itself for now.
	// A Job Object can be introduced here without changing the MCP API.
	return nil
}

func killProcessTree(p *os.Process) error {
	return p.Kill()
}
