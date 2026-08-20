package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChunkText(t *testing.T) {
	lines := []string{"alpha beta", "gamma delta", "epsilon"}
	chunks := chunkText(lines)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	if chunks[0].lineStart != 1 || chunks[0].lineEnd != 3 {
		t.Fatalf("bad range: %+v", chunks[0])
	}
}

func TestTokenize(t *testing.T) {
	toks := tokenize("snake_case_name camelCase")
	found := map[string]bool{}
	for _, tk := range toks {
		found[tk] = true
	}
	if !found["snake"] || !found["case"] || !found["name"] {
		t.Fatalf("missing snake tokens: %v", toks)
	}
	if !found["camelcase"] {
		t.Fatalf("missing camel: %v", toks)
	}
}

func TestLoadChunksRealFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("the quick brown fox jumps over the lazy dog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := NewRetrievalEngine(root)
	chunks, scanned, hits, misses, err := eng.loadChunks(root, false)
	if err != nil {
		t.Fatalf("loadChunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatalf("expected chunks, got 0 (scanned=%d hits=%d misses=%d)", scanned, hits, misses)
	}
	if scanned != 1 {
		t.Fatalf("expected 1 file scanned, got %d", scanned)
	}
}

func TestSearchRelevantPipeline(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)
	eng := NewRetrievalEngine(root)
	res, err := eng.searchRelevant("hello function", 5, "", false)
	if err != nil {
		t.Fatalf("searchRelevant: %v", err)
	}
	meta, _ := res["meta"].(map[string]any)
	if meta == nil {
		t.Fatal("expected meta in result")
	}
	scanned, _ := meta["filesScanned"].(int)
	if scanned == 0 {
		t.Fatalf("expected scanned >0, got %v; res=%v", scanned, res)
	}
}
