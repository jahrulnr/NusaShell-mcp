package main

// tools.go registers the 20 MCP tools for the nusashell.telegram plugin.
//
// Handler pattern follows the WhatsApp plugin (whatsapp/mcp/tools.go): each
// handler is a server.ToolHandlerFunc closure returned by a handleXxx factory,
// and results are emitted via jsonResult / errorResult (helpers.go), which wrap
// mcp.NewTextContent. This matches mark3labs/mcp-go v0.58's
//
//	type ToolHandlerFunc func(ctx context.Context, request mcp.CallToolRequest)
//	                            (*mcp.CallToolResult, error)
//
// and MCPServer.AddTool(tool mcp.Tool, handler ToolHandlerFunc).
//
// Types this file depends on (defined in sibling files, same package main):
//
//	tgclient.go:
//	  type PairState struct { Paired, Connected bool; BotName, BotID string; AwaitingToken bool }
//	  type SendResult struct { MessageID int64; Timestamp time.Time }
//	  type InlineButton struct { Label, CallbackData string }
//	  type Client interface { ... }  // State, Login, Logout, SendText, SendMedia,
//	                                 //   SendInlineButtons, EditMessage, DeleteMessage,
//	                                 //   AnswerCallback, SendChatAction, RequestSync
//	store.go:
//	  type ChatRow struct { ID, Type, Name, LastMessage string; LastMessageAt int64; UnreadCount int }
//	  type MessageRow struct { ID, ChatID, SenderName, Text string; Timestamp int64; FromMe bool; EditedAt *int64 }
//	  type ApprovalRow struct { ID, ChatID, MessageID, Text, SenderID string; Time int64; Status string }
//	  // Methods: CountMessages, CountChats, ListChats, GetChat, SearchMessages,
//	  //   ListPendingApprovals, AddToAllowlist, RemoveFromAllowlist, ResetUnread,
//	  //   GetMeta, SetMeta. GetMessagesCursor and GetChatHistory are defined at
//	  //   the bottom of this file as package-local Store extensions.
//	ingest.go:
//	  type Ingester struct{ ... }  // LastEventAt() time.Time
//	  func stderr(format string, args ...any)  // shared package logger
//	main.go:
//	  var tgLog *log.Logger  // stderr logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// tool names — short, scoped to the plugin namespace.
const (
	toolStatus            = "status"
	toolLogin             = "login"
	toolLogout            = "logout"
	toolListChats         = "list_chats"
	toolGetChat           = "get_chat"
	toolGetMessages       = "get_messages"
	toolSearchMessages    = "search_messages"
	toolSendMessage       = "send_message"
	toolSendMedia         = "send_media"
	toolSendInlineButtons = "send_inline_buttons"
	toolEditMessage       = "edit_message"
	toolDeleteMessage     = "delete_message"
	toolAnswerCallback    = "answer_callback"
	toolSendChatAction    = "send_chat_action"
	toolListPendingApprov = "list_pending_approvals"
	toolAddToAllowlist    = "add_to_allowlist"
	toolRemoveFromAllow   = "remove_from_allowlist"
	toolGetChatHistory    = "get_chat_history"
	toolRequestSync       = "request_sync"
	toolSetPrivacyMode    = "set_privacy_mode"
)

// Telegram message text cap in code points (sendMessage). chunkText is
// rune-aware so we never exceed this server-side.
const telegramTextCap = 4096

// sendMediaMaxBytes is the cloud Bot API upload ceiling (50 MB). Larger files
// require a self-hosted local Bot API server.
const sendMediaMaxBytes = 50 * 1024 * 1024

// privacyModeMetaKey is the meta key under which the plugin's local
// privacy/allowlist enforcement flag is persisted ("1" on, "0" off).
const privacyModeMetaKey = "privacy_mode"

