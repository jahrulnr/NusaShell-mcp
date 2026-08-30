// tgclient.go defines the Telegram Client contract: the PairState, SendResult,
// and InlineButton types plus the Client interface. The production
// implementation lives in tgbot.go (telego-backed); the FakeClient in this
// file is used only when MOCK_ENABLED=1 (explicit mock mode).
//
// Connect/Events/Disconnect are concrete methods on *BotClient and *FakeClient
// (used by main.go for startup/shutdown and by the ingester) and are
// intentionally NOT part of the Client interface, which only covers the
// tool-facing surface consumed by tools.go. The TelegramRuntime interface
// bundles Client with those lifecycle methods so main.go can hold either
// implementation behind a single typed variable.
package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotConnected is returned by Client methods when the bot is not connected.
// Tool handlers detect it via errors.Is to flag retryable errors so the LLM
// knows the call can be retried.
var ErrNotConnected = errors.New("telegram bot not connected")

// ErrNotPaired is returned when no bot token has been stored.
var ErrNotPaired = errors.New("telegram bot not paired — run login first")

// PairState describes the current linking state for the status/login tools.
type PairState struct {
	Paired        bool   // true if a bot token is stored
	Connected     bool   // true if long polling is active
	BotName       string // bot display name from getMe
	BotID         string // bot user id from getMe (int64 as string)
	AwaitingToken bool   // true if a pairing flow is waiting for a token
}

// SendResult is what every successful send returns.
type SendResult struct {
	MessageID int64
	Timestamp time.Time
}

// InlineButton is a single callback button rendered in an inline keyboard.
type InlineButton struct {
	Label        string
	CallbackData string
}

// Client is the only Telegram-touching interface in this plugin. The
// production implementation wraps github.com/mymmrac/telego; the FakeClient in
// this file substitutes mock data. This contract must stay in sync with the
// *BotClient methods in tgbot.go, the *FakeClient methods in this file, and
// the tool handlers in tools.go.
type Client interface {
	// State reports the current pairing/connection state.
	State() PairState

	// Login validates a Bot API token via getMe, stores it (mode 0600), and
	// starts long polling. Returns the resulting state.
	Login(ctx context.Context, token string) (PairState, error)

	// Logout stops polling and clears the stored token.
	Logout(ctx context.Context) error

	// SendText sends a text message. replyTo is a message id to quote (0 for
	// none); parseMode is "" / "MarkdownV2" / "HTML"; disableNotification sends
	// silently when true.
	SendText(ctx context.Context, chatID, text string, replyTo int64, parseMode string, disableNotification bool) (SendResult, error)

	// SendMedia uploads and sends a file. Kind is "photo", "video", "audio",
	// or "document" (default). replyTo is a message id to quote (0 for none).
	SendMedia(ctx context.Context, chatID, filePath, kind, caption string, replyTo int64) (SendResult, error)

	// SendInlineButtons sends a text message with an inline keyboard built
	// from buttons (a slice of rows). replyTo is a message id to quote (0 none).
	SendInlineButtons(ctx context.Context, chatID, text string, buttons [][]InlineButton, replyTo int64) (SendResult, error)

	// EditMessage edits the text of an existing message. messageID is the
	// numeric message id as a string; parseMode is "" / "MarkdownV2" / "HTML".
	EditMessage(ctx context.Context, chatID, messageID, text, parseMode string) error

	// DeleteMessage deletes a message from a chat. messageID is the numeric
	// message id as a string.
	DeleteMessage(ctx context.Context, chatID, messageID string) error

	// AnswerCallback answers a callback query (e.g. approval/deny). showAlert
	// pops an alert dialog instead of a toast.
	AnswerCallback(ctx context.Context, callbackQueryID, text string, showAlert bool) error

	// SendChatAction sends a typing/uploading indicator.
	SendChatAction(ctx context.Context, chatID, action string) error

	// RequestSync is a no-op for the cloud Bot API (it cannot backfill
	// pre-polling history); forwarded so a future local-Bot-API path can hook
	// here without changing the tool contract.
	RequestSync(ctx context.Context, chatID string) error
}

// TelegramRuntime bundles the tool-facing Client contract with the lifecycle
// methods main.go and the ingester rely on. Both *BotClient and *FakeClient
// satisfy it, so main.go can hold either behind one typed variable and still
// call Connect/Events/Disconnect directly while passing the value to
// registerTools (which takes the narrower Client interface).
type TelegramRuntime interface {
	Client
	Connect(ctx context.Context) error
	Events(ctx context.Context) <-chan any
	Disconnect()
}

