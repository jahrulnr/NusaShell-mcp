package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGrepFixture(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"a.js":  "function foo() { return 1; }\nconst bar = foo();\n",
		"b.js":  "function baz() { return foo(); }\n",
		"c.txt": "no match here\n",
	}
	for name, content := range files {
		abs := filepath.Join(dir, name)
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGrepOutputModeContent(t *testing.T) {
	dir := t.TempDir()
	writeGrepFixture(t, dir)
	svc := NewFileService(dir)

	result, err := svc.GrepFiles(dir, "foo", GrepOpts{OutputMode: "content"})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	results, ok := result["results"].([]GrepResult)
	if !ok {
		t.Fatalf("results should be []GrepResult, got %T", result["results"])
	}
	if len(results) == 0 {
		t.Fatal("expected matching lines in content mode")
	}
	// Each result should have content (the matched line).
	for _, r := range results {
		if r.Content == "" {
			t.Fatalf("content mode result has empty content: %+v", r)
		}
	}
}

func TestGrepOutputModeFilesWithMatches(t *testing.T) {
	dir := t.TempDir()
	writeGrepFixture(t, dir)
	svc := NewFileService(dir)

	result, err := svc.GrepFiles(dir, "foo", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	// In files_with_matches mode, "files" should be a list of paths.
	files, ok := result["files"].([]string)
	if !ok {
		t.Fatalf("files_with_matches mode should return []string in 'files', got %T", result["files"])
	}
	// "foo" appears in a.js and b.js but NOT c.txt.
	if len(files) != 2 {
		t.Fatalf("files = %v, want 2 matches (a.js, b.js)", files)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "c.txt") {
			t.Fatalf("c.txt should not match: %s", f)
		}
	}
	// results should NOT be present in files_with_matches mode.
	if _, ok := result["results"]; ok {
		t.Fatal("files_with_matches mode should not include 'results'")
	}
}

func TestGrepOutputModeCount(t *testing.T) {
	dir := t.TempDir()
	writeGrepFixture(t, dir)
	svc := NewFileService(dir)

	result, err := svc.GrepFiles(dir, "foo", GrepOpts{OutputMode: "count"})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	// In count mode, "counts" should be a map of file → match count.
	counts, ok := result["counts"].(map[string]int)
	if !ok {
		t.Fatalf("count mode should return map[string]int in 'counts', got %T", result["counts"])
	}
	// a.js has 2 "foo" matches, b.js has 1.
	totalMatches := 0
	for _, c := range counts {
		totalMatches += c
	}
	if totalMatches != 3 {
		t.Fatalf("total matches = %d, want 3 (a.js=2, b.js=1)", totalMatches)
	}
	// results should NOT be present in count mode.
	if _, ok := result["results"]; ok {
		t.Fatal("count mode should not include 'results'")
	}
}

func TestGrepOutputModeDefaultsToContent(t *testing.T) {
	dir := t.TempDir()
	writeGrepFixture(t, dir)
	svc := NewFileService(dir)

	// Empty OutputMode should default to content (backward compat).
	result, err := svc.GrepFiles(dir, "foo", GrepOpts{})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	if _, ok := result["results"]; !ok {
		t.Fatal("default mode should return 'results' (content mode)")
	}
	if _, ok := result["files"]; ok {
		t.Fatal("default mode should not return 'files'")
	}
}

func TestGrepOutputModeInvalidFallsBackToContent(t *testing.T) {
	dir := t.TempDir()
	writeGrepFixture(t, dir)
	svc := NewFileService(dir)

	// Invalid output_mode should not error — fall back to content.
	result, err := svc.GrepFiles(dir, "foo", GrepOpts{OutputMode: "bogus"})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	if _, ok := result["results"]; !ok {
		t.Fatal("invalid output_mode should fall back to content (results)")
	}
}