// registerTools wires all MCP tools onto the server.
func registerTools(s *server.MCPServer, cli Client, store *Store, ingester *Ingester) {
	s.AddTool(mcp.NewTool(toolStatus,
		mcp.WithDescription("Report Telegram bridge status: pairing state (token stored), connection state (long polling active), bot identity, privacy-mode flag, ingestion freshness, and database row counts. Call this first before any send or read operation."),
	), handleStatus(cli, store, ingester))

	s.AddTool(mcp.NewTool(toolLogin,
		mcp.WithDescription("Validate and store a Telegram bot token. Calls getMe to confirm the token is valid, persists it to disk (file mode 0600), and starts long polling. If a token is already stored, call logout first. The token comes from @BotFather and looks like '123456:ABC-DEF...'."),
		mcp.WithString("bot_token",
			mcp.Required(),
			mcp.Description("Bot token from @BotFather, format '123456:ABC-DEF...'."),
		),
	), handleLogin(cli))

	s.AddTool(mcp.NewTool(toolLogout,
		mcp.WithDescription("Delete the stored bot token and stop polling. The bot must re-login afterwards."),
	), handleLogout(cli))

	s.AddTool(mcp.NewTool(toolListChats,
		mcp.WithDescription("List recent Telegram chats observed by the bot, newest activity first. Each chat carries its id, type, name, last message preview, and unread count. Sourced from the local SQLite mirror, not a server query — only chats the bot has received updates from appear here."),
		mcp.WithString("kind",
			mcp.Description("Filter by chat type: 'dm', 'group', or 'channel'. Omit for all."),
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
		mcp.WithDescription("Get full chat metadata by chat_id from the local mirror."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Target chat id (int64-as-string, supergroups are '-100'-prefixed) or '@username'."),
		),
	), handleGetChat(store))

	s.AddTool(mcp.NewTool(toolGetMessages,
		mcp.WithDescription("Page through messages in a chat, newest first. Cursor-based pagination — pass the timestamp of the oldest message in the previous page as the cursor for the next page. Omit or 0 for the first page (newest). Reading marks the chat read in the local store (unread badge clears)."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Chat to read messages from (int64-as-string or '@username')."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max messages to return (default 50, max 200)."),
			mcp.Min(1.0),
		),
		mcp.WithNumber("cursor",
			mcp.Description("Pagination cursor: Unix timestamp (seconds) of the oldest message from the previous page. Omit or 0 for the first page (newest)."),
			mcp.Min(0.0),
		),
	), handleGetMessages(store))

	s.AddTool(mcp.NewTool(toolSearchMessages,
		mcp.WithDescription("Full-text search across all Telegram message text stored locally. Supports FTS5 query syntax (AND, OR, NOT, *, :). Optional filters: chat_id, since, until, limit."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query. Plain text does a phrase search; use FTS5 operators for advanced queries."),
		),
		mcp.WithString("chat_id",
			mcp.Description("Filter results to a specific chat id."),
		),
		mcp.WithNumber("since",
			mcp.Description("Unix timestamp, seconds — only messages at or after this time."),
			mcp.Min(0.0),
		),
		mcp.WithNumber("until",
			mcp.Description("Unix timestamp, seconds — only messages at or before this time."),
			mcp.Min(0.0),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 50, max 200)."),
			mcp.Min(1.0),
		),
	), handleSearchMessages(store))

	s.AddTool(mcp.NewTool(toolSendMessage,
		mcp.WithDescription("Send a text message to a Telegram chat. HTML parse_mode is the default; text longer than 4096 code points is split into multiple messages (the reply attaches to the first chunk). Confirm the chat_id and content before sending — this delivers a real message."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Target chat id (int64-as-string or '@username')."),
		),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("Message text. With HTML parse_mode (default), escape <, >, & — or send plain text with parse_mode empty."),
			mcp.MaxLength(65536),
		),
		mcp.WithNumber("reply_to_message_id",
			mcp.Description("message_id to quote (reply to). Attaches to the first chunk when text is split."),
			mcp.Min(1.0),
		),
		mcp.WithString("parse_mode",
			mcp.Description("Override parse_mode: 'HTML' (default), 'MarkdownV2', or empty string for plain text."),
		),
		mcp.WithBoolean("disable_notification",
			mcp.Description("Send silently (default false)."),
		),
	), handleSendMessage(cli, store))

	s.AddTool(mcp.NewTool(toolSendMedia,
		mcp.WithDescription("Send a file from disk as a Telegram attachment. Kind is inferred from MIME type or filename if omitted. Rejects files larger than 50 MB (cloud Bot API) with a hint to use a local Bot API server. Caption uses HTML parse_mode."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Target chat id (int64-as-string or '@username')."),
		),
		mcp.WithString("file_path",
			mcp.Required(),
			mcp.Description("Absolute path to the file on disk."),
		),
		mcp.WithString("kind",
			mcp.Description("Media kind override: 'photo', 'video', 'audio', or 'document'. Inferred from MIME/extension if omitted."),
			mcp.Enum("photo", "video", "audio", "document"),
		),
		mcp.WithString("caption",
			mcp.Description("Optional caption (HTML parse_mode)."),
		),
		mcp.WithNumber("reply_to_message_id",
			mcp.Description("message_id to quote (reply to)."),
			mcp.Min(1.0),
		),
	), handleSendMedia(cli, store))

	s.AddTool(mcp.NewTool(toolSendInlineButtons,
		mcp.WithDescription("Send a message with an inline keyboard (buttons). Each button has a label and a callback_data string delivered back via telegram.callback_query when pressed. Buttons are arranged in rows. The 'buttons' argument is a JSON array of rows; each row is an array of {label, callback_data} objects, e.g. [[{\"label\":\"Yes\",\"callback_data\":\"yes\"},{\"label\":\"No\",\"callback_data\":\"no\"}]]."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Target chat id (int64-as-string or '@username')."),
		),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("Message text accompanying the buttons (<=4096 code points, HTML parse_mode)."),
			mcp.MaxLength(telegramTextCap),
		),
		mcp.WithString("buttons",
			mcp.Required(),
			mcp.Description("JSON array of rows; each row is an array of {\"label\":\"...\",\"callback_data\":\"...\"} objects. callback_data must be 1-64 bytes."),
		),
		mcp.WithNumber("reply_to_message_id",
			mcp.Description("message_id to quote (reply to)."),
			mcp.Min(1.0),
		),
	), handleSendInlineButtons(cli, store))

	s.AddTool(mcp.NewTool(toolEditMessage,
		mcp.WithDescription("Edit the text of a previously sent message. Text longer than 4096 code points is rejected. HTML parse_mode default."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Chat containing the message (int64-as-string or '@username')."),
		),
		mcp.WithNumber("message_id",
			mcp.Required(),
			mcp.Description("The message id to edit."),
			mcp.Min(1.0),
		),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("New text (<=4096 code points, HTML parse_mode)."),
			mcp.MaxLength(telegramTextCap),
		),
		mcp.WithString("parse_mode",
			mcp.Description("Override parse_mode: 'HTML' (default), 'MarkdownV2', or empty string for plain text."),
		),
	), handleEditMessage(cli))

	s.AddTool(mcp.NewTool(toolDeleteMessage,
		mcp.WithDescription("Delete a message. If the bot has admin rights, it can delete others' messages; otherwise only its own. Irreversible."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Chat containing the message (int64-as-string or '@username')."),
		),
		mcp.WithNumber("message_id",
			mcp.Required(),
			mcp.Description("The message id to delete."),
			mcp.Min(1.0),
		),
	), handleDeleteMessage(cli))

	s.AddTool(mcp.NewTool(toolAnswerCallback,
		mcp.WithDescription("Acknowledge a callback query (button press) so the button stops loading, and resolve the matching pending approval as approved/denied. Optionally show an alert or toast to the user. Must be called within a few seconds of the callback arriving."),
		mcp.WithString("callback_query_id",
			mcp.Required(),
			mcp.Description("The id from the telegram.callback_query event (also the approval id)."),
		),
		mcp.WithString("text",
			mcp.Description("Notification text (0-200 chars)."),
			mcp.MaxLength(200),
		),
		mcp.WithBoolean("show_alert",
			mcp.Description("If true, show as an alert popup (default false)."),
		),
	), handleAnswerCallback(cli, store))

	s.AddTool(mcp.NewTool(toolSendChatAction,
		mcp.WithDescription("Show a typing/uploading indicator in the chat for up to 5 seconds. Use before long operations so the user knows the bot is working."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Target chat id (int64-as-string or '@username')."),
		),
		mcp.WithString("action",
			mcp.Required(),
			mcp.Description("One of: 'typing', 'upload_photo', 'upload_video', 'record_video', 'upload_document', 'find_location'."),
			mcp.Enum("typing", "upload_photo", "upload_video", "record_video", "upload_document", "find_location"),
		),
	), handleSendChatAction(cli))

	s.AddTool(mcp.NewTool(toolListPendingApprov,
		mcp.WithDescription("List pending approval requests — inline-button presses recorded as pending approvals, awaiting resolution via answer_callback. Each entry carries the approval id (callback query id), chat, message, payload, sender, time, and status."),
	), handleListPendingApprovals(store))

	s.AddTool(mcp.NewTool(toolAddToAllowlist,
		mcp.WithDescription("Add a user/chat to the allowlist. When privacy mode is on, only allowlisted users may interact with the bot; others are dropped silently (not errored) so they never leak to automation."),
		mcp.WithString("user_id",
			mcp.Required(),
			mcp.Description("Telegram user/chat to allowlist: numeric user id, @username, or display name (case-insensitive)."),
		),
	), handleAddToAllowlist(store))

	s.AddTool(mcp.NewTool(toolRemoveFromAllow,
		mcp.WithDescription("Remove a user/chat from the allowlist. Future messages from this user are dropped while privacy mode is on."),
		mcp.WithString("user_id",
			mcp.Required(),
			mcp.Description("Telegram user/chat id to remove (int64-as-string)."),
		),
	), handleRemoveFromAllowlist(store))

	s.AddTool(mcp.NewTool(toolGetChatHistory,
		mcp.WithDescription("Return the full local message history observed for a chat, oldest first. This is the same data as get_messages but unpaginated — intended for small chats or exports. Only messages seen since polling started are present; Telegram Bot API cannot backfill history before the bot observed the chat."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Chat to read history for (int64-as-string or '@username')."),
		),
	), handleGetChatHistory(store))

	s.AddTool(mcp.NewTool(toolRequestSync,
		mcp.WithDescription("Request a history sync for a chat. NOTE: the Telegram Bot API has no on-demand backfill mechanism — messages that arrived before polling started cannot be recovered. This tool is reserved for a future local-Bot-API-server or MTProto path; for now it is a no-op that succeeds without a server call."),
		mcp.WithString("chat_id",
			mcp.Required(),
			mcp.Description("Chat id to request sync for (int64-as-string or '@username')."),
		),
	), handleRequestSync(cli))

	s.AddTool(mcp.NewTool(toolSetPrivacyMode,
		mcp.WithDescription("Toggle the plugin's privacy/allowlist enforcement. When on, only allowlisted users may interact with the bot and unknown senders are dropped. When off, the bot accepts messages from anyone. This is a local plugin gate, distinct from Telegram's BotFather privacy mode — disable that separately in @BotFather if the bot must read all group messages."),
		mcp.WithBoolean("enabled",
			mcp.Required(),
			mcp.Description("True to enforce the allowlist (privacy mode on), false to accept all senders."),
		),
	), handleSetPrivacyMode(store))
}

