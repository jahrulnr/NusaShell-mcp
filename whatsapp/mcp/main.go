// Package main is the NusaShell WhatsApp MCP server.
//
// Links a personal WhatsApp account as a Web "linked device" via whatsmeow
// and exposes messages, contacts, and groups through MCP tools.
//
// Tools: status, login, logout, list_chats, get_chat, list_contacts,
// list_groups, send_message, send_media, react, mark_read, get_messages,
// search_messages, download_media, request_sync.
//
// Data: {NUSASHELL_USER_DATA}/plugins-data/nusashell.whatsapp/
//   session.db    — whatsmeow session and device keys
//   whatsapp.db   — application DB (messages, contacts, groups, media metadata)
//   media/        — downloaded media blobs, sha256-named
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
)

func main() {
	// Resolve the data directory.
	dataDir, err := resolveDataDir()
	if err != nil {
		stderr("failed to resolve data dir: %s", err)
		os.Exit(1)
	}
	stderr("data dir: %s", dataDir)

	// Open the application store (whatsapp.db).
	store, err := NewStore(dataDir)
	if err != nil {
		stderr("failed to open store: %s", err)
		os.Exit(1)
	}
	defer store.Close()

	// Create the WhatsApp client (whatsmeow-backed).
	cli := NewWhatsmeowClient(dataDir, isVerbose())

	// Try to connect using stored credentials. If not paired, the server
	// starts anyway — the user calls login to begin QR pairing.
	if err := cli.Connect(context.Background()); err != nil {
		// ErrNotPaired is expected on first run — not fatal.
		if err != ErrNotPaired {
			stderr("connect warning: %s (login may be needed)", err)
		}
	}

	// Start the ingester goroutine — sole writer to whatsapp.db.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingester := NewIngester(store)
	go ingester.Run(ctx, cli.Events(ctx))

	// Graceful shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		stderr("shutting down...")
		cli.Disconnect()
		cancel()
	}()

	// Build and register the MCP server.
	s := server.NewMCPServer("nusashell-whatsapp", "0.1.4",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)
	registerTools(s, cli, store, ingester)

	// Serve over stdio — diagnostics to stderr, stdout reserved for MCP.
	if err := mcpkit.ServeStdio(s, "nusashell-whatsapp"); err != nil {
		stderr("server error: %s", err)
	}
}
