package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// tool names — short, scoped to the plugin namespace.
const (
	toolStatus        = "status"
	toolLogin         = "login"
	toolPairWithCode  = "pair_with_code"
	toolLogout        = "logout"
	toolListChats     = "list_chats"
	toolGetChat       = "get_chat"
	toolListContacts  = "list_contacts"
	toolListGroups    = "list_groups"
	toolSendMessage   = "send_message"
	toolSendMedia     = "send_media"
	toolReact         = "react"
	toolMarkRead      = "mark_read"
	toolGetMessages   = "get_messages"
	toolSearchMsgs    = "search_messages"
	toolDownloadMedia = "download_media"
	toolRequestSync   = "request_sync"

	maxSendMediaBytes int64 = 50 << 20
)

// registerTools wires all MCP tools onto the server.
func registerTools(s *server.MCPServer, cli Client, store *Store, ingester *Ingester) {
	s.AddTool(mcp.NewTool(toolStatus,
		mcp.WithDescription("Report WhatsApp bridge status: connection state, linked device JID, ingestion freshness, and database row counts. Call this first before any send or read operation."),
	), handleStatus(cli, store, ingester))

	s.AddTool(mcp.NewTool(toolLogin,
		mcp.WithDescription("Start WhatsApp QR login flow. Returns the QR code string to scan with your phone (WhatsApp → Settings → Linked Devices → Link a Device). The QR expires in ~60 seconds; re-call login for a fresh one. If QR scanning is not viable (e.g. camera blocked, server rejecting repeated QR attempts), use pair_with_code instead — it issues an 8-character code you can type on the phone."),
	), handleLogin(cli))

	s.AddTool(mcp.NewTool(toolPairWithCode,
		mcp.WithDescription("Start WhatsApp phone-number pairing flow as an alternative to QR login. The phone number is auto-normalized to E.164 (accepts '+62 812-3456-7890', '0812-3456-7890', '6281234567890', etc. — default country code is 62/Indonesia). Returns a short dash-formatted code (e.g. 'ABCD-1234') that you must enter on the phone (WhatsApp → Settings → Linked Devices → Link with phone number). The code expires after ~120 seconds. Note: this pairing method may not be available for all WhatsApp accounts (consumer accounts sometimes have it disabled at the server level)."),
		mcp.WithString("phone",
			mcp.Required(),
			mcp.Description("Phone number in any common format: '+62 812-3456-7890', '0812-3456-7890', or '6281234567890'. Plugin strips non-digits, drops leading 0s, and prepends the default country code (62) if missing."),
		),
	), handlePairWithCode(cli))

	s.AddTool(mcp.NewTool(toolLogout,
		mcp.WithDescription("Disconnect and clear WhatsApp auth state. The account must be re-linked via login afterwards."),
	), handleLogout(cli))

	s.AddTool(mcp.NewTool(toolListChats,
		mcp.WithDescription("List recent WhatsApp chats, newest activity first. Each chat carries its JID, kind (dm/group), name, last message preview, and unread count."),
		mcp.WithString("kind",
			mcp.Description("Filter by chat kind: 'dm', 'group', or 'channel'. Omit for all."),
			mcp.Enum("dm", "group", "channel"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max chats to return (default 50, max 200)."),
			mcp.Min(1.0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Pagination offset (default 0)."),
			mcp.Min(0.0),
		),
	), handleListChats(store))

	s.AddTool(mcp.NewTool(toolGetChat,
		mcp.WithDescription("Get full chat metadata by JID. For groups, includes participants with admin flags."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("The chat JID (e.g. '12025551234@s.whatsapp.net' for DM, '120363...@g.us' for group)."),
		),
	), handleGetChat(store))

	s.AddTool(mcp.NewTool(toolListContacts,
		mcp.WithDescription("Search contacts by substring across push name, business name, phone, and JID. Omit query to list all."),
		mcp.WithString("query",
			mcp.Description("Substring to search for (case-insensitive)."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max contacts to return (default 50, max 200)."),
			mcp.Min(1.0),
		),
	), handleListContacts(store))

	s.AddTool(mcp.NewTool(toolListGroups,
		mcp.WithDescription("Search groups you're in by substring across name, topic, and JID. Omit query to list all."),
		mcp.WithString("query",
			mcp.Description("Substring to search for (case-insensitive)."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max groups to return (default 50, max 200)."),
			mcp.Min(1.0),
		),
	), handleListGroups(store))

	s.AddTool(mcp.NewTool(toolSendMessage,
		mcp.WithDescription("Send a text message to a WhatsApp chat. Markdown is converted to WhatsApp formatting (**bold** → *bold*, ~~strike~~ → ~strike~, lists, blockquotes). Text longer than 4096 chars is split into multiple messages. Use reply_to_id to quote a specific message — the quote attaches to the first chunk. Confirm the chat_jid and content before sending — this delivers a real message to a real person."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("Target chat JID (obtain from list_chats, list_contacts, or list_groups)."),
		),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("Message text. Markdown is converted to WhatsApp formatting before delivery."),
			mcp.MaxLength(65536),
		),
		mcp.WithString("reply_to_id",
			mcp.Description("Optional message ID to quote (reply to)."),
		),
	), handleSendMessage(cli, store))

	s.AddTool(mcp.NewTool(toolSendMedia,
		mcp.WithDescription("Send a regular file from disk as a WhatsApp attachment (maximum 50 MiB). Kind is inferred from MIME type or filename if omitted. Images appear as photos, videos play inline, audio sends as voice, other files arrive as documents. Caption markdown is converted to WhatsApp formatting."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("Target chat JID."),
		),
		mcp.WithString("file_path",
			mcp.Required(),
			mcp.Description("Absolute path to the file on disk."),
		),
		mcp.WithString("kind",
			mcp.Description("Media kind override: 'image', 'video', 'audio', or 'document'. Inferred from MIME/extension if omitted."),
			mcp.Enum("image", "video", "audio", "document"),
		),
		mcp.WithString("caption",
			mcp.Description("Optional caption (for images, videos, documents)."),
		),
		mcp.WithString("reply_to_id",
			mcp.Description("Optional message ID to quote (reply to)."),
		),
	), handleSendMedia(cli, store))

	s.AddTool(mcp.NewTool(toolReact,
		mcp.WithDescription("Add or remove a reaction on a WhatsApp message. Empty emoji removes the reaction."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("Chat JID containing the message."),
		),
		mcp.WithString("message_id",
			mcp.Required(),
			mcp.Description("The message ID to react to."),
		),
		mcp.WithString("emoji",
			mcp.Description("Reaction emoji (e.g. '👍'). Empty string removes the reaction."),
		),
	), handleReact(cli))

	s.AddTool(mcp.NewTool(toolMarkRead,
		mcp.WithDescription("Mark a WhatsApp chat as read up to a specific message ID (or latest if omitted). Clears the unread badge."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("Chat JID to mark read."),
		),
		mcp.WithString("up_to_message_id",
			mcp.Description("Message ID to mark read up to. Omit to mark the latest message read."),
		),
	), handleMarkRead(cli, store))

	s.AddTool(mcp.NewTool(toolGetMessages,
		mcp.WithDescription("Page through messages in a chat, newest first. Uses cursor-based pagination — pass the timestamp of the oldest message in the previous page as the cursor for the next page."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("Chat JID to read messages from."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max messages to return (default 50, max 200)."),
			mcp.Min(1.0),
		),
		mcp.WithNumber("cursor",
			mcp.Description("Pagination cursor: timestamp of the oldest message from the previous page. Omit or 0 for the first page (newest)."),
			mcp.Min(0.0),
		),
	), handleGetMessages(store))

	s.AddTool(mcp.NewTool(toolSearchMsgs,
		mcp.WithDescription("Full-text search across all WhatsApp message text and media captions. Supports FTS5 query syntax (AND, OR, NOT, *, :). Optional filters: chat_jid, sender_jid, since, until."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query. Plain text does a phrase search; use FTS5 operators for advanced queries."),
		),
		mcp.WithString("chat_jid",
			mcp.Description("Filter to a specific chat JID."),
		),
		mcp.WithString("sender_jid",
			mcp.Description("Filter to a specific sender JID."),
		),
		mcp.WithNumber("since",
			mcp.Description("Unix timestamp: only messages at or after this time."),
			mcp.Min(0.0),
		),
		mcp.WithNumber("until",
			mcp.Description("Unix timestamp: only messages at or before this time."),
			mcp.Min(0.0),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 50, max 200)."),
			mcp.Min(1.0),
		),
	), handleSearchMessages(store))

	s.AddTool(mcp.NewTool(toolDownloadMedia,
		mcp.WithDescription("Download a media attachment for a message. Fetches and caches the blob under the plugin data directory. Returns the local file path. Idempotent — repeated calls return the cached path without re-downloading."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("Chat JID containing the media message."),
		),
		mcp.WithString("message_id",
			mcp.Required(),
			mcp.Description("The media message ID."),
		),
	), handleDownloadMedia(cli, store))

	s.AddTool(mcp.NewTool(toolRequestSync,
		mcp.WithDescription("Ask WhatsApp to backfill older messages for a specific chat by sending an on-demand history-sync request to your phone. Use when earlier messages are missing. The response arrives asynchronously as history sync data — check get_messages after a few seconds. Recovery depends on WhatsApp's server-side retention."),
		mcp.WithString("chat_jid",
			mcp.Required(),
			mcp.Description("Chat JID to backfill."),
		),
	), handleRequestSync(cli))
}

// --- Handlers ---

// outboundDeliveryState reflects what whatsmeow's SendMessage confirms: the
// WhatsApp server accepted the send. It is not a recipient delivery/read receipt.
func outboundDeliveryState() string { return "server_acknowledged" }

func eventFreshness(at time.Time) (int64, int) {
	if at.IsZero() {
		return 0, 0
	}
	ago := int(time.Since(at).Seconds())
	if ago < 0 {
		ago = 0
	}
	return at.Unix(), ago
}

func clampPagination(limit, offset int) (int, int) {
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func clampLimit(limit int) int {
	limit, _ = clampPagination(limit, 0)
	return limit
}

func handleStatus(cli Client, store *Store, ingester *Ingester) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		state := cli.State()
		msgCount, _ := store.CountMessages(ctx)
		chatCount, _ := store.CountChats(ctx)
		contactCount, _ := store.CountContacts(ctx)

		lastEventAt, lastEventAgo := eventFreshness(ingester.LastEventAt())
		result := map[string]any{
			"paired":              state.Paired,
			"transport_connected": state.TransportConnected,
			"connected":           state.Connected,
			"device_jid":          state.DeviceJID,
			"awaiting_qr":         state.AwaitingQR,
			"message_count":       msgCount,
			"chat_count":          chatCount,
			"contact_count":       contactCount,
			"last_event_at":       lastEventAt,
			"last_event_ago":      lastEventAgo,
		}
		if !state.Paired {
			result["hint"] = "Not paired. Call login to start QR pairing."
		} else if !state.Connected {
			result["hint"] = "Paired but disconnected. Sends will fail — retry shortly."
		}
		return jsonResult(result)
	}
}

func handleLogin(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		state := cli.State()
		if state.Paired {
			return errorResult(fmt.Errorf("already paired as %s. Call logout first to re-link.", state.DeviceJID)), nil
		}

		qrCh, err := cli.StartQR(ctx)
		if err != nil {
			return errorResult(fmt.Errorf("start qr: %w", err)), nil
		}

		// Wait for the first QR code (non-blocking with timeout).
		select {
		case qr, ok := <-qrCh:
			if !ok {
				return errorResult(fmt.Errorf("qr channel closed before code was received")), nil
			}
			return jsonResult(map[string]any{
				"qr_code":    qr.Code,
				"expires_at": qr.ExpiresAt.Unix(),
				"hint":       "Scan this QR with your phone: WhatsApp → Settings → Linked Devices → Link a Device. The QR rotates; re-call login for a fresh one if it expires.",
			})
		case <-time.After(10 * time.Second):
			return errorResult(fmt.Errorf("timed out waiting for QR code")), nil
		case <-ctx.Done():
			return errorResult(ctx.Err()), nil
		}
	}
}

func handlePairWithCode(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		state := cli.State()
		if state.Paired {
			return errorResult(fmt.Errorf("already paired as %s. Call logout first to re-link.", state.DeviceJID)), nil
		}

		rawPhone := argString(req.GetArguments(), "phone")
		if rawPhone == "" {
			return errorResult(fmt.Errorf("phone is required")), nil
		}

		// Normalize to E.164 — default country code is 62 (Indonesia). The
		// helper accepts '+62...', '0812...', and '62812...' and returns a
		// clean error if the number is too short or otherwise invalid,
		// which is friendlier than a cryptic whatsmeow error.
		phone, err := normalizePhone(rawPhone, "")
		if err != nil {
			return errorResult(fmt.Errorf("normalize phone: %w", err)), nil
		}

		pairCh, err := cli.StartPairCode(ctx, phone)
		if err != nil {
			return errorResult(fmt.Errorf("start pair code: %w", err)), nil
		}

		// Wait for the single pair-code event. The same channel will close
		// once the underlying drain goroutine observes "success" or context
		// cancel, but the handler returns the code immediately and lets the
		// user submit it asynchronously on the phone. We do NOT block on
		// "success" here because the user may need >2 minutes to type the
		// code; the connection itself stays open in the background.
		select {
		case pc, ok := <-pairCh:
			if !ok {
				return errorResult(fmt.Errorf("pair code channel closed before code was received")), nil
			}
			return jsonResult(map[string]any{
				"pair_code":  pc.Code,
				"expires_at": pc.ExpiresAt.Unix(),
				"phone":      phone,
				"hint":       "Enter this code on your phone: WhatsApp → Settings → Linked Devices → Link with phone number. The code expires after ~120 seconds. If pairing fails, the connection will close — call pair_with_code again for a fresh code.",
			})
		case <-time.After(15 * time.Second):
			return errorResult(fmt.Errorf("timed out waiting for pair code from WhatsApp server")), nil
		case <-ctx.Done():
			return errorResult(ctx.Err()), nil
		}
	}
}

