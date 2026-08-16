package main

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const promptHowto = "howto"

func registerPrompts(s *server.MCPServer) {
	s.AddPrompt(
		mcp.NewPrompt(promptHowto,
			mcp.WithPromptDescription("Notes plugin how-to"),
		),
		handleHowto,
	)
}

func handleHowto(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	if req.Params.Name != promptHowto {
		return nil, fmt.Errorf("unknown prompt: %s", req.Params.Name)
	}

	text := `Use the Notes plugin for persistent local notes.

Main tools:
- create: create a note with a title and body.
- list: list saved notes.
- get: read one note by id.
- update: change a note's title or body.
- delete: permanently remove a note.
- search: find notes by text.

Use tool_schema for the exact arguments and required fields. Notes are persisted by the plugin and are separate from the shell conversation history.`

	return &mcp.GetPromptResult{
		Description: "Notes plugin how-to",
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.NewTextContent(text),
			},
		},
	}, nil
}
