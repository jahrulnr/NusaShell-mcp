package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRetrievalEngineConcurrentSearchIsSafe(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 32; i++ {
		name := filepath.Join(root, "file-"+string(rune('a'+i%26))+"-"+string(rune('0'+i%10))+".go")
		if err := os.WriteFile(name, []byte("package main\nfunc Example() { println(\"hello\") }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	eng := NewRetrievalEngine(root)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(refresh bool) {
			defer wg.Done()
			result, err := eng.searchRelevant("hello function", 5, "", refresh)
			if err != nil {
				t.Errorf("search failed: %v", err)
				return
			}
			if result["error"] != nil {
				t.Errorf("search failed: %v", result["error"])
			}
		}(i%8 == 0)
	}
	wg.Wait()
}

func TestRetrievalEngineCancellation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 100; i++ {
		name := filepath.Join(root, "file-"+string(rune('a'+i%26))+".go")
		if err := os.WriteFile(name, []byte("package main\nfunc Example() { println(\"hello\") }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	eng := NewRetrievalEngine(root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.searchRelevantContext(ctx, "hello function", 5, "", false)
	if err == nil {
		t.Fatal("expected cancelled search to return an error")
	}
}
