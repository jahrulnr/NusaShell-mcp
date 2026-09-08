package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTokenRestrictsExistingFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot-token")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := writeToken(path, "secret"); err != nil {
		t.Fatalf("writeToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("token mode = %o, want 600", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(contents) != "secret" {
		t.Fatalf("token contents = %q, want secret", contents)
	}
}