// --- FakeClient ------------------------------------------------------------
//
// FakeClient is a stand-in Client implementation used by main.go when no bot
// token is stored yet (Connect returns ErrNotPaired). It serves mock chats,
// messages, and approvals so the NusaShell UI is explorable before pairing.
//
// It holds its mock data in memory and is self-contained, but when a *Store is
// supplied to NewFakeClient it also seeds that store with the same mock data
// (best-effort) so the read-side tools in tools.go — which query the Store
// directly, not the Client — return the mock rows. Send methods append to both
// the in-memory mock state and the Store so a sent message is immediately
// visible via get_messages/get_chat_history.

// fakeBotName/fakeBotID are the mock identity reported after Login.
const (
	fakeBotName = "@mock_bot"
	fakeBotID   = "123456789"
)

// fakeTokenMetaKey is the meta key under which FakeClient persists the token
// when a Store is available, mirroring how BotClient stores it on disk.
const fakeTokenMetaKey = "telegram_token"

// FakeClient is the mock Client implementation used when MOCK_ENABLED=1
// (explicit mock mode). It is NOT used automatically when the bot is unpaired —
// when MOCK_ENABLED=0 and the bot is not connected, BotClient methods return
// ErrNotConnected so the UI shows an honest "Not connected" state.
type FakeClient struct {
	mu        sync.RWMutex
	store     *Store // optional; when set, mock data is mirrored here
	state     PairState
	chats     []ChatRow
	messages  map[string][]MessageRow // chatID -> messages, oldest first
	approvals []ApprovalRow
	allowlist []string
	nextMsgID int64 // counter for fake message ids produced by sends
}

// NewFakeClient builds a FakeClient preloaded with mock chats, messages, and
// approvals. When store is non-nil the same mock data is seeded into it
// (best-effort) so the Store-backed read tools surface mock rows, and the
// token supplied to Login is persisted via store.SetMeta.
func NewFakeClient(store *Store) *FakeClient {
	chats, messages, approvals := mockData()
	c := &FakeClient{
		store:     store,
		chats:     chats,
		messages:  messages,
		approvals: approvals,
		nextMsgID: 1000,
	}
	c.seedStore()
	return c
}

// seedStore mirrors the in-memory mock data into the Store (best-effort,
// errors ignored). Re-seeding is idempotent: messages use INSERT OR IGNORE,
// chats are upserted with stable values, and unread counts are reset before
// being re-applied so they don't accumulate across restarts.
func (c *FakeClient) seedStore() {
	if c.store == nil {
		return
	}
	ctx := context.Background()
	for _, ch := range c.chats {
		_ = c.store.UpsertChat(ctx, ch.ID, ch.Type, ch.Name, ch.LastMessage, ch.LastMessageAt)
		_ = c.store.ResetUnread(ctx, ch.ID)
		for i := 0; i < ch.UnreadCount; i++ {
			_ = c.store.IncrementUnread(ctx, ch.ID)
		}
	}
	for _, msgs := range c.messages {
		for _, m := range msgs {
			_ = c.store.InsertMessage(ctx, m)
		}
	}
	for _, a := range c.approvals {
		_ = c.store.UpsertApproval(ctx, a)
	}
}

// State reports the current (mock) pairing/connection state.
func (c *FakeClient) State() PairState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Login marks the fake bot as paired. It does not contact Telegram; the token
// is remembered in the mock state and, when a Store is present, persisted via
// store.SetMeta so a later real Connect could pick it up.
func (c *FakeClient) Login(ctx context.Context, token string) (PairState, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return PairState{}, errors.New("bot token is empty")
	}
	c.mu.Lock()
	if c.state.Paired {
		c.mu.Unlock()
		return PairState{}, errors.New("already paired — logout first")
	}
	c.state = PairState{
		Paired:    true,
		Connected: true,
		BotName:   fakeBotName,
		BotID:     fakeBotID,
	}
	c.mu.Unlock()
	if c.store != nil {
		_ = c.store.SetMeta(ctx, fakeTokenMetaKey, token)
	}
	return c.State(), nil
}

// Logout clears the mock paired state and drops any persisted token.
func (c *FakeClient) Logout(ctx context.Context) error {
	c.mu.Lock()
	c.state = PairState{}
	c.mu.Unlock()
	if c.store != nil {
		_ = c.store.SetMeta(ctx, fakeTokenMetaKey, "")
	}
	return nil
}