// --- Handlers --------------------------------------------------------------

func handleStatus(cli Client, store *Store, ingester *Ingester) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		state := cli.State()
		msgCount, _ := store.CountMessages(ctx)
		chatCount, _ := store.CountChats(ctx)
		unreadDMs, _ := store.CountUnreadDMs(ctx)
		privacyMode, _ := store.GetMeta(ctx, privacyModeMetaKey)
		allowlist, _ := store.ListAllowlist(ctx)
		if allowlist == nil {
			allowlist = []string{}
		}

		// lastEventAt is 0 (not a year-1 epoch) until the first event lands so
		// consumers can render "—" instead of a nonsense date.
		lastEventAt := int64(0)
		lastEventAgo := 0
		if t := ingester.LastEventAt(); !t.IsZero() {
			lastEventAt = t.Unix()
			lastEventAgo = int(time.Since(t).Seconds())
		}

		result := map[string]any{
			"paired":          state.Paired,
			"connected":       state.Connected,
			"bot_id":          state.BotID,
			"bot_name":        state.BotName,
			"awaiting_token":  state.AwaitingToken,
			"privacy_mode":    privacyMode == "1",
			"message_count":   msgCount,
			"chat_count":      chatCount,
			"unread_dm_count": unreadDMs,
			"last_event_at":   lastEventAt,
			"last_event_ago":  lastEventAgo,
			"allowlist":       allowlist,
		}
		switch {
		case !state.Paired:
			result["hint"] = "Not paired. Call login with a bot token from @BotFather."
		case !state.Connected:
			result["hint"] = "Paired but polling is stopped. Sends and reads may be stale — retry shortly."
		}
		return jsonResult(result)
	}
}

