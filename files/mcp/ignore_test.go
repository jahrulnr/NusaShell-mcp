package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPatternMatchesIgnore(t *testing.T) {
	cases := []struct {
		rel, pat string
		want     bool
	}{
		{"skipme.txt", "skipme.txt", true},
		{"src/skipme.txt", "skipme.txt", true},
		{"src/keep.go", "skipme.txt", false},
		{"src/foo.log", "*.log", true},
		{"foo.log", "*.log", true},
		{"src/foo.tmp", "src/*.tmp", true},
		{"foo.tmp", "src/*.tmp", false},
		{"build/out.js", "build", true},
		{"build/out.js", "/build", true},
		{"src/build/out.js", "/build", false},
		{"node_modules/pkg/index.js", "node_modules", true},
	}
	for _, tc := range cases {
		got := patternMatchesIgnore(tc.rel, tc.pat)
		if got != tc.want {
			t.Errorf("patternMatchesIgnore(%q, %q) = %v, want %v", tc.rel, tc.pat, got, tc.want)
		}
	}
}

func TestMatchesIgnoreNegationLastWins(t *testing.T) {
	patterns := []string{"*.log", "!keep.log"}
	if !matchesIgnore("drop.log", patterns) {
		t.Fatal("drop.log should be ignored")
	}
	if matchesIgnore("keep.log", patterns) {
		t.Fatal("keep.log should be re-included by !keep.log")
	}
}

func TestIsDefaultIgnoredGit(t *testing.T) {
	if !isDefaultIgnored(".git") {
		t.Fatal(".git must be ignored by default")
	}
	if !isDefaultIgnored("node_modules") {
		t.Fatal("node_modules must be ignored by default")
	}
	if isDefaultIgnored("src") {
		t.Fatal("src must not be ignored")
	}
}

func TestFindRepoRoot(t *testing.T) {
	repo := t.TempDir()
	src := filepath.Join(repo, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := findRepoRoot(src)
	if got != repo {
		t.Fatalf("findRepoRoot = %q, want %q", got, repo)
	}

	orphan := t.TempDir()
	if findRepoRoot(orphan) != filepath.Clean(orphan) {
		t.Fatalf("no .git should fall back to the start dir, got %q", findRepoRoot(orphan))
	}
}
