package main

import (
	"os"
	"testing"
)

func TestWriteMediaCacheRestrictsExistingFilePermissions(t *testing.T) {
	path := t.TempDir() + "/cached-media"
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := writeMediaCache(path, []byte("media")); err != nil {
		t.Fatalf("writeMediaCache: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("media mode = %o, want 600", got)
	}
}