func handleLogout(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := cli.Logout(ctx); err != nil {
			return errorResult(fmt.Errorf("logout: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "logged out", "hint": "Call login to re-link."})
	}
}

func handleListChats(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		kind := argString(args, "kind")
		limit, offset := clampPagination(argInt(args, "limit", 50), argInt(args, "offset", 0))

		chats, err := store.ListChats(ctx, kind, limit, offset)
		if err != nil {
			return errorResult(fmt.Errorf("list chats: %w", err)), nil
		}
		return jsonResult(map[string]any{"chats": chats, "total": len(chats), "kind": kind, "limit": limit, "offset": offset})
	}
}

func handleGetChat(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		chatJID := argString(req.GetArguments(), "chat_jid")
		if chatJID == "" {
			return errorResult(fmt.Errorf("chat_jid is required")), nil
		}

		chat, err := store.GetChat(ctx, chatJID)
		if err != nil {
			return errorResult(fmt.Errorf("get chat: %w", err)), nil
		}
		if chat == nil {
			return errorResult(fmt.Errorf("no chat found for JID %s", chatJID)), nil
		}

		result := map[string]any{
			"chat": chat,
		}
		// Include participants for groups.
		if chat.Kind == "group" {
			participants, err := store.GetGroupParticipants(ctx, chatJID)
			if err == nil {
				result["participants"] = participants
			}
		}
		return jsonResult(result)
	}
}