// SendText records a mock outgoing message and returns a fake SendResult.
func (c *FakeClient) SendText(ctx context.Context, chatID, text string, replyTo int64, parseMode string, disableNotification bool) (SendResult, error) {
	return c.recordSend(ctx, chatID, text), nil
}

// SendMedia records a mock outgoing media message and returns a fake SendResult.
func (c *FakeClient) SendMedia(ctx context.Context, chatID, filePath, kind, caption string, replyTo int64) (SendResult, error) {
	label := caption
	if label == "" {
		label = "[" + kind + "] " + filePath
	}
	return c.recordSend(ctx, chatID, label), nil
}

// SendInlineButtons records a mock outgoing message with buttons and returns a
// fake SendResult.
func (c *FakeClient) SendInlineButtons(ctx context.Context, chatID, text string, buttons [][]InlineButton, replyTo int64) (SendResult, error) {
	return c.recordSend(ctx, chatID, text), nil
}

// recordSend appends an outgoing (from_me) message to the mock state and the
// Store, returning a SendResult with a fresh mock message id.
func (c *FakeClient) recordSend(ctx context.Context, chatID, text string) SendResult {
	c.mu.Lock()
	c.nextMsgID++
	id := c.nextMsgID
	now := time.Now().Unix()
	row := MessageRow{
		ID:         strconv.FormatInt(id, 10),
		ChatID:     chatID,
		SenderName: fakeBotName,
		Text:       text,
		Timestamp:  now,
		FromMe:     true,
	}
	c.messages[chatID] = append(c.messages[chatID], row)
	c.mu.Unlock()
	if c.store != nil {
		_ = c.store.InsertMessage(ctx, row)
		_ = c.store.UpsertChat(ctx, chatID, chatTypeFor(chatID), chatNameFor(c.chats, chatID), text, now)
	}
	return SendResult{MessageID: id, Timestamp: time.Unix(now, 0)}
}

// EditMessage is a no-op in mock mode.
func (c *FakeClient) EditMessage(ctx context.Context, chatID, messageID, text, parseMode string) error {
	return nil
}

// DeleteMessage is a no-op in mock mode.
func (c *FakeClient) DeleteMessage(ctx context.Context, chatID, messageID string) error { return nil }

// AnswerCallback resolves the matching mock approval. The approval id is the
// callback query id; status is "denied" when the notification text or the
// approval payload suggests rejection, otherwise "approved".
func (c *FakeClient) AnswerCallback(ctx context.Context, callbackQueryID, text string, showAlert bool) error {
	status := "approved"
	hay := strings.ToLower(text)
	for _, neg := range []string{"deny", "denied", "no", "reject", "cancel"} {
		if strings.Contains(hay, neg) {
			status = "denied"
			break
		}
	}
	c.mu.Lock()
	for i := range c.approvals {
		if c.approvals[i].ID == callbackQueryID {
			c.approvals[i].Status = status
			break
		}
	}
	c.mu.Unlock()
	if c.store != nil {
		_ = c.store.UpdateApprovalStatus(ctx, callbackQueryID, status)
	}
	return nil
}

// SendChatAction is a no-op in mock mode.
func (c *FakeClient) SendChatAction(ctx context.Context, chatID, action string) error { return nil }

// RequestSync is a no-op in mock mode.
func (c *FakeClient) RequestSync(ctx context.Context, chatID string) error { return nil }

// Connect is a no-op for the fake client — it is always "ready" in mock mode.
func (c *FakeClient) Connect(ctx context.Context) error { return nil }

// Events returns a channel that is already closed, so the ingester drains it
// immediately and exits (there are no live events in mock mode).
func (c *FakeClient) Events(ctx context.Context) <-chan any {
	ch := make(chan any)
	close(ch)
	return ch
}

// Disconnect is a no-op in mock mode.
func (c *FakeClient) Disconnect() {}

// --- FakeClient read-side helpers (mock data accessors) -------------------
//
// These are concrete methods on *FakeClient, not part of the Client interface.
// They expose the in-memory mock state directly for tests or a future UI path
// that queries the client instead of the Store. The Store-backed tools in
// tools.go read the seeded Store, so these are not on the hot path today.

// ListChats returns the mock chats, most recent activity first.
func (c *FakeClient) ListChats() []ChatRow {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ChatRow, len(c.chats))
	copy(out, c.chats)
	sortChatsByActivity(out)
	return out
}

