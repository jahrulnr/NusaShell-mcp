package main

import (
	"strconv"

	"github.com/mymmrac/telego"
)

// EventType is the normalized kind of a Telegram Bot API update. The ingester
// switches on this; telego types never cross the boundary into ingest.go.
type EventType string

const (
	// EventMessage is a new incoming message (private, group, or channel post
	// delivered as Update.Message).
	EventMessage EventType = "message"
	// EventCallbackQuery is an inline-keyboard button press.
	EventCallbackQuery EventType = "callback_query"
	// EventEditedMessage is a new version of a previously known message.
	EventEditedMessage EventType = "edited_message"
	// EventChannelPost is a new incoming channel post (Update.ChannelPost).
	EventChannelPost EventType = "channel_post"
)

// TelegramEvent is the normalized representation of a single Telegram update,
// produced by NormalizeUpdate and consumed by the Ingester. The producer
// (tgclient/tgbot) translates telego.Update into this; the consumer (ingest)
// never imports telego, so the seam is this struct.
//
// Data carries the payload fields as typed values under well-known keys (see
// the accessor methods below). UpdateID is the Telegram update_id used as the
// incremental ingestion watermark.
type TelegramEvent struct {
	Type      EventType
	Data      map[string]any
	Timestamp int64
}

// Well-known Data keys.
const (
	kChatID         = "chat_id"
	kChatType       = "chat_type"
	kChatName       = "chat_name"
	kMessageID      = "message_id"
	kText           = "text"
	kSenderID       = "sender_id"
	kSenderUsername = "sender_username"
	kSenderName     = "sender_name"
	kFromMe         = "from_me"
	kEditedAt       = "edited_at"
	kUpdateID       = "update_id"
	kCallbackID     = "callback_id"
	kCallbackData   = "callback_data"
)

// --- Accessors (read from Data with safe type assertions) ---

