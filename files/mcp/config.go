package main

import (
	"os"
	"path/filepath"
)

// loadRootFromEnv resolves the root directory for file operations.
// Precedence: NUSASHELL_FILES_ROOT → NUSASHELL_WORKSPACE → user home.
func loadRootFromEnv() string {
	raw := os.Getenv("NUSASHELL_FILES_ROOT")
	if raw == "" {
		raw = os.Getenv("NUSASHELL_WORKSPACE")
	}
	if raw == "" {
		home, _ := os.UserHomeDir()
		raw = home
	}
	root, err := filepath.Abs(raw)
	if err != nil {
		root = raw
	}
	return root
}

// resolvePath resolves a path relative to root. Absolute paths resolve to
// OS-absolute paths. Relative paths resolve against root. `../` traversal
// is allowed (no containment jail).
func resolvePath(root, input string) string {
	if input == "" {
		return root
	}
	if filepath.IsAbs(input) {
		return filepath.Clean(input)
	}
	return filepath.Clean(filepath.Join(root, input))
}

// relativePosix returns a workspace-relative path with POSIX separators.
func relativePosix(root, absPath, fallback string) string {
	rel, err := filepath.Rel(root, absPath)
	if err != nil || rel == "" {
		return filepath.ToSlash(fallback)
	}
	return filepath.ToSlash(rel)
}
