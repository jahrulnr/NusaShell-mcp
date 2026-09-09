package main

import (
	"os"
	"runtime"
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
	if runtime.GOOS != "windows" {
		// Windows has no POSIX permission bits: os.Chmod only maps the
		// owner-write bit to the read-only attribute and Stat always reports
		// 0666 for a writable file, so a 0600 assertion cannot hold there.
		// Access control is enforced by the user-profile ACLs instead.
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("media mode = %o, want 600", got)
		}
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(contents) != "media" {
		t.Fatalf("media contents = %q, want media", contents)
	}
}
