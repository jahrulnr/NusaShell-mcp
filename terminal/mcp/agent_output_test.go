package main

import (
	"strings"
	"testing"
)

func TestFormatShellsText(t *testing.T) {
	data := map[string]any{
		"platform":    "linux",
		"defaultKind": "bash",
		"shells": []ResolvedShell{
			{Kind: KindBash, Path: "/bin/bash", Available: true, Source: "which"},
			{Kind: KindZsh, Path: "/bin/zsh", Available: true, Source: "which"},
		},
	}

	got := formatShellsText(data)
	if !strings.Contains(got, "count=2") {
		t.Fatalf("expected shell count in receipt, got %q", got)
	}
	if !strings.Contains(got, "bash\t/bin/bash\twhich") {
		t.Fatalf("expected bash entry in receipt, got %q", got)
	}
}