func handleListContacts(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := argString(req.GetArguments(), "query")
		limit := clampLimit(argInt(req.GetArguments(), "limit", 50))

		contacts, err := store.ListContacts(ctx, query, limit)
		if err != nil {
			return errorResult(fmt.Errorf("list contacts: %w", err)), nil
		}
		return jsonResult(map[string]any{"contacts": contacts, "total": len(contacts), "query": query, "limit": limit})
	}
}

func handleListGroups(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := argString(req.GetArguments(), "query")
		limit := clampLimit(argInt(req.GetArguments(), "limit", 50))

		groups, err := store.ListGroups(ctx, query, limit)
		if err != nil {
			return errorResult(fmt.Errorf("list groups: %w", err)), nil
		}
		return jsonResult(map[string]any{"groups": groups, "total": len(groups), "query": query, "limit": limit})
	}
}

func recordOutboundMessage(ctx context.Context, store *Store, chatJID string, result SendResult, text, replyToID string) {
	if store == nil {
		return
	}
	ts := result.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	message := EventMessage{
		ChatJID:   chatJID,
		ID:        result.MessageID,
		Text:      text,
		Timestamp: ts,
		QuotedID:  replyToID,
		FromMe:    true,
	}
	if err := store.InsertMessage(ctx, message); err != nil {
		stderr("send: mirror outbound message: %s", err)
	}
	if err := store.UpsertChat(ctx, chatJID, chatKindFromJID(chatJID), "", text, ts.Unix()); err != nil {
		stderr("send: mirror outbound chat: %s", err)
	}
}

