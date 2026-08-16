package main

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerPrompts(s *server.MCPServer) {
	s.AddPrompt(
		mcp.NewPrompt("howto",
			mcp.WithPromptDescription("Terminal plugin how-to"),
		),
		handleHowto,
	)
}

func handleHowto(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	if req.Params.Name != "howto" {
		return nil, fmt.Errorf("unknown prompt: %s", req.Params.Name)
	}
	text := `Use the Terminal plugin to run commands or maintain an interactive PTY session.

Main tools:
- shells: list installed shells (bash, zsh, pwsh, powershell, cmd, wsl) and the auto default — call this first on Windows.
- exec: run one command; returns stdout/stderr plus structured fields. Pass shell="pwsh"|"powershell"|"bash"|"cmd"|"wsl" when auto is wrong.
- open: open an interactive session (same shell kinds).
- write / read: send input and read buffered output (read strips ANSI in agent text; UI keeps raw PTY).
- resize: change PTY dimensions.
- close / list: close or inspect sessions.

Pass an absolute cwd when a specific directory matters; do not assume the conversation workspace is the process cwd. Prefer pwsh/bash over cmd.exe for scripting. Commands execute with the user's shell permissions and can change files or access external systems. Confirm destructive or irreversible commands before running them.`

	return &mcp.GetPromptResult{
		Description: "Terminal plugin how-to",
		Messages: []mcp.PromptMessage{
			{Role: mcp.RoleUser, Content: mcp.NewTextContent(text)},
		},
	}, nil
}
