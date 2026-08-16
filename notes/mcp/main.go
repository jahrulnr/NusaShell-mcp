// Package main is the NusaShell Notes MCP server (Go port).
//
// Tools: create, list, get, update, delete, search.
// Prompt: howto.
// Data: {NUSASHELL_USER_DATA}/plugins-data/nusashell.notes/notes.json
package main

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
)

func main() {
	svc := NewNoteService(mcpkit.MustResolveDataFile(
		"NUSASHELL_NOTES_DATA_FILE",
		"nusashell.notes",
		"notes.json",
		".",
	))
	if err := svc.Load(); err != nil {
		stderr("failed to load notes: %s", err)
	}

	s := server.NewMCPServer("nusashell-notes", "2.0.0",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(false),
	)

	registerTools(s, svc)
	registerPrompts(s)

	if err := mcpkit.ServeStdio(s, "nusashell-notes"); err != nil {
		stderr("server error: %s", err)
	}
}