func recordOutboundMedia(ctx context.Context, store *Store, chatJID string, result SendResult, kind, mimeType string, size int64, caption string) {
	if store == nil {
		return
	}
	ts := result.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	media := EventMedia{
		ChatJID:   chatJID,
		ID:        result.MessageID,
		Caption:   caption,
		Timestamp: ts,
		Kind:      kind,
		MimeType:  mimeType,
		Size:      size,
		FromMe:    true,
	}
	if err := store.InsertMediaMessage(ctx, media); err != nil {
		stderr("send: mirror outbound media message: %s", err)
	}
	if err := store.UpsertMedia(ctx, media); err != nil {
		stderr("send: mirror outbound media metadata: %s", err)
	}
	if err := store.UpsertChat(ctx, chatJID, chatKindFromJID(chatJID), "", caption, ts.Unix()); err != nil {
		stderr("send: mirror outbound media chat: %s", err)
	}
}

func handleSendMessage(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatJID := argString(args, "chat_jid")
		text := argString(args, "text")
		replyToID := argString(args, "reply_to_id")

		if chatJID == "" {
			return errorResult(fmt.Errorf("chat_jid is required")), nil
		}
		if text == "" {
			return errorResult(fmt.Errorf("text is required")), nil
		}

		result, err := cli.SendText(ctx, chatJID, text, replyToID)
		if err != nil {
			return errorResult(fmt.Errorf("send message: %w", err)), nil
		}

		// Mirror the successful send locally so the UI and read tools show it
		// immediately. The provider's own echo is idempotent on the same key.
		recordOutboundMessage(ctx, store, chatJID, result, text, replyToID)
		_ = store.ResetUnread(ctx, chatJID)

		return jsonResult(map[string]any{
			"message_id":     result.MessageID,
			"timestamp":      result.Timestamp.Unix(),
			"chat_jid":       chatJID,
			"delivery_state": outboundDeliveryState(),
			"retryable":      false,
		})
	}
}

