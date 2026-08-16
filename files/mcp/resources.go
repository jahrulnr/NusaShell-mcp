package main

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerResources exposes the workspace-root AGENTS.md as an MCP resource
// when present (port of server.js ListResources/ReadResource).
func registerResources(s *server.MCPServer, svc *FileService) {
	s.AddResource(
		mcp.NewResource("nusashell://workspace/AGENTS.md", "Workspace instructions",
			mcp.WithResourceDescription("Workspace-root AGENTS.md project guidance."),
			mcp.WithMIMEType("text/markdown"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			info, ok := svc.ctxEngine.readWorkspaceInstructions()
			if !ok {
				return nil, nil
			}
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      info["uri"].(string),
					MIMEType: info["mimeType"].(string),
					Text:     info["text"].(string),
				},
			}, nil
		},
	)
}
