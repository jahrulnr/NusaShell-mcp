package main

import (
	"testing"
)

func TestResolvePathOrRootEmptyReturnsRoot(t *testing.T) {
	got, err := resolvePathOrRoot("", "/home/user/workspace")
	if err != nil {
		t.Fatalf("empty path should resolve to root, got error: %v", err)
	}
	if got != "/home/user/workspace" {
		t.Errorf("empty path = %q, want %q", got, "/home/user/workspace")
	}
}

func TestResolvePathOrRootNonEmptyDelegates(t *testing.T) {
	got, err := resolvePathOrRoot("/srv/app", "/home/user/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/srv/app" {
		t.Errorf("got %q, want /srv/app", got)
	}
}

func TestResolvePathOrRootRejectsRelative(t *testing.T) {
	_, err := resolvePathOrRoot("relative/path", "/home/user/workspace")
	if err == nil {
		t.Fatal("relative path should be rejected even when root is provided")
	}
}

func TestResolvePathRequiredRejectsEmpty(t *testing.T) {
	// resolvePath (required variant) must still reject empty — mutation
	// tools (write, read, delete, ...) depend on this to force explicitness.
	_, err := resolvePath("")
	if err == nil {
		t.Fatal("resolvePath must reject empty string")
	}
}