func handleSendMedia(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatJID := argString(args, "chat_jid")
		filePath := argString(args, "file_path")
		kind := argString(args, "kind")
		caption := argString(args, "caption")
		replyToID := argString(args, "reply_to_id")

		if chatJID == "" {
			return errorResult(fmt.Errorf("chat_jid is required")), nil
		}
		if filePath == "" {
			return errorResult(fmt.Errorf("file_path is required")), nil
		}

		info, err := os.Stat(filePath)
		if err != nil {
			return errorResult(fmt.Errorf("stat file %s: %w", filePath, err)), nil
		}
		if err := validateMediaFile(info); err != nil {
			return errorResult(fmt.Errorf("validate file %s: %w", filePath, err)), nil
		}

		// Read the file from disk after validating its type and size.
		data, err := os.ReadFile(filePath)
		if err != nil {
			return errorResult(fmt.Errorf("read file %s: %w", filePath, err)), nil
		}

		// Infer kind if not specified.
		mimeType := detectMimeType(filePath)
		if kind == "" {
			kind = inferMediaKind(mimeType, filepath.Base(filePath))
		}

		result, err := cli.SendMedia(ctx, chatJID, kind, data, mimeType, caption, replyToID)
		if err != nil {
			return errorResult(fmt.Errorf("send media: %w", err)), nil
		}

		recordOutboundMedia(ctx, store, chatJID, result, kind, mimeType, int64(len(data)), caption)
		_ = store.ResetUnread(ctx, chatJID)

		return jsonResult(map[string]any{
			"message_id":     result.MessageID,
			"timestamp":      result.Timestamp.Unix(),
			"chat_jid":       chatJID,
			"kind":           kind,
			"size":           len(data),
			"delivery_state": outboundDeliveryState(),
		})
	}
}

