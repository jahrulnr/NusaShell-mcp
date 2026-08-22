package main

import (
	"os"
	"path/filepath"
)

func userHomeDir() (string, error) {
	return os.UserHomeDir()
}

func isAbs(p string) bool {
	return filepath.IsAbs(p)
}

// exitCodeOr dereferences a *int exit code for display. Returns nil when
// the process is still running (no exit code yet); otherwise returns the
// plain int value. This prevents fmt %v from printing the pointer address
// (e.g. 0x1815aac77f0) instead of the actual code.
func exitCodeOr(code *int) any {
	if code == nil {
		return nil
	}
	return *code
}
