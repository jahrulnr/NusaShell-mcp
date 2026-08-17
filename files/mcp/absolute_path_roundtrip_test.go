package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestListDirReturnsAbsolutePaths verifies that the server always returns
// absolute paths in results — never root-relative paths like "Documents".
// This is the round-trip contract: whatever the server returns must be
// usable as input to the same server.
func TestListDirReturnsAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "Documents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewFileService(root)
	items, err := svc.ListDir(root)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item (Documents), got %d", len(items))
	}
	item := items[0]
	if !filepath.IsAbs(item.Path) {
		t.Fatalf("item.Path = %q, want absolute path (never root-relative)", item.Path)
	}
	if item.Path == "Documents" {
		t.Fatal("item.Path is root-relative 'Documents' — round-trip is broken, server must return absolute paths")
	}
}

// TestTreeReturnsAbsolutePaths verifies tree nodes also return absolute paths.
func TestTreeReturnsAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	svc := NewFileService(root)
	tree, err := svc.Tree(root, 2, nil, true)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	var checkNodes func([]TreeNode)
	checkNodes = func(nodes []TreeNode) {
		for _, n := range nodes {
			if !filepath.IsAbs(n.Path) {
				t.Fatalf("tree node path = %q, want absolute", n.Path)
			}
			if n.Children != nil {
				checkNodes(n.Children)
			}
		}
	}
	checkNodes(tree)
}

// TestGrepReturnsAbsolutePaths verifies grep results use absolute paths.
func TestGrepReturnsAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewFileService(root)
	result, err := svc.GrepFiles(root, "foo", GrepOpts{})
	if err != nil {
		t.Fatalf("GrepFiles: %v", err)
	}
	results, ok := result["results"].([]GrepResult)
	if !ok {
		t.Fatalf("results should be []GrepResult, got %T", result["results"])
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 grep result")
	}
	for _, r := range results {
		if !filepath.IsAbs(r.Path) {
			t.Fatalf("grep result path = %q, want absolute", r.Path)
		}
	}
}

// TestAbsPathAlwaysReturnsAbsolute verifies the helper itself.
func TestAbsPathAlwaysReturnsAbsolute(t *testing.T) {
	// Use OS-native paths so the test works on Windows too.
	dir1 := t.TempDir()
	abs := filepath.Join(dir1, "docs", "file.txt")
	dir2 := t.TempDir()
	logPath := filepath.Join(dir2, "app.log")
	cases := []struct{ absPath, fallback, want string }{
		{abs, "file.txt", abs},
		{"", "fallback.txt", "fallback.txt"},
		{logPath, "", logPath},
	}
	for _, c := range cases {
		got := absPath(c.absPath, c.fallback)
		if got != c.want {
			t.Fatalf("absPath(%q, %q) = %q, want %q", c.absPath, c.fallback, got, c.want)
		}
		// When absPath is set, result must be absolute (OS-native form).
		if c.absPath != "" && !filepath.IsAbs(got) {
			t.Fatalf("absPath result %q is not absolute", got)
		}
	}
}

// TestDefaultRootIgnoresEnvVars verifies that env vars no longer
// override the root — the server always uses the home directory default.
func TestDefaultRootIgnoresEnvVars(t *testing.T) {
	t.Setenv("NUSASHELL_FILES_ROOT", "/tmp/should-be-ignored")
	t.Setenv("NUSASHELL_WORKSPACE", "/tmp/also-ignored")
	root := defaultRoot()
	if root == "/tmp/should-be-ignored" || root == "/tmp/also-ignored" {
		t.Fatalf("defaultRoot should ignore env vars, got %q", root)
	}
}