func handleReact(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatJID := argString(args, "chat_jid")
		messageID := argString(args, "message_id")
		emoji := argString(args, "emoji")

		if chatJID == "" || messageID == "" {
			return errorResult(fmt.Errorf("chat_jid and message_id are required")), nil
		}

		if err := cli.React(ctx, chatJID, messageID, emoji); err != nil {
			return errorResult(fmt.Errorf("react: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "ok", "chat_jid": chatJID, "message_id": messageID, "emoji": emoji})
	}
}

func handleMarkRead(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatJID := argString(args, "chat_jid")
		upToID := argString(args, "up_to_message_id")

		if chatJID == "" {
			return errorResult(fmt.Errorf("chat_jid is required")), nil
		}

		if err := cli.MarkRead(ctx, chatJID, upToID); err != nil {
			return errorResult(fmt.Errorf("mark read: %w", err)), nil
		}
		_ = store.ResetUnread(ctx, chatJID)
		return jsonResult(map[string]any{"status": "ok", "chat_jid": chatJID})
	}
}

func handleGetMessages(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatJID := argString(args, "chat_jid")
		limit := clampLimit(argInt(args, "limit", 50))
		cursor := argInt(args, "cursor", 0)

		if chatJID == "" {
			return errorResult(fmt.Errorf("chat_jid is required")), nil
		}

		messages, err := store.GetMessages(ctx, chatJID, int64(cursor), limit)
		if err != nil {
			return errorResult(fmt.Errorf("get messages: %w", err)), nil
		}
		if err := store.ResetUnread(ctx, chatJID); err != nil {
			stderr("get messages: reset unread: %s", err)
		}

		// Compute next cursor (timestamp of the oldest message in this page).
		nextCursor := int64(0)
		if len(messages) > 0 {
			nextCursor = messages[len(messages)-1].Timestamp
		}

		return jsonResult(map[string]any{
			"messages":    messages,
			"total":       len(messages),
			"chat_jid":    chatJID,
			"limit":       limit,
			"cursor":      cursor,
			"next_cursor": nextCursor,
		})
	}
}

func handleSearchMessages(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query := argString(args, "query")
		chatJID := argString(args, "chat_jid")
		senderJID := argString(args, "sender_jid")
		since := argInt(args, "since", 0)
		until := argInt(args, "until", 0)
		limit := clampLimit(argInt(args, "limit", 50))

		if query == "" {
			return errorResult(fmt.Errorf("query is required")), nil
		}

		messages, err := store.SearchMessages(ctx, query, chatJID, senderJID, int64(since), int64(until), limit)
		if err != nil {
			return errorResult(fmt.Errorf("search messages: %w", err)), nil
		}

		return jsonResult(map[string]any{
			"messages":   messages,
			"total":      len(messages),
			"query":      query,
			"chat_jid":   chatJID,
			"sender_jid": senderJID,
			"since":      since,
			"until":      until,
			"limit":      limit,
		})
	}
}