// GetMessages returns mock messages for a chat, newest first, with limit/offset.
func (c *FakeClient) GetMessages(chatID string, limit, offset int) []MessageRow {
	c.mu.RLock()
	defer c.mu.RUnlock()
	msgs := newestFirst(c.messages[chatID])
	if offset > 0 && offset >= len(msgs) {
		return []MessageRow{}
	}
	if offset > 0 {
		msgs = msgs[offset:]
	}
	if limit > 0 && limit < len(msgs) {
		msgs = msgs[:limit]
	}
	out := make([]MessageRow, len(msgs))
	copy(out, msgs)
	return out
}

// GetChat returns the mock chat matching chatID, or nil if none.
func (c *FakeClient) GetChat(chatID string) *ChatRow {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.chats {
		if c.chats[i].ID == chatID {
			ch := c.chats[i]
			return &ch
		}
	}
	return nil
}

// SearchMessages returns mock messages whose text contains the query (case
// insensitive), newest first. Returns an empty slice for no matches.
func (c *FakeClient) SearchMessages(query string) []MessageRow {
	q := strings.ToLower(query)
	var out []MessageRow
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, msgs := range c.messages {
		for _, m := range msgs {
			if q != "" && strings.Contains(strings.ToLower(m.Text), q) {
				out = append(out, m)
			}
		}
	}
	sortMessagesNewestFirst(out)
	return out
}

// ListPendingApprovals returns the mock approvals that are still pending.
func (c *FakeClient) ListPendingApprovals() []ApprovalRow {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ApprovalRow, 0, len(c.approvals))
	for _, a := range c.approvals {
		if a.Status == "pending" {
			out = append(out, a)
		}
	}
	return out
}

// AddToAllowlist adds a user/chat id to the mock allowlist and, when a Store is
// present, mirrors it there.
func (c *FakeClient) AddToAllowlist(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("user_id is empty")
	}
	c.mu.Lock()
	for _, id := range c.allowlist {
		if id == userID {
			c.mu.Unlock()
			return nil
		}
	}
	c.allowlist = append(c.allowlist, userID)
	c.mu.Unlock()
	if c.store != nil {
		_ = c.store.AddToAllowlist(ctx, userID)
	}
	return nil
}

// RemoveFromAllowlist removes a user/chat id from the mock allowlist and, when
// a Store is present, mirrors it there.
func (c *FakeClient) RemoveFromAllowlist(ctx context.Context, userID string) error {
	c.mu.Lock()
	for i, id := range c.allowlist {
		if id == userID {
			c.allowlist = append(c.allowlist[:i], c.allowlist[i+1:]...)
			break
		}
	}
	c.mu.Unlock()
	if c.store != nil {
		_ = c.store.RemoveFromAllowlist(ctx, userID)
	}
	return nil
}

// GetChatHistory returns all mock messages for a chat, oldest first.
func (c *FakeClient) GetChatHistory(ctx context.Context, chatID string) []MessageRow {
	c.mu.RLock()
	defer c.mu.RUnlock()
	msgs := c.messages[chatID]
	out := make([]MessageRow, len(msgs))
	copy(out, msgs)
	return out
}

// --- mock data --------------------------------------------------------------