func (e TelegramEvent) str(key string) string {
	if v, ok := e.Data[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (e TelegramEvent) int64v(key string) int64 {
	if v, ok := e.Data[key]; ok {
		switch n := v.(type) {
		case int64:
			return n
		case int:
			return int64(n)
		}
	}
	return 0
}

func (e TelegramEvent) boolv(key string) bool {
	if v, ok := e.Data[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// ChatID returns the chat identifier as a string (int64-as-string, matching
// the WhatsApp chat_jid convention for precision in JSON/JS/LLM params).
func (e TelegramEvent) ChatID() string { return e.str(kChatID) }

// ChatType returns the normalized chat type: "dm", "group", or "channel".
func (e TelegramEvent) ChatType() string { return e.str(kChatType) }

// ChatName returns the display name of the chat.
func (e TelegramEvent) ChatName() string { return e.str(kChatName) }

// MessageID returns the message identifier as a string.
func (e TelegramEvent) MessageID() string { return e.str(kMessageID) }

// Text returns the message text (or caption for media messages).
func (e TelegramEvent) Text() string { return e.str(kText) }

// SenderID returns the sender's user id as a string (empty for channel posts).
func (e TelegramEvent) SenderID() string { return e.str(kSenderID) }

// SenderUsername returns the sender's @username without the '@' (empty when
// the sender has no username or it is unknown).
func (e TelegramEvent) SenderUsername() string { return e.str(kSenderUsername) }

// SenderName returns a human-readable sender label.
func (e TelegramEvent) SenderName() string { return e.str(kSenderName) }

// FromMe reports whether the message was sent by this bot.
func (e TelegramEvent) FromMe() bool { return e.boolv(kFromMe) }

// EditedAt returns the edit timestamp (unix seconds), or 0 if not an edit.
func (e TelegramEvent) EditedAt() int64 { return e.int64v(kEditedAt) }

// UpdateID returns the Telegram update_id used as the ingestion watermark.
func (e TelegramEvent) UpdateID() int { return int(e.int64v(kUpdateID)) }

// CallbackID returns the callback query id (for answer_callback).
func (e TelegramEvent) CallbackID() string { return e.str(kCallbackID) }

// CallbackData returns the data payload of an inline button press.
func (e TelegramEvent) CallbackData() string { return e.str(kCallbackData) }

// NormalizeUpdate translates a telego.Update into a normalized TelegramEvent.
// botID is this bot's own user id (from getMe); pass 0 if unknown, in which
// case FromMe falls back to the IsBot heuristic. The boolean is false when the
// update carries no payload this plugin tracks (inline queries, polls, etc.),
// so the caller can drop it without dispatching to the ingester.
func NormalizeUpdate(botID int64, u telego.Update) (TelegramEvent, bool) {
	switch {
	case u.Message != nil:
		return normalizeMessage(EventMessage, botID, u.Message, 0, u.UpdateID), true
	case u.EditedMessage != nil:
		return normalizeMessage(EventEditedMessage, botID, u.EditedMessage, u.EditedMessage.EditDate, u.UpdateID), true
	case u.ChannelPost != nil:
		return normalizeMessage(EventChannelPost, botID, u.ChannelPost, 0, u.UpdateID), true
	case u.EditedChannelPost != nil:
		// An edited channel post is still a channel post; carry the edit time.
		return normalizeMessage(EventChannelPost, botID, u.EditedChannelPost, u.EditedChannelPost.EditDate, u.UpdateID), true
	case u.CallbackQuery != nil:
		return normalizeCallback(botID, u.CallbackQuery, u.UpdateID), true
	}
	return TelegramEvent{}, false
}

// normalizeMessage builds a TelegramEvent from a telego.Message. editedAt is
// non-zero for edited messages/channel posts.
func normalizeMessage(t EventType, botID int64, m *telego.Message, editedAt int64, updateID int) TelegramEvent {
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	chatType := normalizeChatType(m.Chat.Type)
	chatName := chatDisplayName(m.Chat)
	text := m.Text
	if text == "" {
		text = m.Caption
	}

	senderID, senderUsername, senderName, fromMe := senderInfo(botID, m)

	data := map[string]any{
		kChatID:         chatID,
		kChatType:       chatType,
		kChatName:       chatName,
		kMessageID:      strconv.Itoa(m.MessageID),
		kText:           text,
		kSenderID:       senderID,
		kSenderUsername: senderUsername,
		kSenderName:     senderName,
		kFromMe:         fromMe,
		kUpdateID:       updateID,
	}
	if editedAt > 0 {
		data[kEditedAt] = editedAt
	}

	return TelegramEvent{
		Type:      t,
		Data:      data,
		Timestamp: m.Date,
	}
}

// normalizeCallback builds a TelegramEvent from a telego.CallbackQuery.
func normalizeCallback(botID int64, q *telego.CallbackQuery, updateID int) TelegramEvent {
	chatID, chatName, chatType, messageID, date := "", "", "", "", int64(0)
	if q.Message != nil {
		ch := q.Message.GetChat()
		chatID = strconv.FormatInt(ch.ID, 10)
		chatType = normalizeChatType(ch.Type)
		chatName = chatDisplayName(ch)
		messageID = strconv.Itoa(q.Message.GetMessageID())
		date = q.Message.GetDate()
	}

	fromMe := botID != 0 && q.From.ID == botID
	senderID := strconv.FormatInt(q.From.ID, 10)
	senderUsername := q.From.Username
	senderName := userDisplayName(q.From)

	data := map[string]any{
		kChatID:         chatID,
		kChatType:       chatType,
		kChatName:       chatName,
		kMessageID:      messageID,
		kText:           q.Data,
		kSenderID:       senderID,
		kSenderUsername: senderUsername,
		kSenderName:     senderName,
		kFromMe:         fromMe,
		kUpdateID:       updateID,
		kCallbackID:     q.ID,
		kCallbackData:   q.Data,
	}

	return TelegramEvent{
		Type:      EventCallbackQuery,
		Data:      data,
		Timestamp: date,
	}
}

// senderInfo extracts the sender id, username, display name, and fromMe flag
// for a message. Channel posts have no From (the channel is the sender); we
// use SenderChat/Chat title as the name and treat fromMe as false.
func senderInfo(botID int64, m *telego.Message) (id, username, name string, fromMe bool) {
	if m.From != nil {
		id = strconv.FormatInt(m.From.ID, 10)
		username = m.From.Username
		name = userDisplayName(*m.From)
		if botID != 0 {
			fromMe = m.From.ID == botID
		} else {
			fromMe = m.From.IsBot
		}
		return
	}
	if m.SenderChat != nil {
		name = chatDisplayName(*m.SenderChat)
		id = strconv.FormatInt(m.SenderChat.ID, 10)
		if m.SenderChat.Username != "" {
			username = m.SenderChat.Username
		}
	}
	return
}

// normalizeChatType maps Telegram chat types to the plugin's internal kinds.
func normalizeChatType(t string) string {
	switch t {
	case "private":
		return "dm"
	case "group", "supergroup":
		return "group"
	case "channel":
		return "channel"
	default:
		return t
	}
}

// chatDisplayName returns the best available label for a chat.
func chatDisplayName(c telego.Chat) string {
	switch c.Type {
	case "private":
		if c.FirstName != "" || c.LastName != "" {
			return joinNames(c.FirstName, c.LastName)
		}
	}
	if c.Title != "" {
		return c.Title
	}
	if c.Username != "" {
		return "@" + c.Username
	}
	return joinNames(c.FirstName, c.LastName)
}

// userDisplayName returns the best available label for a user.
func userDisplayName(u telego.User) string {
	if u.FirstName != "" || u.LastName != "" {
		return joinNames(u.FirstName, u.LastName)
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return strconv.FormatInt(u.ID, 10)
}

// joinNames trims trailing whitespace from "first last".
func joinNames(first, last string) string {
	if first == "" {
		return last
	}
	if last == "" {
		return first
	}
	return first + " " + last
}