func handleLogin(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s := cli.State(); s.Paired {
			return errorResult(fmt.Errorf("already paired as %s (id %s). Call logout first to re-link.", s.BotName, s.BotID)), nil
		}
		token := strings.TrimSpace(argString(req.GetArguments(), "bot_token"))
		if token == "" {
			return errorResult(fmt.Errorf("bot_token is required")), nil
		}

		state, err := cli.Login(ctx, token)
		if err != nil {
			return errorResult(fmt.Errorf("login: %w", err)), nil
		}
		return jsonResult(map[string]any{
			"paired":    state.Paired,
			"connected": state.Connected,
			"bot_id":    state.BotID,
			"bot_name":  state.BotName,
			"hint":      "Token stored and polling started.",
		})
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
		limit := argInt(args, "limit", 50)
		if limit > 200 {
			limit = 200
		}
		offset := argInt(args, "offset", 0)

		chats, err := store.ListChats(ctx, kind, limit, offset)
		if err != nil {
			return errorResult(fmt.Errorf("list chats: %w", err)), nil
		}
		if chats == nil {
			chats = []ChatRow{}
		}
		return jsonResult(map[string]any{
			"chats":  chats,
			"total":  len(chats),
			"kind":   kind,
			"limit":  limit,
			"offset": offset,
		})
	}
}