// mockData builds the canned chats, per-chat messages, and pending approvals
// used to populate FakeClient and seed the Store. Timestamps are anchored to
// "now" so the UI always looks fresh.
func mockData() (chats []ChatRow, messages map[string][]MessageRow, approvals []ApprovalRow) {
	const (
		devTeam  = "-1001234567890" // group
		newsChan = "-1009876543210" // channel
		andiDM   = "111111111"      // dm
		budiDM   = "222222222"      // dm
	)
	now := time.Now().Unix()
	// step returns a timestamp `n` minutes before now.
	step := func(n int) int64 { return now - int64(n*60) }

	messages = map[string][]MessageRow{
		devTeam: {
			{ID: "1", ChatID: devTeam, SenderName: "Citra", Text: "Morning everyone! Standup in 10?", Timestamp: step(40), FromMe: false},
			{ID: "2", ChatID: devTeam, SenderName: "Dewi", Text: "I'll join in 5", Timestamp: step(38), FromMe: false},
			{ID: "3", ChatID: devTeam, SenderName: "Andi", Text: "PR #142 is ready for review", Timestamp: step(20), FromMe: false},
			{ID: "4", ChatID: devTeam, SenderName: fakeBotName, Text: "On it — reviewing now", Timestamp: step(18), FromMe: true},
			{ID: "5", ChatID: devTeam, SenderName: "Citra", Text: "Let's ship the release tonight 🚀", Timestamp: step(2), FromMe: false},
		},
		newsChan: {
			{ID: "1", ChatID: newsChan, SenderName: "NusaShell News", Text: "NusaShell 0.4 released — Kanban plugin improvements", Timestamp: step(120), FromMe: false},
			{ID: "2", ChatID: newsChan, SenderName: "NusaShell News", Text: "New plugin: Telegram bridge is live!", Timestamp: step(15), FromMe: false},
		},
		andiDM: {
			{ID: "1", ChatID: andiDM, SenderName: "Andi", Text: "Hey, can you review my PR?", Timestamp: step(30), FromMe: false},
			{ID: "2", ChatID: andiDM, SenderName: fakeBotName, Text: "Sure, looking at it now", Timestamp: step(28), FromMe: true},
			{ID: "3", ChatID: andiDM, SenderName: "Andi", Text: "Thanks!", Timestamp: step(5), FromMe: false},
		},
		budiDM: {
			{ID: "1", ChatID: budiDM, SenderName: "Budi", Text: "The deploy script is broken", Timestamp: step(60), FromMe: false},
			{ID: "2", ChatID: budiDM, SenderName: fakeBotName, Text: "I'll take a look", Timestamp: step(58), FromMe: true},
			{ID: "3", ChatID: budiDM, SenderName: "Budi", Text: "Thanks for the help!", Timestamp: step(3), FromMe: false},
		},
	}

	chats = []ChatRow{
		{ID: devTeam, Type: "group", Name: "Dev Team", LastMessage: "Let's ship the release tonight 🚀", LastMessageAt: step(2), UnreadCount: 3},
		{ID: newsChan, Type: "channel", Name: "NusaShell News", LastMessage: "New plugin: Telegram bridge is live!", LastMessageAt: step(15), UnreadCount: 1},
		{ID: andiDM, Type: "dm", Name: "Andi", LastMessage: "Thanks!", LastMessageAt: step(5), UnreadCount: 0},
		{ID: budiDM, Type: "dm", Name: "Budi", LastMessage: "Thanks for the help!", LastMessageAt: step(3), UnreadCount: 0},
	}

	approvals = []ApprovalRow{
		{ID: "a1", ChatID: devTeam, MessageID: "4", Text: "approve:yes", SenderID: andiDM, Time: step(18), Status: "pending"},
		{ID: "a2", ChatID: devTeam, MessageID: "4", Text: "approve:no", SenderID: budiDM, Time: step(17), Status: "pending"},
	}
	return chats, messages, approvals
}

// chatTypeFor returns the mock chat type for a chat id, defaulting to "dm".
func chatTypeFor(chatID string) string {
	switch chatID {
	case "-1001234567890":
		return "group"
	case "-1009876543210":
		return "channel"
	default:
		return "dm"
	}
}

// chatNameFor returns the mock chat name for a chat id from the given chats, or
// the id itself when unknown (so an upsert never wipes a resolved name with "").
func chatNameFor(chats []ChatRow, chatID string) string {
	for _, ch := range chats {
		if ch.ID == chatID {
			return ch.Name
		}
	}
	return chatID
}

// newestFirst returns a copy of msgs ordered newest first.
func newestFirst(msgs []MessageRow) []MessageRow {
	out := make([]MessageRow, len(msgs))
	copy(out, msgs)
	sortMessagesNewestFirst(out)
	return out
}

// sortMessagesNewestFirst orders msgs by timestamp descending (in place).
func sortMessagesNewestFirst(msgs []MessageRow) {
	for i := 0; i < len(msgs); i++ {
		for j := i + 1; j < len(msgs); j++ {
			if msgs[j].Timestamp > msgs[i].Timestamp {
				msgs[i], msgs[j] = msgs[j], msgs[i]
			}
		}
	}
}

// sortChatsByActivity orders chats by last activity descending (in place).
func sortChatsByActivity(chats []ChatRow) {
	for i := 0; i < len(chats); i++ {
		for j := i + 1; j < len(chats); j++ {
			if chats[j].LastMessageAt > chats[i].LastMessageAt {
				chats[i], chats[j] = chats[j], chats[i]
			}
		}
	}
}
