package main

import (
	"strings"
	"testing"
)

// TestSessionDSNUsesModerncPragmaSyntax guards against regressing to the
// mattn/go-sqlite3 DSN syntax (_foreign_keys=on), which modernc.org/sqlite
// silently ignores. When foreign_keys is ignored, whatsmeow's schema
// upgrade aborts with "foreign keys are not enabled".
func TestSessionDSNUsesModerncPragmaSyntax(t *testing.T) {
	dsn := sessionDSN("/tmp/whatsapp")

	// modernc.org/sqlite uses _pragma=<name>(<value>).
	if !strings.Contains(dsn, "_pragma=foreign_keys(on)") {
		t.Errorf("DSN missing _pragma=foreign_keys(on): %s", dsn)
	}
	if !strings.Contains(dsn, "_pragma=journal_mode(WAL)") {
		t.Errorf("DSN missing _pragma=journal_mode(WAL): %s", dsn)
	}
	if !strings.Contains(dsn, "_pragma=busy_timeout(5000)") {
		t.Errorf("DSN missing _pragma=busy_timeout(5000): %s", dsn)
	}

	// The mattn-style _foreign_keys=on must NOT appear — modernc ignores it.
	if strings.Contains(dsn, "_foreign_keys=") {
		t.Errorf("DSN uses mattn-style _foreign_keys= which modernc ignores: %s", dsn)
	}
}

// TestSessionDSNUsesForwardSlashes ensures the path separator is forward
// slash on all OSes (Windows filepath.Join produces backslashes which
// break SQLite URI parsing).
func TestSessionDSNUsesForwardSlashes(t *testing.T) {
	dsn := sessionDSN("/tmp/whatsapp")
	if !strings.HasPrefix(dsn, "file:/tmp/whatsapp/session.db?") {
		t.Errorf("DSN path not forward-slashed: %s", dsn)
	}
	if strings.Contains(dsn, "\\") {
		t.Errorf("DSN contains backslash: %s", dsn)
	}
}