func handleGetChat(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		chatID := argString(req.GetArguments(), "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}

		chat, err := store.GetChat(ctx, chatID)
		if err != nil {
			return errorResult(fmt.Errorf("get chat: %w", err)), nil
		}
		if chat == nil {
			return errorResult(fmt.Errorf("no chat found for id %s", chatID)), nil
		}
		return jsonResult(map[string]any{"chat": chat})
	}
}

func handleGetMessages(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatID := argString(args, "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}
		limit := argInt(args, "limit", 50)
		if limit > 200 {
			limit = 200
		}
		cursor := int64(argInt(args, "cursor", 0))

		messages, err := store.GetMessagesCursor(ctx, chatID, cursor, limit)
		if err != nil {
			return errorResult(fmt.Errorf("get messages: %w", err)), nil
		}

		// Reading a chat clears its unread badge locally. Local-only side
		// effect (no Telegram API call); keeps the UI honest about read state.
		if err := store.ResetUnread(ctx, chatID); err != nil {
			stderr("get messages: reset unread: %s", err)
		}
		if messages == nil {
			messages = []MessageRow{}
		}

		nextCursor := int64(0)
		if len(messages) > 0 {
			nextCursor = messages[len(messages)-1].Timestamp
		}
		return jsonResult(map[string]any{
			"messages":    messages,
			"total":       len(messages),
			"chat_id":     chatID,
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
		if query == "" {
			return errorResult(fmt.Errorf("query is required")), nil
		}
		chatID := argString(args, "chat_id")
		since := int64(argInt(args, "since", 0))
		until := int64(argInt(args, "until", 0))
		limit := argInt(args, "limit", 50)
		if limit > 200 {
			limit = 200
		}

		// The store's SearchMessages performs the FTS5 match; the optional
		// chat_id / time-window filters are applied here since the schema's
		// search path returns all matches newest-first.
		all, err := store.SearchMessages(ctx, query)
		if err != nil {
			return errorResult(fmt.Errorf("search messages: %w", err)), nil
		}

		filtered := make([]MessageRow, 0, len(all))
		for _, m := range all {
			if chatID != "" && m.ChatID != chatID {
				continue
			}
			if since > 0 && m.Timestamp < since {
				continue
			}
			if until > 0 && m.Timestamp > until {
				continue
			}
			filtered = append(filtered, m)
			if len(filtered) >= limit {
				break
			}
		}
		if filtered == nil {
			filtered = []MessageRow{}
		}
		return jsonResult(map[string]any{
			"messages": filtered,
			"total":    len(filtered),
			"query":    query,
			"chat_id":  chatID,
			"since":    since,
			"until":    until,
			"limit":    limit,
		})
	}
}

// recordOutbound mirrors a successfully sent message into the local store so
// the UI and read-side tools see it immediately instead of waiting for the
// bot's own-message update to arrive via long polling (which the ingester
// then ignores thanks to INSERT OR IGNORE on the same (chat_id, id) key).
// Best-effort: a store write failure never fails a successful send.
func recordOutbound(ctx context.Context, store *Store, chatID string, id int64, text string, ts time.Time) {
	if store == nil {
		return
	}
	row := MessageRow{
		ID:         strconv.FormatInt(id, 10),
		ChatID:     chatID,
		SenderName: "me",
		Text:       text,
		Timestamp:  ts.Unix(),
		FromMe:     true,
	}
	if err := store.InsertMessage(ctx, row); err != nil {
		stderr("send: mirror outbound message: %s", err)
	}
	if err := store.UpsertChat(ctx, chatID, kindForChat(chatID), "", text, ts.Unix()); err != nil {
		stderr("send: mirror outbound chat: %s", err)
	}
}

// kindForChat guesses a chat type label for an outbound send. Telegram does
// not echo the chat type back in the send result, so we keep any existing
// row's type ("") — UpsertChat only overwrites type with a non-empty value,
// which never wipes a previously ingested dm/group/channel classification.
func kindForChat(chatID string) string {
	if strings.HasPrefix(chatID, "-100") {
		return "channel" // supergroups & channels share the -100 prefix
	}
	if strings.HasPrefix(chatID, "-") {
		return "group"
	}
	return "" // dm or unknown — "" preserves the stored type on upsert
}

func handleSendMessage(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatID := argString(args, "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}
		text := argString(args, "text")
		if text == "" {
			return errorResult(fmt.Errorf("text is required")), nil
		}
		replyTo := int64(argInt(args, "reply_to_message_id", 0))
		parseMode := argString(args, "parse_mode")
		if parseMode == "" {
			parseMode = "HTML"
		}
		disableNotification := argBool(args, "disable_notification")

		chunks := chunkText(text, telegramTextCap)
		var first SendResult
		for i, chunk := range chunks {
			reply := replyTo
			if i > 0 {
				reply = 0 // quote attaches to the first chunk only
			}
			res, err := cli.SendText(ctx, chatID, chunk, reply, parseMode, disableNotification)
			if err != nil {
				if i == 0 {
					return errorResult(fmt.Errorf("send message: %w", err)), nil
				}
				return errorResult(fmt.Errorf("send message (chunk %d/%d): %w", i+1, len(chunks), err)), nil
			}
			if i == 0 {
				first = res
			}
		}

		// Mirror the full text under the first chunk's id so the UI shows the
		// sent message instantly and FTS indexes the whole payload.
		recordOutbound(ctx, store, chatID, first.MessageID, text, first.Timestamp)
		_ = store.ResetUnread(ctx, chatID)

		return jsonResult(map[string]any{
			"message_id": first.MessageID,
			"timestamp":  first.Timestamp.Unix(),
			"chat_id":    chatID,
			"chunks":     len(chunks),
		})
	}
}

func handleSendMedia(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatID := argString(args, "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}
		filePath := argString(args, "file_path")
		if filePath == "" {
			return errorResult(fmt.Errorf("file_path is required")), nil
		}
		kind := argString(args, "kind")
		caption := argString(args, "caption")
		replyTo := int64(argInt(args, "reply_to_message_id", 0))

		info, err := os.Stat(filePath)
		if err != nil {
			return errorResult(fmt.Errorf("stat file %s: %w", filePath, err)), nil
		}
		if info.Size() > sendMediaMaxBytes {
			return errorResult(fmt.Errorf("file is %d bytes (>50 MB); cloud Bot API upload cap is 50 MB — run a local Bot API server to raise the limit", info.Size())), nil
		}

		res, err := cli.SendMedia(ctx, chatID, filePath, kind, caption, replyTo)
		if err != nil {
			return errorResult(fmt.Errorf("send media: %w", err)), nil
		}

		// Mirror the outbound media as a textual row so it appears in the
		// chat thread immediately; capture the caption (or a filename label)
		// so the thread stays readable even when Telegram has no caption.
		label := caption
		if label == "" {
			label = "📎 " + filepath.Base(filePath)
		}
		recordOutbound(ctx, store, chatID, res.MessageID, label, res.Timestamp)
		_ = store.ResetUnread(ctx, chatID)

		return jsonResult(map[string]any{
			"message_id": res.MessageID,
			"timestamp":  res.Timestamp.Unix(),
			"chat_id":    chatID,
			"kind":       kind,
			"file_path":  filePath,
			"size":       info.Size(),
		})
	}
}

func handleSendInlineButtons(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatID := argString(args, "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}
		text := argString(args, "text")
		if text == "" {
			return errorResult(fmt.Errorf("text is required")), nil
		}
		buttonsRaw := argString(args, "buttons")
		if buttonsRaw == "" {
			return errorResult(fmt.Errorf("buttons is required")), nil
		}
		replyTo := int64(argInt(args, "reply_to_message_id", 0))

		var buttons [][]InlineButton
		if err := json.Unmarshal([]byte(buttonsRaw), &buttons); err != nil {
			return errorResult(fmt.Errorf("parse buttons JSON (expected array of rows of {label,callback_data}): %w", err)), nil
		}
		if len(buttons) == 0 {
			return errorResult(fmt.Errorf("buttons must contain at least one row")), nil
		}
		for ri, row := range buttons {
			if len(row) == 0 {
				return errorResult(fmt.Errorf("row %d is empty", ri)), nil
			}
			for bi, b := range row {
				if b.Label == "" {
					return errorResult(fmt.Errorf("button %d in row %d has empty label", bi, ri)), nil
				}
				if b.CallbackData == "" {
					return errorResult(fmt.Errorf("button %d in row %d has empty callback_data", bi, ri)), nil
				}
				if len(b.CallbackData) > 64 {
					return errorResult(fmt.Errorf("button %d in row %d callback_data exceeds 64 bytes", bi, ri)), nil
				}
			}
		}

		res, err := cli.SendInlineButtons(ctx, chatID, text, buttons, replyTo)
		if err != nil {
			return errorResult(fmt.Errorf("send inline buttons: %w", err)), nil
		}

		recordOutbound(ctx, store, chatID, res.MessageID, text, res.Timestamp)
		_ = store.ResetUnread(ctx, chatID)

		return jsonResult(map[string]any{
			"message_id": res.MessageID,
			"timestamp":  res.Timestamp.Unix(),
			"chat_id":    chatID,
		})
	}
}

func handleEditMessage(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatID := argString(args, "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}
		messageID := int64(argInt(args, "message_id", 0))
		if messageID <= 0 {
			return errorResult(fmt.Errorf("message_id is required")), nil
		}
		text := argString(args, "text")
		if text == "" {
			return errorResult(fmt.Errorf("text is required")), nil
		}
		if len([]rune(text)) > telegramTextCap {
			return errorResult(fmt.Errorf("text exceeds %d code points", telegramTextCap)), nil
		}
		parseMode := argString(args, "parse_mode")
		if parseMode == "" {
			parseMode = "HTML"
		}

		if err := cli.EditMessage(ctx, chatID, strconv.FormatInt(messageID, 10), text, parseMode); err != nil {
			return errorResult(fmt.Errorf("edit message: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "ok", "chat_id": chatID, "message_id": messageID})
	}
}

func handleDeleteMessage(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatID := argString(args, "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}
		messageID := int64(argInt(args, "message_id", 0))
		if messageID <= 0 {
			return errorResult(fmt.Errorf("message_id is required")), nil
		}

		if err := cli.DeleteMessage(ctx, chatID, strconv.FormatInt(messageID, 10)); err != nil {
			return errorResult(fmt.Errorf("delete message: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "ok", "chat_id": chatID, "message_id": messageID})
	}
}

func handleAnswerCallback(cli Client, store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		callbackQueryID := argString(args, "callback_query_id")
		if callbackQueryID == "" {
			return errorResult(fmt.Errorf("callback_query_id is required")), nil
		}
		text := argString(args, "text")
		showAlert := argBool(args, "show_alert")

		// Answer first so the button stops loading even if the local resolve
		// below fails — the user already got their acknowledgement.
		if err := cli.AnswerCallback(ctx, callbackQueryID, text, showAlert); err != nil {
			return errorResult(fmt.Errorf("answer callback: %w", err)), nil
		}

		// Resolve the matching pending approval locally: derive the outcome
		// from the notification text so approve/deny verbs map to statuses.
		// This mirrors FakeClient.AnswerCallback so mock and real modes stay
		// consistent, and keeps list_pending_approvals honest after the
		// decision. A missing approval row is not an error (e.g. callback
		// answered for a message whose press predates the store or was
		// already resolved).
		status := "approved"
		hay := strings.ToLower(text)
		for _, neg := range []string{"deny", "denied", "reject", "cancel", "no "} {
			if strings.Contains(hay, neg) {
				status = "denied"
				break
			}
		}
		if store != nil {
			_ = store.UpdateApprovalStatus(ctx, callbackQueryID, status)
		}

		return jsonResult(map[string]any{
			"status":            "ok",
			"callback_query_id": callbackQueryID,
			"resolution":        status,
		})
	}
}

func handleSendChatAction(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		chatID := argString(args, "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}
		action := argString(args, "action")
		if action == "" {
			return errorResult(fmt.Errorf("action is required")), nil
		}

		if err := cli.SendChatAction(ctx, chatID, action); err != nil {
			return errorResult(fmt.Errorf("send chat action: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "ok", "chat_id": chatID, "action": action})
	}
}

func handleListPendingApprovals(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		approvals, err := store.ListPendingApprovals(ctx)
		if err != nil {
			return errorResult(fmt.Errorf("list pending approvals: %w", err)), nil
		}
		if approvals == nil {
			approvals = []ApprovalRow{}
		}
		return jsonResult(map[string]any{
			"approvals": approvals,
			"total":     len(approvals),
		})
	}
}

func handleAddToAllowlist(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID := strings.TrimSpace(argString(req.GetArguments(), "user_id"))
		if userID == "" {
			return errorResult(fmt.Errorf("user_id is required")), nil
		}
		if err := store.AddToAllowlist(ctx, userID); err != nil {
			return errorResult(fmt.Errorf("add to allowlist: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "ok", "user_id": userID})
	}
}

func handleRemoveFromAllowlist(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID := strings.TrimSpace(argString(req.GetArguments(), "user_id"))
		if userID == "" {
			return errorResult(fmt.Errorf("user_id is required")), nil
		}
		if err := store.RemoveFromAllowlist(ctx, userID); err != nil {
			return errorResult(fmt.Errorf("remove from allowlist: %w", err)), nil
		}
		return jsonResult(map[string]any{"status": "ok", "user_id": userID})
	}
}

func handleGetChatHistory(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		chatID := argString(req.GetArguments(), "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}

		messages, err := store.GetChatHistory(ctx, chatID)
		if err != nil {
			return errorResult(fmt.Errorf("get chat history: %w", err)), nil
		}
		if messages == nil {
			messages = []MessageRow{}
		}

		// Include a human-readable preview alongside the raw rows so the LLM
		// can reason about the thread without re-formatting each row.
		preview := make([]string, 0, len(messages))
		for _, m := range messages {
			preview = append(preview, formatMessage(m))
		}
		return jsonResult(map[string]any{
			"messages": messages,
			"preview":  preview,
			"total":    len(messages),
			"chat_id":  chatID,
		})
	}
}

func handleRequestSync(cli Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		chatID := argString(req.GetArguments(), "chat_id")
		if err := validateChatID(chatID); err != nil {
			return errorResult(err), nil
		}

		// The Telegram Bot API has no on-demand history backfill. RequestSync
		// is a no-op that succeeds without a server call; a future
		// local-Bot-API/MTProto path can hook here without changing the tool
		// contract.
		if err := cli.RequestSync(ctx, chatID); err != nil {
			return errorResult(fmt.Errorf("request sync: %w", err)), nil
		}
		return jsonResult(map[string]any{
			"status":  "requested",
			"chat_id": chatID,
			"hint":    "Telegram Bot API cannot backfill messages that arrived before polling started. Sync only covers updates observed from now on.",
		})
	}
}

func handleSetPrivacyMode(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		enabled, ok := req.GetArguments()["enabled"].(bool)
		if !ok {
			return errorResult(fmt.Errorf("enabled (boolean) is required")), nil
		}
		value := "0"
		if enabled {
			value = "1"
		}
		if err := store.SetMeta(ctx, privacyModeMetaKey, value); err != nil {
			return errorResult(fmt.Errorf("set privacy mode: %w", err)), nil
		}
		return jsonResult(map[string]any{
			"status":  "ok",
			"enabled": enabled,
			"hint":    boolHint(enabled),
		})
	}
}

// boolHint returns a human-readable description of the privacy-mode state.
func boolHint(enabled bool) string {
	if enabled {
		return "Privacy mode ON: only allowlisted users may interact; unknown senders are dropped."
	}
	return "Privacy mode OFF: the bot accepts messages from anyone."
}

// --- Store extensions (package-local query helpers) -----------------------
//
// These two read methods extend Store with cursor/history queries that the
// sibling store.go does not provide. They live in this file (same package) so
// they can access Store's unexported db field without modifying store.go. The
// scan shape mirrors Store.GetMessages in store.go.

// GetMessagesCursor returns messages for a chat, newest first, with
// cursor-based pagination: when before > 0, only messages older than `before`
// (a Unix-second timestamp) are returned. before <= 0 yields the newest page.
func (s *Store) GetMessagesCursor(ctx context.Context, chatID string, before int64, limit int) ([]MessageRow, error) {
	var (
		q    string
		args []any
	)
	if before > 0 {
		q = `SELECT id, chat_id, sender_name, text, timestamp, from_me, edited_at
		     FROM messages WHERE chat_id = ? AND timestamp < ?
		     ORDER BY timestamp DESC LIMIT ?`
		args = []any{chatID, before, limit}
	} else {
		q = `SELECT id, chat_id, sender_name, text, timestamp, from_me, edited_at
		     FROM messages WHERE chat_id = ?
		     ORDER BY timestamp DESC LIMIT ?`
		args = []any{chatID, limit}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var fromMe int
		var text sql.NullString
		if err := rows.Scan(&m.ID, &m.ChatID, &m.SenderName, &text, &m.Timestamp, &fromMe, &m.EditedAt); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.FromMe = fromMe != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetChatHistory returns the full local message history for a chat, oldest
// first. Intended for small chats or exports; callers that need pagination
// should use get_messages instead.
func (s *Store) GetChatHistory(ctx context.Context, chatID string) ([]MessageRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, sender_name, text, timestamp, from_me, edited_at
		 FROM messages WHERE chat_id = ?
		 ORDER BY timestamp ASC`,
		chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var fromMe int
		var text sql.NullString
		if err := rows.Scan(&m.ID, &m.ChatID, &m.SenderName, &text, &m.Timestamp, &fromMe, &m.EditedAt); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.FromMe = fromMe != 0
		out = append(out, m)
	}
	return out, rows.Err()
}
