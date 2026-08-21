package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepEmptyPatternRejected(t *testing.T) {
	dir := t.TempDir()
	svc := NewFileService(dir)
	_, err := svc.GrepFiles(dir, "", GrepOpts{})
	if err == nil {
		t.Fatal("empty pattern must be rejected")
	}
	if !strings.Contains(err.Error(), "pattern is required") {
		t.Fatalf("error = %v, want pattern is required", err)
	}
	_, err = svc.GrepFiles(dir, "   ", GrepOpts{})
	if err == nil {
		t.Fatal("whitespace-only pattern must be rejected")
	}
}

func TestGrepTruncatedWhenCapHit(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("foo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewFileService(dir)

	hit, err := svc.GrepFiles(dir, "foo", GrepOpts{MaxResults: 2, OutputMode: "content"})
	if err != nil {
		t.Fatal(err)
	}
	meta := hit["meta"].(map[string]any)
	if meta["truncated"] != true {
		t.Fatalf("expected truncated=true when more matches exist, got %v", meta)
	}
	if meta["count"] != 2 {
		t.Fatalf("count = %v, want 2", meta["count"])
	}

	exact, err := svc.GrepFiles(dir, "foo", GrepOpts{MaxResults: 3, OutputMode: "content"})
	if err != nil {
		t.Fatal(err)
	}
	exactMeta := exact["meta"].(map[string]any)
	if exactMeta["truncated"] != false {
		t.Fatalf("expected truncated=false when every match fits, got %v", exactMeta)
	}
}

func TestGrepReturnsResolvedPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(dir)
	result, err := svc.GrepFiles("", "package", GrepOpts{OutputMode: "content"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := result["path"].(string)
	if got != filepath.Clean(dir) {
		t.Fatalf("path = %q, want resolved root %q", got, dir)
	}
	text := formatGrepText(map[string]any{
		"path":    got,
		"pattern": "package",
		"meta":    result["meta"],
		"results": result["results"],
	})
	if strings.Contains(text, "path=.") {
		t.Fatalf("receipt must not hide the scan root as path=.:\n%s", text)
	}
	if !strings.Contains(text, "path="+got) {
		t.Fatalf("receipt must include resolved path, got:\n%s", text)
	}
}

func TestRejectTerminalExecArgs(t *testing.T) {
	// Reproduces the mix-up from conv_9d7baf78: terminal exec args sent to files grep.
	args := map[string]any{
		"command": "grep -rn 'rgba(197\\|rgba(245' frontend/styles --include='*.css' | head -30",
		"cwd":     "/media/jahrulnr/storage/workspace/NusaShell",
	}
	msg := rejectTerminalExecArgs(args, "grep")
	if msg == "" {
		t.Fatal("expected an error when command/cwd are sent to files grep")
	}
	if !strings.Contains(msg, "nusashell.files:grep") || !strings.Contains(msg, "nusashell.terminal:exec") {
		t.Fatalf("error should name both tools, got %q", msg)
	}
	if rejectTerminalExecArgs(map[string]any{"pattern": "foo", "path": "/tmp"}, "grep") != "" {
		t.Fatal("valid grep args must not be rejected")
	}
}

func TestGrepGitignoreAndGitDir(t *testing.T) {
	repo := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(".git/config", "foo in git\n")
	mustWrite(".gitignore", "skipme.txt\n*.log\n-\n")
	mustWrite("src/keep.go", "foo in keep\n")
	mustWrite("src/skipme.txt", "foo in skipped\n")
	mustWrite("src/noise.log", "foo in log\n")
	mustWrite("-", "-----BEGIN CERTIFICATE-----\nfoo cert\n")

	svc := NewFileService(repo)
	result, err := svc.GrepFiles(filepath.Join(repo, "src"), "foo", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := result["files"].([]string)
	if len(files) != 1 || !strings.HasSuffix(filepath.ToSlash(files[0]), "src/keep.go") {
		t.Fatalf("gitignore walk failed: got %v", files)
	}

	// Explicit path still searches a gitignored file.
	direct, err := svc.GrepFiles(filepath.Join(repo, "src", "skipme.txt"), "foo", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatal(err)
	}
	directFiles, _ := direct["files"].([]string)
	if len(directFiles) != 1 {
		t.Fatalf("explicit gitignored file should still be searchable, got %v", directFiles)
	}

	// A dash-named CA bundle at the repo root is gitignored via "-".
	rootHit, err := svc.GrepFiles(repo, "BEGIN CERTIFICATE", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatal(err)
	}
	rootFiles, _ := rootHit["files"].([]string)
	for _, f := range rootFiles {
		if filepath.Base(f) == "-" {
			t.Fatalf("gitignored '-' certificate file should not be grepped: %v", rootFiles)
		}
	}
}

func TestGrepParentGitignoreAppliesToSubdir(t *testing.T) {
	repo := t.TempDir()
	src := filepath.Join(repo, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("skipme.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "skipme.txt"), []byte("foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "keep.go"), []byte("foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewFileService(repo)
	result, err := svc.GrepFiles(src, "foo", GrepOpts{OutputMode: "files_with_matches"})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := result["files"].([]string)
	if len(files) != 1 || !strings.HasSuffix(filepath.ToSlash(files[0]), "src/keep.go") {
		t.Fatalf("parent .gitignore should apply when grepping a subdir, got %v", files)
	}
}

func TestTreeSkipsGitAndGitignore(t *testing.T) {
	dir := writeNoiseFixture(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.ts"), []byte("export const y = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(dir)
	tree, err := svc.Tree(dir, 3, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var walk func([]TreeNode)
	walk = func(nodes []TreeNode) {
		for _, n := range nodes {
			names = append(names, n.Name)
			walk(n.Children)
		}
	}
	walk(tree)
	joined := strings.Join(names, ",")
	for _, banned := range []string{".git", "node_modules", "ignored.ts", "vendor"} {
		for _, n := range names {
			if n == banned {
				t.Fatalf("tree should skip %s, got %s", banned, joined)
			}
		}
	}
}

func TestSearchGitignore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kept.ts"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.ts"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(dir)
	result, err := svc.SearchFiles(dir, "*.ts", nil, "file", 10)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := result["results"].([]SearchResult)
	if len(items) != 1 || items[0].Name != "kept.ts" {
		t.Fatalf("search gitignore failed: %+v", items)
	}
}
