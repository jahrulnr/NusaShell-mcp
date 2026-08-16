// Package mcpkit provides shared helpers for NusaShell first-party MCP
// plugins: safe error redaction, data-file resolution, and a stdio server
// bootstrap that keeps stdout clean for MCP protocol traffic.
package mcpkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveDataFile picks the durable JSON path for a plugin's data.
//
// Precedence:
//  1. envOverride (e.g. NUSASHELL_NOTES_DATA_FILE) — tests and explicit overrides
//  2. {NUSASHELL_USER_DATA or NUSASHELL_DATA_DIR}/plugins-data/{pluginID}/{filename}
//     — desktop shell (keeps user data out of the install/plugin bundle)
//  3. fallbackDir/filename — standalone / legacy fallback
//
// The parent directory is created on demand when createDir is true.
func ResolveDataFile(envOverride, pluginID, filename, fallbackDir string, createDir bool) (string, error) {
	if env := os.Getenv(envOverride); env != "" {
		return filepath.Clean(env), nil
	}

	userData := os.Getenv("NUSASHELL_USER_DATA")
	if userData == "" {
		userData = os.Getenv("NUSASHELL_DATA_DIR")
	}
	if userData != "" {
		dir := filepath.Join(filepath.Clean(userData), "plugins-data", pluginID)
		if createDir {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("create data dir %s: %w", dir, err)
			}
		}
		return filepath.Join(dir, filename), nil
	}

	return filepath.Join(fallbackDir, filename), nil
}

// SafeError sanitizes an error message for public output: strips control
// characters, truncates to maxLen, and returns a generic message for nil or
// empty errors.
func SafeError(err error, maxLen int) string {
	if err == nil {
		return "unknown error"
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "unknown error"
	}
	msg = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, msg)
	if maxLen > 0 && len(msg) > maxLen {
		msg = msg[:maxLen]
	}
	return msg
}

// MustResolveDataFile is like ResolveDataFile but panics on directory
// creation failure. Use only in main() where startup must abort.
func MustResolveDataFile(envOverride, pluginID, filename, fallbackDir string) string {
	path, err := ResolveDataFile(envOverride, pluginID, filename, fallbackDir, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mcpkit] failed to resolve data file: %s\n", err)
		os.Exit(1)
	}
	return path
}
