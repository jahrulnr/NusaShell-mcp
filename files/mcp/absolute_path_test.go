package main

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePathRejectsRelative(t *testing.T) {
	_, err := resolvePath("tmp/x.md")
	if err == nil || !strings.Contains(err.Error(), "absolute path required") {
		t.Fatalf("expected absolute-path error, got %v", err)
	}
}

func TestResolvePathRejectsEmpty(t *testing.T) {
	_, err := resolvePath("")
	if err == nil {
		t.Fatalf("expected error for empty path (forced explicitness)")
	}
}

func TestResolvePathAcceptsAbsolute(t *testing.T) {
	// OS-agnostic absolute path: on Windows root is a drive; on Unix a plain
	// absolute path. Avoid hardcoding "/media/…" which is relative on Windows.
	abs := t.TempDir()
	if runtime.GOOS == "windows" {
		abs = filepath.Join("C:\\", filepath.Base(t.TempDir()))
	}
	p, err := resolvePath(abs)
	if err != nil {
		t.Fatalf("absolute should pass: %v", err)
	}
	if p != filepath.Clean(abs) {
		t.Fatalf("expected cleaned abs path, got %q", p)
	}
}

func TestListDirRejectsRelative(t *testing.T) {
	svc := NewFileService("/tmp/root")
	_, err := svc.ListDir("relative/dir")
	if err == nil || !strings.Contains(err.Error(), "absolute path required") {
		t.Fatalf("expected rejection for relative list, got %v", err)
	}
}

func TestReadFileRejectsRelative(t *testing.T) {
	svc := NewFileService("/tmp/root")
	_, err := svc.ReadFile("docs/x.md", ReadOpts{})
	if err == nil || !strings.Contains(err.Error(), "absolute path required") {
		t.Fatalf("expected rejection for relative read, got %v", err)
	}
}

func TestSearchRelevantRejectsRelativeScope(t *testing.T) {
	eng := NewRetrievalEngine("/tmp/root")
	_, err := eng.searchRelevant("hello", 5, "sub", false)
	if err == nil || !strings.Contains(err.Error(), "absolute path required") {
		t.Fatalf("expected rejection in search_relevant, got %v", err)
	}
}

func TestListSymbolsRejectsRelative(t *testing.T) {
	eng := NewContextEngine("/tmp/root")
	_, err := eng.ListSymbols("src/file.go", "", 5)
	if err == nil || !strings.Contains(err.Error(), "absolute path required") {
		t.Fatalf("expected rejection in list_symbols, got %v", err)
	}
}

// Dummy context import guard.
var _ = context.Background
