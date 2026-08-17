// Package main is the NusaShell Kanban MCP server (Go port).
package main

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
)

func main() {
	store := NewStore(mcpkit.MustResolveDataFile(
		"NUSASHELL_KANBAN_DATA_FILE",
		"nusashell.kanban",
		"kanban.json",
		".",
	))
	store.Load()
	store.GetOrCreateDefaultProject()

	s := server.NewMCPServer("nusashell-kanban", "2.0.1",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(false),
	)

	registerTools(s, store)
	registerPrompts(s)

	if err := mcpkit.ServeStdio(s, "nusashell-kanban"); err != nil {
		stderr("server error: %s", err)
	}
}
