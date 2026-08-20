package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeNoiseFixture builds a tree with real sources plus the classic noise:
// hidden entries (.git, .experimental with vendored 40-language-style
// CHANGELOGs), node_modules, and build outputs.
func writeNoiseFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("src/app.go", "package app\n// foo here\n")
	mustWrite("src/util.go", "package util\n// no match\n")
	mustWrite("src/notes.txt", "notes content\n")
	mustWrite("node_modules/pkg/index.js", "foo in node_modules\n")
	mustWrite("vendor/third/lib.rs", "foo in vendor\n")
	mustWrite("build/out.txt", "foo in build\n")
	mustWrite(".git/config", "foo in git\n")
	mustWrite(".experimental/OmniRoute/CHANGELOG-en.md", "foo changelog en\n")
	mustWrite(".experimental/OmniRoute/CHANGELOG-ja.md", "foo changelog ja\n")
	mustWrite(".hidden-file.txt", "foo in hidden file\n")
	return dir
}

func TestGrepDefaultIgnoresNoise(t *testing.T) {
	dir := writeNoiseFixture(t)
	svc := NewFileService(dir)

	result, err := svc.GrepFiles(dir, "foo", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	files, ok := result["files"].([]string)
	if !ok {
		t.Fatalf("expected files list, got %T", result["files"])
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], "src/app.go") {
		t.Fatalf("default ignore failed: expected only src/app.go, got %v", files)
	}
}

func TestGrepDefaultIgnoreRootExemption(t *testing.T) {
	dir := writeNoiseFixture(t)
	svc := NewFileService(dir)

	// Passing a hidden directory explicitly as the root still searches it.
	result, err := svc.GrepFiles(filepath.Join(dir, ".experimental"), "foo", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	files, _ := result["files"].([]string)
	if len(files) != 2 {
		t.Fatalf("root exemption failed: expected the two vendored CHANGELOGs, got %v", files)
	}
}

func TestSearchDefaultIgnoresNoise(t *testing.T) {
	dir := writeNoiseFixture(t)
	svc := NewFileService(dir)

	result, err := svc.SearchFiles(dir, "*.txt", nil, "any", 10)
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	items, _ := result["results"].([]SearchResult)
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	// src/notes.txt is the only non-hidden, non-ignored *.txt file
	// (build/out.txt lives under an ignored build dir).
	if len(items) != 1 || items[0].Name != "notes.txt" {
		t.Fatalf("search default ignore failed: got %v", names)
	}
}

func TestSearchDefaultIgnoreRootExemption(t *testing.T) {
	dir := writeNoiseFixture(t)
	svc := NewFileService(dir)

	result, err := svc.SearchFiles(filepath.Join(dir, "node_modules"), "*.js", nil, "any", 10)
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	items, _ := result["results"].([]SearchResult)
	if len(items) != 1 || items[0].Name != "index.js" {
		t.Fatalf("search root exemption failed: got %+v", items)
	}
}
