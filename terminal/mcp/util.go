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
