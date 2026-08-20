package main

import (
	"strings"
	"testing"
)

// The agent-facing grep receipt must honor output_mode: files_with_matches
// renders only paths (never matched lines), count renders per-file counts,
// and content renders path:line:content. Regression: the formatter used to
// ignore the mode and rendered nothing for files_with_matches.
func TestFormatGrepTextContentMode(t *testing.T) {
	text := formatGrepText(map[string]any{
		"path":    "/repo",
		"pattern": "foo",
		"meta":    map[string]any{"count": 2, "truncated": false},
		"results": []GrepResult{
			{Path: "/repo/a.js", Line: 3, Content: "foo bar"},
			{Path: "/repo/b.js", Line: 7, Content: "x foo y"},
		},
	})
	for _, want := range []string{"ok=true", "/repo/a.js:3:foo bar", "/repo/b.js:7:x foo y"} {
		if !strings.Contains(text, want) {
			t.Errorf("content mode missing %q:\n%s", want, text)
		}
	}
}

func TestFormatGrepTextFilesWithMatches(t *testing.T) {
	text := formatGrepText(map[string]any{
		"path":    "/repo",
		"pattern": "foo",
		"meta":    map[string]any{"count": 2, "truncated": false},
		"files":   []string{"/repo/a.js", "/repo/b.js"},
	})
	if strings.Contains(text, ":3:") || strings.Contains(text, ":7:") {
		t.Errorf("files_with_matches must never render matched lines:\n%s", text)
	}
	for _, want := range []string{"/repo/a.js", "/repo/b.js"} {
		if !strings.Contains(text, want) {
			t.Errorf("files_with_matches missing path %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "count=2") {
		t.Errorf("files_with_matches should report the file count:\n%s", text)
	}
}

func TestFormatGrepTextCountMode(t *testing.T) {
	text := formatGrepText(map[string]any{
		"path":    "/repo",
		"pattern": "foo",
		"meta":    map[string]any{"fileCount": 2, "totalMatches": 3, "truncated": false},
		"counts":  map[string]int{"/repo/b.js": 1, "/repo/a.js": 2},
	})
	for _, want := range []string{"2\t/repo/a.js", "1\t/repo/b.js"} {
		if !strings.Contains(text, want) {
			t.Errorf("count mode missing %q:\n%s", want, text)
		}
	}
}

func TestFormatGenericTextErrorKeyNoOkTrue(t *testing.T) {
	// A result with an "error" key must NOT render ok=true — the agent
	// must see a clear failure, not an ambiguous ok+error mix.
	text := formatGenericText(map[string]any{"error": "context deadline exceeded"})
	if strings.Contains(text, "ok=true") {
		t.Errorf("error result must not contain ok=true:\n%s", text)
	}
	if !strings.Contains(text, "error=context deadline exceeded") {
		t.Errorf("error result must contain the error message:\n%s", text)
	}
}
