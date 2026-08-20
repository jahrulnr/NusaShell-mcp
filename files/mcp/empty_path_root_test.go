package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestListEmptyPathResolvesToRoot pins the schema promise: list with
// path="" resolves to the workspace root, not an error. Regression: the
// schema said "Use empty string for the workspace root" but resolvePath
// rejected "" — costing the agent two wasted rounds.
func TestListEmptyPathResolvesToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(root)
	items, err := svc.ListDir("")
	if err != nil {
		t.Fatalf("ListDir(\"\") should resolve to workspace root, got error: %v", err)
	}
	found := false
	for _, it := range items {
		if it.Name == "marker.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListDir(\"\") did not list root contents, got %d items", len(items))
	}
}

func TestGrepEmptyPathResolvesToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n// foo here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(root)
	result, err := svc.GrepFiles("", "foo", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatalf("GrepFiles(\"\") should resolve to workspace root, got error: %v", err)
	}
	files, _ := result["files"].([]string)
	if len(files) != 1 || !strings.HasSuffix(files[0], "a.go") {
		t.Errorf("GrepFiles(\"\") expected 1 match (a.go), got %v", files)
	}
}

func TestSearchEmptyPathResolvesToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(root)
	result, err := svc.SearchFiles("", "*.txt", nil, "any", 10)
	if err != nil {
		t.Fatalf("SearchFiles(\"\") should resolve to workspace root, got error: %v", err)
	}
	items, _ := result["results"].([]SearchResult)
	if len(items) != 1 || items[0].Name != "notes.txt" {
		t.Errorf("SearchFiles(\"\") expected notes.txt, got %+v", items)
	}
}

func TestDetectStackEmptyPathResolvesToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := NewContextEngine(root)
	stack, err := eng.DetectStack("")
	if err != nil {
		t.Fatalf("DetectStack(\"\") should resolve to workspace root, got error: %v", err)
	}
	if stack["category"] == nil {
		t.Errorf("DetectStack(\"\") expected a category, got %v", stack)
	}
}
