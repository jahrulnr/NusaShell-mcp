// Package main is the NusaShell Files MCP server (Go port).
package main

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
)

func main() {
	root := defaultRoot()
	svc := NewFileService(root)
	svc.ctxEngine = NewContextEngine(root)
	svc.retrEngine = NewRetrievalEngine(root)

	s := server.NewMCPServer("nusashell-files", "2.1.3",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	_ = s
	registerTools(s, svc)
	registerPrompts(s)
	registerResources(s, svc)

	if err := mcpkit.ServeStdio(s, "nusashell-files"); err != nil {
		stderr("server error: %s", err)
	}
}
