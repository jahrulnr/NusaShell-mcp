package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempFile writes content to a file under tmpDir and returns its
// absolute path (PatchFile requires absolute paths).
func writeTempFile(t *testing.T, tmpDir, name, content string) string {
	t.Helper()
	abs := filepath.Join(tmpDir, name)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return abs
}

// --- Backward compatibility ---

func TestPatchFileReplacesSingleOccurrence(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "hello world\nfoo bar")
	svc := NewFileService(dir)

	result, err := svc.PatchFile(abs, []PatchEdit{{OldString: "foo bar", NewString: "baz qux"}}, false)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if !result["patched"].(bool) {
		t.Fatalf("expected patched=true")
	}
	got, _ := os.ReadFile(abs)
	if string(got) != "hello world\nbaz qux" {
		t.Fatalf("content = %q", string(got))
	}
	_ = result
}

func TestPatchFileReplaceAll(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "aaa\naaa\naaa")
	svc := NewFileService(dir)

	result, err := svc.PatchFile(abs, []PatchEdit{{OldString: "aaa", NewString: "bbb", ReplaceAll: true}}, false)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	occ := result["occurrences"].([]int)
	if occ[0] != 3 {
		t.Fatalf("occurrences = %v, want [3]", occ)
	}
	got, _ := os.ReadFile(abs)
	if string(got) != "bbb\nbbb\nbbb" {
		t.Fatalf("content = %q", string(got))
	}
}

// --- Uniqueness enforcement (NEW) ---

func TestPatchFileRejectsAmbiguousOldString(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "return x\nreturn x\nreturn x")
	svc := NewFileService(dir)

	_, err := svc.PatchFile(abs, []PatchEdit{{OldString: "return x", NewString: "return y"}}, false)
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "matches 3 times") {
		t.Fatalf("error should mention match count: %v", err)
	}
	if !strings.Contains(err.Error(), "line") {
		t.Fatalf("error should mention line numbers: %v", err)
	}
}

func TestPatchFileAmbiguityErrorListsLineNumbers(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "foo\nbar\nfoo\nbaz\nfoo")
	svc := NewFileService(dir)

	_, err := svc.PatchFile(abs, []PatchEdit{{OldString: "foo", NewString: "qux"}}, false)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	// "foo" appears on lines 1, 3, 5.
	for _, want := range []string{"1", "3", "5"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should list line %s: %v", want, err)
		}
	}
}

// --- occurrence_index (NEW) ---

func TestPatchFileOccurrenceIndexReplacesNth(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "return x\nreturn x\nreturn x")
	svc := NewFileService(dir)

	idx := 2
	result, err := svc.PatchFile(abs, []PatchEdit{{
		OldString:       "return x",
		NewString:       "return y",
		OccurrenceIndex: &idx,
	}}, false)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	occ := result["occurrences"].([]int)
	if occ[0] != 1 {
		t.Fatalf("occurrences = %v, want [1]", occ)
	}
	got, _ := os.ReadFile(abs)
	if string(got) != "return x\nreturn y\nreturn x" {
		t.Fatalf("content = %q, want 2nd occurrence replaced", string(got))
	}
}

func TestPatchFileOccurrenceIndexOutOfBounds(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "foo\nfoo")
	svc := NewFileService(dir)

	idx := 5
	_, err := svc.PatchFile(abs, []PatchEdit{{
		OldString:       "foo",
		NewString:       "bar",
		OccurrenceIndex: &idx,
	}}, false)
	if err == nil || !strings.Contains(err.Error(), "occurrence_index 5") {
		t.Fatalf("expected out-of-bounds error, got %v", err)
	}
}

func TestPatchFileOccurrenceIndexZeroIsUnset(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "foo\nfoo")
	svc := NewFileService(dir)

	idx := 0
	_, err := svc.PatchFile(abs, []PatchEdit{{
		OldString:       "foo",
		NewString:       "bar",
		OccurrenceIndex: &idx,
	}}, false)
	// occurrence_index=0 means unset → ambiguity error (2 matches).
	if err == nil || !strings.Contains(err.Error(), "matches 2 times") {
		t.Fatalf("expected ambiguity error for index=0, got %v", err)
	}
}

