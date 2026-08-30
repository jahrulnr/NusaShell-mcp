// Package main is the NusaShell Telegram MCP server.
//
// Links a Telegram bot (Bot API, not MTProto) via github.com/mymmrac/telego
// and exposes messages, chats, and approval flows through MCP tools.
//
// Tools (registered in tools.go): status, login, logout, list_chats, get_chat,
// get_messages, search_messages, send_message, send_media, send_inline_buttons,
// edit_message, delete_message, answer_callback, send_chat_action,
// list_pending_approvals, add_to_allowlist, remove_from_allowlist,
// get_chat_history, request_sync, set_privacy_mode.
//
// Data: {NUSASHELL_USER_DATA}/plugins-data/nusashell.telegram/
//
//	bot-token   — Bot API token (mode 0600)
//	telegram.db — application DB (messages, chats, approvals, allowlist;
//	              SQLite WAL + FTS5)
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mark3labs/mcp-go/server"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
)

func main() {
	// Resolve the database file path (and its parent data directory).
	dbPath := mcpkit.MustResolveDataFile(
		"NUSASHELL_TELEGRAM_DATA_FILE",
		"nusashell.telegram",
		"telegram.db",
		"telegram-data",
	)
	dataDir := filepath.Dir(dbPath)
	stderr("data dir: %s", dataDir)

	// Open the application store (telegram.db).
	store, err := NewStore(dataDir)
	if err != nil {
		stderr("failed to open store: %s", err)
		os.Exit(1)
	}
	defer store.Close()

	// Create the Telegram bot client (telego-backed). cli is typed as the
	// TelegramRuntime interface so main.go can swap in a FakeClient when
	// MOCK_ENABLED=1 — both implementations share the Connect/Events/Disconnect
	// lifecycle methods and the Client contract.
	verbose := os.Getenv("NUSASHELL_TELEGRAM_DEBUG") == "1" ||
		os.Getenv("NUSASHELL_TELEGRAM_DEBUG") == "true"
	mockEnabled := os.Getenv("MOCK_ENABLED") == "1"
	var cli TelegramRuntime

	if mockEnabled {
		stderr("MOCK_ENABLED=1 — using fake client (mock data)")
		cli = NewFakeClient(store)
	} else {
		cli = NewTelegramClient(store, dataDir, verbose)
	}

	// Try to connect using a stored token. ErrNotPaired is expected on first
	// run — the bot is NOT connected yet; tools will return ErrNotConnected
	// until the user calls login. Other connect errors are non-fatal warnings.
	if err := cli.Connect(context.Background()); err != nil {
		if !errors.Is(err, ErrNotPaired) {
			stderr("connect warning: %s (login may be needed)", err)
		}
	}

	// Start the ingester goroutine — sole writer to telegram.db. It drains the
	// normalized event channel (TelegramEvent values) produced by the client.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingester := NewIngester(store)

	// Build the MCP server before wiring the push hook so the notifier
	// closure captures a non-nil server (a message may arrive at any time
	// once polling starts).
	s := server.NewMCPServer("nusashell.telegram", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	// Push hook: after an inbound message lands in the store, notify the host
	// over MCP (server→client notification) so event-driven automation can
	// react without polling. The host translates notifications/message into a
	// domain event (e.g. "telegram.message") and matches when-triggers.
	ingester.WithInboundNotify(func(ev TelegramEvent) {
		subject := ev.SenderName()
		if subject == "" {
			subject = ev.ChatName()
		}
		if subject == "" {
			subject = ev.ChatID()
		}
		s.SendNotificationToAllClients(notificationMessageMethod, map[string]any{
			"plugin":     "nusashell.telegram",
			"event":      "message",
			"chat_id":    ev.ChatID(),
			"message_id": ev.MessageID(),
			"chat_type":  ev.ChatType(),
			"subject":    subject,
			"text":       truncateText(ev.Text(), 200),
			"from_me":    ev.FromMe(),
		})
	})

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

	registerTools(s, cli, store, ingester)

	// Serve over stdio — diagnostics to stderr, stdout reserved for MCP.
	if err := mcpkit.ServeStdio(s, "nusashell.telegram"); err != nil {
		stderr("server error: %s", err)
	}
}
