package main

import (
	"os"
	"testing"
)

func TestValidateMediaFileRejectsDirectories(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if err := validateMediaFile(info); err == nil {
		t.Fatal("validateMediaFile(directory) = nil, want error")
	}
}

func TestValidateMediaFileRejectsOversizedFiles(t *testing.T) {
	path := t.TempDir() + "/large.bin"
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Truncate(path, maxSendMediaBytes+1); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if err := validateMediaFile(info); err == nil {
		t.Fatal("validateMediaFile(oversized) = nil, want error")
	}
}
