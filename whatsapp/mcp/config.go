package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveDataDir returns the plugin's durable data directory.
//
// Precedence:
//  1. NUSASHELL_WHATSAPP_DATA_DIR — explicit override (tests)
//  2. {NUSASHELL_USER_DATA or NUSASHELL_DATA_DIR}/plugins-data/nusashell.whatsapp/
//     — desktop shell (keeps user data out of the plugin bundle)
//  3. ./whatsapp-data/ — standalone / legacy fallback
//
// The directory is created on demand with 0700 permissions.
func resolveDataDir() (string, error) {
	if env := os.Getenv("NUSASHELL_WHATSAPP_DATA_DIR"); env != "" {
		dir := filepath.Clean(env)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create data dir %s: %w", dir, err)
		}
		return dir, nil
	}

	userData := os.Getenv("NUSASHELL_USER_DATA")
	if userData == "" {
		userData = os.Getenv("NUSASHELL_DATA_DIR")
	}
	if userData != "" {
		dir := filepath.Join(filepath.Clean(userData), "plugins-data", "nusashell.whatsapp")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create data dir %s: %w", dir, err)
		}
		return dir, nil
	}

	// Fallback for standalone execution.
	dir := filepath.Join(".", "whatsapp-data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create fallback data dir %s: %w", dir, err)
	}
	return dir, nil
}

// isVerbose returns true if NUSASHELL_WHATSAPP_DEBUG is set to a truthy value.
func isVerbose() bool {
	v := os.Getenv("NUSASHELL_WHATSAPP_DEBUG")
	return v == "1" || v == "true" || v == "TRUE"
}
