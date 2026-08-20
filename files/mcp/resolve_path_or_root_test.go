package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathOrRootEmptyReturnsRoot(t *testing.T) {
	root := t.TempDir()
	got, err := resolvePathOrRoot("", root)
	if err != nil {
		t.Fatalf("empty path should resolve to root, got error: %v", err)
	}
	if want, _ := filepath.Abs(root); got != want {
		t.Errorf("empty path = %q, want %q", got, want)
	}
}

func TestResolvePathOrRootNonEmptyDelegates(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sub")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolvePathOrRoot(target, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Clean(target) {
		t.Errorf("got %q, want %q", got, filepath.Clean(target))
	}
}

func TestResolvePathOrRootRejectsRelative(t *testing.T) {
	root := t.TempDir()
	_, err := resolvePathOrRoot("relative/path", root)
	if err == nil {
		t.Fatal("relative path should be rejected even when root is provided")
	}
}

func TestResolvePathRequiredRejectsEmpty(t *testing.T) {
	// resolvePath (required variant) must still reject empty — mutation
	// tools (write, read, delete, ...) depend on this to force explicitness.
	_, err := resolvePath("")
	if err == nil {
		t.Fatal("resolvePath must reject empty string")
	}
}