func handleDownloadMedia(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatJID := argString(args, "chat_jid")
		messageID := argString(args, "message_id")

		if chatJID == "" || messageID == "" {
			return errorResult(fmt.Errorf("chat_jid and message_id are required")), nil
		}

		// Check if already downloaded.
		media, err := store.GetMedia(ctx, chatJID, messageID)
		if err != nil {
			return errorResult(fmt.Errorf("get media metadata: %w", err)), nil
		}
		if media == nil {
			return errorResult(fmt.Errorf("no media found for message %s in chat %s", messageID, chatJID)), nil
		}
		if media.Downloaded && media.LocalPath != "" {
			if _, err := os.Stat(media.LocalPath); err == nil {
				return jsonResult(map[string]any{
					"local_path": media.LocalPath,
					"kind":       media.Kind,
					"mime_type":  media.MimeType,
					"size":       media.Size,
					"cached":     true,
				})
			}
		}

		// Fetch the download reference.
		ref, err := store.GetMediaDownloadRef(ctx, chatJID, messageID)
		if err != nil {
			return errorResult(fmt.Errorf("get download ref: %w", err)), nil
		}
		if ref == "" {
			return errorResult(fmt.Errorf("no download reference for message %s — the media may be too old or from an imported history", messageID)), nil
		}

		// Download and cache.
		result, err := cli.Download(ctx, ref)
		if err != nil {
			return errorResult(fmt.Errorf("download media: %w", err)), nil
		}

		// Compute sha256 for cache filename.
		hash := sha256.Sum256(result.Bytes)
		sha256hex := hex.EncodeToString(hash[:])
		ext := extensionForMime(result.MimeType)
		localPath := store.MediaPath(sha256hex, ext)

		if err := writeMediaCache(localPath, result.Bytes); err != nil {
			return errorResult(fmt.Errorf("write media cache: %w", err)), nil
		}

		// Update the media row.
		_ = store.UpdateMediaDownloaded(ctx, chatJID, messageID, localPath, sha256hex, int64(len(result.Bytes)))

		return jsonResult(map[string]any{
			"local_path": localPath,
			"kind":       media.Kind,
			"mime_type":  result.MimeType,
			"size":       len(result.Bytes),
			"sha256":     sha256hex,
			"cached":     false,
		})
	}
}

func handleRequestSync(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		chatJID := argString(req.GetArguments(), "chat_jid")
		if chatJID == "" {
			return errorResult(fmt.Errorf("chat_jid is required")), nil
		}

		if err := cli.RequestSync(ctx, chatJID); err != nil {
			return errorResult(fmt.Errorf("request sync: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "sync requested", "chat_jid": chatJID, "hint": "History backfill is asynchronous; check get_messages after a few seconds."})
	}
}

func validateMediaFile(info os.FileInfo) error {
	if info == nil {
		return fmt.Errorf("file metadata is missing")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("file is not a regular file")
	}
	if info.Size() > maxSendMediaBytes {
		return fmt.Errorf("file is %d bytes, maximum is %d bytes", info.Size(), maxSendMediaBytes)
	}
	return nil
}

func writeMediaCache(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	// Re-assert 0600 for a pre-existing file (WriteFile keeps its old mode).
	// Best-effort on Windows, where mode bits do not exist (see token write).
	return os.Chmod(path, 0o600)
}

// detectMimeType returns a MIME type based on file extension. This is a
// lightweight heuristic — the actual content type is determined by WhatsApp.
func detectMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".wav":
		return "audio/wav"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// extensionForMime returns a file extension for a MIME type.
func extensionForMime(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	case "video/quicktime":
		return "mov"
	case "video/webm":
		return "webm"
	case "audio/mpeg":
		return "mp3"
	case "audio/ogg":
		return "ogg"
	case "audio/opus":
		return "opus"
	case "audio/mp4":
		return "m4a"
	case "audio/aac":
		return "aac"
	case "audio/wav":
		return "wav"
	case "application/pdf":
		return "pdf"
	default:
		return "bin"
	}
}