// --- context_before / context_after (NEW) ---

func TestPatchFileContextBeforeDisambiguates(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "func a() {\n  return x\n}\nfunc b() {\n  return x\n}")
	svc := NewFileService(dir)

	result, err := svc.PatchFile(abs, []PatchEdit{{
		OldString:     "return x",
		NewString:     "return y",
		ContextBefore: "func b() {",
	}}, false)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	occ := result["occurrences"].([]int)
	if occ[0] != 1 {
		t.Fatalf("occurrences = %v, want [1]", occ)
	}
	got, _ := os.ReadFile(abs)
	if strings.Contains(string(got), "func a() {\n  return y") {
		t.Fatalf("should not replace in func a: %q", string(got))
	}
	if !strings.Contains(string(got), "func b() {\n  return y") {
		t.Fatalf("should replace in func b: %q", string(got))
	}
}

func TestPatchFileContextAfterDisambiguates(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "  return x\n}\n\n  return x\n// end")
	svc := NewFileService(dir)

	result, err := svc.PatchFile(abs, []PatchEdit{{
		OldString:    "return x",
		NewString:    "return z",
		ContextAfter: "// end",
	}}, false)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	occ := result["occurrences"].([]int)
	if occ[0] != 1 {
		t.Fatalf("occurrences = %v, want [1]", occ)
	}
	got, _ := os.ReadFile(abs)
	if !strings.Contains(string(got), "  return z\n// end") {
		t.Fatalf("should replace before // end: %q", string(got))
	}
}

func TestPatchFileContextBeforeAndAfterNarrowsToSingleMatch(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt",
		"func a() {\n  return x\n}\nfunc b() {\n  return x\n}\nfunc c() {\n  return x\n}")
	svc := NewFileService(dir)

	result, err := svc.PatchFile(abs, []PatchEdit{{
		OldString:     "return x",
		NewString:     "return y",
		ContextBefore: "func b() {",
		ContextAfter:  "}",
	}}, false)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	occ := result["occurrences"].([]int)
	if occ[0] != 1 {
		t.Fatalf("occurrences = %v, want [1]", occ)
	}
	got, _ := os.ReadFile(abs)
	if !strings.Contains(string(got), "func b() {\n  return y\n}") {
		t.Fatalf("should replace in func b: %q", string(got))
	}
	if strings.Contains(string(got), "func a() {\n  return y") {
		t.Fatalf("should not touch func a: %q", string(got))
	}
}

func TestPatchFileContextNoMatchStillErrors(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "return x\nreturn x")
	svc := NewFileService(dir)

	_, err := svc.PatchFile(abs, []PatchEdit{{
		OldString:     "return x",
		NewString:     "return y",
		ContextBefore: "nonexistent context",
	}}, false)
	if err == nil {
		t.Fatal("expected error when context doesn't match any occurrence")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("error should mention context: %v", err)
	}
}

// --- Multiple edits still work ---

func TestPatchFileMultipleEditsInSequence(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "foo\nbar\nbaz")
	svc := NewFileService(dir)

	result, err := svc.PatchFile(abs, []PatchEdit{
		{OldString: "foo", NewString: "FOO"},
		{OldString: "bar", NewString: "BAR"},
	}, false)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if result["applied"].(int) != 2 {
		t.Fatalf("applied = %v, want 2", result["applied"])
	}
	got, _ := os.ReadFile(abs)
	if string(got) != "FOO\nBAR\nbaz" {
		t.Fatalf("content = %q", string(got))
	}
}

// --- Preview mode ---

func TestPatchFilePreviewDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	abs := writeTempFile(t, dir, "test.txt", "hello world")
	svc := NewFileService(dir)

	result, err := svc.PatchFile(abs, []PatchEdit{{OldString: "hello", NewString: "hi"}}, true)
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if result["patched"].(bool) {
		t.Fatalf("preview should not patch")
	}
	if result["preview"].(string) != "hi world" {
		t.Fatalf("preview = %v", result["preview"])
	}
	got, _ := os.ReadFile(abs)
	if string(got) != "hello world" {
		t.Fatalf("file should be unchanged: %q", string(got))
	}
}
