package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultRoot returns the default root directory for the context/retrieval
// engines. Env-based root resolution (NUSASHELL_FILES_ROOT,
// NUSASHELL_WORKSPACE) was removed because the MCP server is shared
// between concurrent agents — env vars are set once at spawn time and
// cannot reflect per-conversation workspaces. The root is only used by
// the context/retrieval engines for indexing; all file operations
// require absolute paths from the caller.
func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "/"
	}
	return filepath.Clean(home)
}

// resolvePath resolves an ABSOLUTE filesystem path. Relative paths are
// rejected: the Files MCP server is shared between concurrent agents and
// a relative path has no stable meaning across them, so callers must pass
// absolute paths (or "" which is also rejected to force explicitness).
func resolvePath(input string) (string, error) {
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
	return fmt.Errorf("absolute path required: got %q. This MCP server is shared and does not resolve relative paths; pass an absolute path.", input)
}

// absPath returns the absolute path for a result, in the native OS
// separator form (no ToSlash conversion — that would break round-trips
// on Windows where filepath.IsAbs expects backslash-style paths). The
// server always returns absolute paths so that callers can round-trip
// them back as inputs without ambiguity. If absPath is empty, fallback
// is used (typically the base name).
func absPath(absPath, fallback string) string {
	if absPath != "" {
		return absPath
	}
	return fallback
}
