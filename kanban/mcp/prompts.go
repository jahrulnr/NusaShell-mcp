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
			mcp.WithPromptDescription("How to use the NusaShell Kanban board safely"),
		),
		handleHowto,
	)
}

func handleHowto(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	if req.Params.Name != "howto" {
		return nil, fmt.Errorf("unknown prompt: %s", req.Params.Name)
	}

	text := `Use this board as the shared work ledger. First call list_projects, then list_columns for the selected project. Create work with create_ticket and keep descriptions useful. Before starting, move_ticket to In Progress; use create_subtask for concrete steps and complete_subtask when each is done; update_ticket with decisions and blockers; move the parent to Done only after acceptance criteria are met. Use list_tickets for board state and get_ticket for details. Do not assume column IDs, do not invent ticket IDs, and do not delete tickets or sessions unless explicitly requested. Projects and all data are local to NusaShell's plugin data directory.`

	return &mcp.GetPromptResult{
		Description: "How to use the NusaShell Kanban board safely",
		Messages: []mcp.PromptMessage{
			{
				Role:    mcp.RoleUser,
				Content: mcp.NewTextContent(text),
			},
		},
	}, nil
}
