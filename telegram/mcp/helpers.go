package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mymmrac/telego"
)

// --- environment / diagnostics --------------------------------------------

// isVerbose reports whether TELEGRAM_VERBOSE is set to a truthy value.
// Verbose mode enables extra stderr diagnostics from the client and ingester.
func isVerbose() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TELEGRAM_VERBOSE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// parseTime formats a Unix-second timestamp as a short relative time string
// (e.g. "just now", "5m ago", "3h ago", "2d ago"). Returns "—" for zero/neg.
func parseTime(ts int64) string {
	if ts <= 0 {
		return "—"
	}
	t := time.Unix(ts, 0)
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.UTC().Format("2006-01-02")
	}
}

// validateChatID checks that a chat_id string is one of the forms accepted by
// the Telegram Bot API: an int64-as-string (optionally "-100"-prefixed for
// supergroups/channels) or a "@username" public handle. It does NOT verify the
// chat exists — only that the shape is plausible, so the client gets a clean
// error instead of a cryptic Bot API response.
func validateChatID(chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return errors.New("chat_id is required")
	}
	// "@username" public handle — Telegram allows 5-32 char usernames.
	if strings.HasPrefix(chatID, "@") {
		name := chatID[1:]
		if len(name) < 5 || len(name) > 32 {
			return fmt.Errorf("invalid @username %q: must be 5-32 chars", chatID)
		}
		return nil
	}
	// Numeric int64-as-string. A leading '-' is valid (supergroups/channels
	// are negative, "-100"-prefixed). Reject anything with other punctuation.
	s := chatID
	if strings.HasPrefix(s, "-") {
		s = s[1:]
	}
	if s == "" {
		return errors.New("chat_id has no digits")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return fmt.Errorf("chat_id %q must be numeric or @username", chatID)
		}
	}
	// int64 range check. Supergroup ids like -1001234567890 fit in int64.
	n, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("chat_id %q out of int64 range: %w", chatID, err)
	}
	// Telegram chat ids are non-zero; user/bot ids are positive, group ids
	// negative, channels/supergroups "-100"-prefixed negative.
	if n == 0 {
		return errors.New("chat_id must not be 0")
	}
	return nil
}

// extractChatIDFromMessage pulls the chat_id (as int64-as-string) out of a
// telego update payload. It handles the update types this plugin processes:
// *telego.Update (dispatching to its Message/EditedMessage/ChannelPost/
// CallbackQuery fields), *telego.Message, and *telego.CallbackQuery. For
// callback queries the chat is read via the MaybeInaccessibleMessage.GetChat()
// accessor, which works for both accessible and inaccessible messages.
// Returns an error for unknown shapes so callers can log and drop the update
// rather than panic.
func extractChatIDFromMessage(msg any) (string, error) {
	switch v := msg.(type) {
	case *telego.Update:
		if v == nil {
			return "", errors.New("nil update")
		}
		switch {
		case v.Message != nil:
			return strconv.FormatInt(v.Message.Chat.ID, 10), nil
		case v.EditedMessage != nil:
			return strconv.FormatInt(v.EditedMessage.Chat.ID, 10), nil
		case v.ChannelPost != nil:
			return strconv.FormatInt(v.ChannelPost.Chat.ID, 10), nil
		case v.EditedChannelPost != nil:
			return strconv.FormatInt(v.EditedChannelPost.Chat.ID, 10), nil
		case v.CallbackQuery != nil:
			return extractChatIDFromMessage(v.CallbackQuery)
		default:
			return "", errors.New("update carries no tracked payload")
		}
	case *telego.Message:
		if v == nil {
			return "", errors.New("nil message")
		}
		return strconv.FormatInt(v.Chat.ID, 10), nil
	case telego.Message:
		return strconv.FormatInt(v.Chat.ID, 10), nil
	case *telego.CallbackQuery:
		if v == nil {
			return "", errors.New("nil callback query")
		}
		// CallbackQuery.Message is a MaybeInaccessibleMessage interface; both
		// *Message and *InaccessibleMessage implement GetChat(). Mirror the
		// nil-guard used by tgevent.go's normalizeCallback.
		if v.Message == nil {
			return "", errors.New("callback query has no message/chat")
		}
		return strconv.FormatInt(v.Message.GetChat().ID, 10), nil
	default:
		return "", fmt.Errorf("extractChatIDFromMessage: unsupported type %T", msg)
	}
}

// --- shared MCP tool-result helpers ---------------------------------------
//
// These mirror the WhatsApp plugin (whatsapp/mcp/helpers.go) so tool handlers
// can return structured JSON results and sanitized errors uniformly. stderr is
// defined in ingest.go (shared by the whole package).

// jsonResult wraps data as a JSON MCP tool result with both text content
// (mcp.NewTextContent) and structured content.
func jsonResult(data any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(raw)),
		},
		StructuredContent: data,
	}, nil
}

// errorResult wraps an error as an MCP error result with a sanitized message.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent(safeErrorMessage(err)),
		},
	}
}

// safeErrorMessage sanitizes an error for output: strips control chars,
// truncates to 1000 chars, and returns a generic message for nil/empty.
func safeErrorMessage(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := err.Error()
	if msg == "" {
		return "unknown error"
	}
	out := make([]rune, 0, len(msg))
	for _, r := range msg {
		if r < 0x20 || r == 0x7f {
			out = append(out, ' ')
		} else {
			out = append(out, r)
		}
	}
	msg = string(out)
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	return msg
}

// argString extracts a string argument from the tool call args map.
func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// argInt extracts an int argument from the tool call args map.
func argInt(args map[string]any, key string, def int) int {
	switch n := args[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return def
	}
}

// argBool extracts a bool argument from the tool call args map.
func argBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}
