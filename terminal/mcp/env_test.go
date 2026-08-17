package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// execWithArgs calls the exec handler with the given arguments and returns
// the structured content.
func execWithArgs(t *testing.T, pm *ProcessManager, args map[string]any) map[string]any {
	t.Helper()
	handler := handleExec(pm)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("exec error: %v", err)
	}
	if res.IsError {
		t.Fatalf("exec IsError: %s", res.Content[0].(mcp.TextContent).Text)
	}
	return res.StructuredContent.(map[string]any)
}

func TestExecPassesEnvVars(t *testing.T) {
	pm := NewProcessManager()

	data := execWithArgs(t, pm, map[string]any{
		"command": "printenv NUSASHELL_TEST_ENV",
		"env": map[string]any{
			"NUSASHELL_TEST_ENV": "hello-from-env",
		},
	})
	stdout := data["stdout"].(string)
	if !strings.Contains(stdout, "hello-from-env") {
		t.Fatalf("stdout = %q, want env var to be visible", stdout)
	}
}

func TestExecEnvMergesWithParentEnviron(t *testing.T) {
	pm := NewProcessManager()

	// PATH should still be inherited from the parent process.
	data := execWithArgs(t, pm, map[string]any{
		"command": "printenv PATH",
		"env": map[string]any{
			"NUSASHELL_EXTRA": "extra-value",
		},
	})
	stdout := data["stdout"].(string)
	if stdout == "" {
		t.Fatalf("PATH should still be inherited from parent environ")
	}
}

func TestExecEnvOverwritesParentValue(t *testing.T) {
	pm := NewProcessManager()

	// Overwrite a known parent env var (TERM is set to xterm-256color by
	// the server). The agent's env should win.
	data := execWithArgs(t, pm, map[string]any{
		"command": "printenv TERM",
		"env": map[string]any{
			"TERM": "dumb",
		},
	})
	stdout := strings.TrimSpace(data["stdout"].(string))
	if stdout != "dumb" {
		t.Fatalf("TERM = %q, want 'dumb' (agent env should overwrite parent)", stdout)
	}
}

func TestExecWithoutEnvStillWorks(t *testing.T) {
	pm := NewProcessManager()

	data := execWithArgs(t, pm, map[string]any{
		"command": "echo no-env-param",
	})
	stdout := strings.TrimSpace(data["stdout"].(string))
	if stdout != "no-env-param" {
		t.Fatalf("stdout = %q, want 'no-env-param'", stdout)
	}
}

func TestExecIgnoresNonStringEnvValues(t *testing.T) {
	pm := NewProcessManager()

	// Mixed types — only string values should be passed; non-strings
	// are silently skipped (don't crash).
	data := execWithArgs(t, pm, map[string]any{
		"command": "printenv NUSASHELL_STR_ONLY",
		"env": map[string]any{
			"NUSASHELL_STR_ONLY":   "string-value",
			"NUSASHELL_NUM_VALUE":  42,
			"NUSASHELL_BOOL_VALUE": true,
		},
	})
	stdout := strings.TrimSpace(data["stdout"].(string))
	if stdout != "string-value" {
		t.Fatalf("stdout = %q, want 'string-value'", stdout)
	}
}
