package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// resolvePath resolves an ABSOLUTE filesystem path. Relative paths are
// rejected: the Files MCP server is shared between concurrent agents and
// a relative path has no stable meaning across them, so callers must pass
// absolute paths (or "" which is also rejected to force explicitness).
func resolvePath(root, input string) (string, error) {
	if input == "" {
		return "", errAbsolutePathRequired("")
	}
	if !filepath.IsAbs(input) {
		return "", errAbsolutePathRequired(input)
	}
	return filepath.Clean(input), nil
}

// errAbsolutePathRequired is the error returned for relative/empty paths.
func errAbsolutePathRequired(input string) error {
	return fmt.Errorf("absolute path required: got %q. This MCP server is shared and does not resolve relative paths; pass an absolute path (e.g. /media/jahrulnr/storage/workspace/...).", input)
}

// relativePosix returns a presentation path for results. Because the server
// requires absolute inputs and is shared, results keep ABSOLUTE POSIX paths
// (the old workspace-relative form would be ambiguous across agents). When
// absPath sits under root we still prefer the relative form for readability;
// otherwise we fall back to the absolute path (never ".."-laden output).
func relativePosix(root, absPath, fallback string) string {
	rel, err := filepath.Rel(root, absPath)
	if err == nil && rel != "" && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return filepath.ToSlash(rel)
	}
	if absPath != "" {
		return filepath.ToSlash(absPath)
	}
	return filepath.ToSlash(fallback)
}
