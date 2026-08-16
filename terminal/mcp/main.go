// Package main is the NusaShell Terminal MCP server (Go port).
package main

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
)

func main() {
	sessions := NewSessionManager()

	s := server.NewMCPServer("nusashell-terminal", "2.0.0",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(false),
	)

	registerTools(s, sessions)
	registerPrompts(s)

	if err := mcpkit.ServeStdio(s, "nusashell-terminal"); err != nil {
		stderr("server error: %s", err)
	}
}
