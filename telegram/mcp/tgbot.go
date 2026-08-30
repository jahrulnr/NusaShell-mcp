// tgbot.go is the production Client implementation, backed by the Telegram
// Bot API via github.com/mymmrac/telego. It owns the bot instance, the long
// polling loop, the normalized event channel (carrying TelegramEvent values
// for the ingester), and the watermark-based incremental offset.
//
// Read-side tools (get_messages, search_messages, list_chats, ...) query the
// Store directly; this client only reads the store for the ingestion watermark
// (so polling resumes from the last seen update_id) and for privacy/allowlist
// enforcement on inbound messages.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// tokenFile is the name of the bot token file stored in the data directory.
// Login writes the token here (mode 0600); Connect reads it. This constant is
// the contract between the client and the login tool.
const tokenFile = "bot-token"

// maxEventBacklog is the event channel buffer. The ingester drains it; if it
// ever falls behind, events are dropped (logged) rather than blocking the
// polling loop.
const maxEventBacklog = 256

// BotClient is the telego-backed Client implementation.
type BotClient struct {
	mu       sync.RWMutex
	bot      *telego.Bot
	botID    int64
	store    *Store
	dataDir  string
	verbose  bool
	state    PairState
	events   chan any
	pollStop context.CancelFunc
}

// NewTelegramClient creates a telego-backed client. The store is used for the
// ingestion watermark (resume offset) and privacy/allowlist enforcement.
func NewTelegramClient(store *Store, dataDir string, verbose bool) *BotClient {
	return &BotClient{
		store:   store,
		dataDir: dataDir,
		verbose: verbose,
		events:  make(chan any, maxEventBacklog),
	}
}

func (c *BotClient) tokenPath() string { return filepath.Join(c.dataDir, tokenFile) }

// State reports the current pairing/connection state.
func (c *BotClient) State() PairState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Events returns the normalized event channel (carrying TelegramEvent values).
// It is valid for the lifetime of the client; the ingester drains it until its
// own context is cancelled.
func (c *BotClient) Events(ctx context.Context) <-chan any { return c.events }

// Connect brings the bot up from the stored token. Returns ErrNotPaired if no
// token is stored. Used by main.go on startup.
func (c *BotClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.state.Connected {
		c.mu.Unlock()
		return nil
	}
	tokenBytes, err := os.ReadFile(c.tokenPath())
	c.mu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotPaired
		}
		return fmt.Errorf("read token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return ErrNotPaired
	}
	return c.connectWithToken(ctx, token)
}

// Login validates a Bot API token via getMe, stores it (mode 0600), and starts
// long polling. Returns the resulting state.
func (c *BotClient) Login(ctx context.Context, token string) (PairState, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return PairState{}, errors.New("bot token is empty")
	}
	if s := c.State(); s.Paired {
		return PairState{}, errors.New("already paired — logout first")
	}
	// Validate before persisting so a bad token never overwrites a good one.
	bot, err := telego.NewBot(token)
	if err != nil {
		return PairState{}, fmt.Errorf("create bot: %w", err)
	}
	me, err := bot.GetMe(ctx)
	if err != nil {
		return PairState{}, fmt.Errorf("validate token (getMe): %w", err)
	}
	if err := os.WriteFile(c.tokenPath(), []byte(token), 0o600); err != nil {
		return PairState{}, fmt.Errorf("store token: %w", err)
	}
	if err := c.startWithBot(ctx, bot, me); err != nil {
		return PairState{}, err
	}
	return c.State(), nil
}

// connectWithToken creates the bot, validates it via getMe, and starts polling.
func (c *BotClient) connectWithToken(ctx context.Context, token string) error {
	bot, err := telego.NewBot(token)
	if err != nil {
		return fmt.Errorf("create bot: %w", err)
	}
	me, err := bot.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("validate token (getMe): %w", err)
	}
	return c.startWithBot(ctx, bot, me)
}

// startWithBot begins long polling with an already-validated bot, resuming
// from the persisted update_id watermark (offset = last_update_id + 1) so no
// updates are missed or replayed across restarts. The polling loop runs in a
// goroutine and reconnects itself with backoff when the stream drops, so a
// transient network failure can never leave the bot stuck in a dead
// "connected" state.
func (c *BotClient) startWithBot(ctx context.Context, bot *telego.Bot, me *telego.User) error {
	pollCtx, pollStop := context.WithCancel(context.Background())

	c.mu.Lock()
	c.bot = bot
	c.botID = me.ID
	c.pollStop = pollStop
	c.state = PairState{
		Paired:    true,
		Connected: true,
		BotName:   userDisplayName(*me),
		BotID:     strconv.FormatInt(me.ID, 10),
	}
	c.mu.Unlock()

	go c.pollLoop(pollCtx, bot)
	return nil
}

// pollLoop runs the long-polling getUpdates stream, draining updates via pump
// and restarting the stream with exponential backoff whenever it drops or
// errors. It exits only when pollCtx is cancelled (Disconnect/Logout).
func (c *BotClient) pollLoop(pollCtx context.Context, bot *telego.Bot) {
	backoff := time.Second
	for {
		if pollCtx.Err() != nil {
			return
		}
		updates, err := bot.UpdatesViaLongPolling(pollCtx, &telego.GetUpdatesParams{
			Offset:  c.nextUpdateOffset(),
			Timeout: 25,
			AllowedUpdates: []string{
				"message", "edited_message",
				"channel_post", "edited_channel_post",
				"callback_query",
			},
		})
		if err != nil {
			if pollCtx.Err() != nil {
				return
			}
			c.setConnected(false)
			stderr("long polling error: %s — reconnecting in %s", err, backoff)
		} else {
			c.setConnected(true)
			backoff = time.Second
			c.pump(updates)
			if pollCtx.Err() != nil {
				return
			}
			c.setConnected(false)
			stderr("long polling stream closed — reconnecting in %s", backoff)
		}
		if !sleepCtx(pollCtx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// nextUpdateOffset returns the getUpdates offset to resume from: the last
// ingested update_id + 1. Zero starts from the newest updates.
func (c *BotClient) nextUpdateOffset() int {
	if c.store == nil {
		return 0
	}
	v, _ := c.store.GetMeta(context.Background(), watermarkKey)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n + 1
	}
	return 0
}

// setConnected updates the connection state under lock. Tools read it to fail
// fast with ErrNotConnected (a retryable error) while polling is down.
func (c *BotClient) setConnected(v bool) {
	c.mu.Lock()
	c.state.Connected = v
	c.mu.Unlock()
}

// sleepCtx sleeps for d unless ctx is cancelled first. Returns false when
// cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Disconnect stops long polling cleanly. The event channel is left open so the
// ingester can keep draining until its own context is cancelled.
func (c *BotClient) Disconnect() {
	c.mu.Lock()
	stop := c.pollStop
	c.pollStop = nil
	c.bot = nil
	if c.state.Connected {
		c.state.Connected = false
	}
	c.mu.Unlock()
	if stop != nil {
		stop() // cancels pollCtx → updates chan closes → pump exits
	}
}

// Logout stops polling and clears the stored token.
func (c *BotClient) Logout(ctx context.Context) error {
	c.Disconnect()
	if err := os.Remove(c.tokenPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove token: %w", err)
	}
	c.mu.Lock()
	c.state = PairState{}
	c.botID = 0
	c.mu.Unlock()
	return nil
}

// SendText sends a text message.
func (c *BotClient) SendText(ctx context.Context, chatID, text string, replyTo int64, parseMode string, disableNotification bool) (SendResult, error) {
	bot, cid, err := c.ready(chatID)
	if err != nil {
		return SendResult{}, err
	}
	msg, err := bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID:              cid,
		Text:                text,
		ParseMode:           parseMode,
		DisableNotification: disableNotification,
		ReplyParameters:     replyParams(replyTo),
	})
	if err != nil {
		return SendResult{}, err
	}
	return sendResult(msg), nil
}

// SendMedia uploads and sends a file. Kind is "photo", "video", "audio", or
// "document" (default).
func (c *BotClient) SendMedia(ctx context.Context, chatID, filePath, kind, caption string, replyTo int64) (SendResult, error) {
	bot, cid, err := c.ready(chatID)
	if err != nil {
		return SendResult{}, err
	}
	f, err := os.Open(filePath)
	if err != nil {
		return SendResult{}, fmt.Errorf("open media: %w", err)
	}
	defer f.Close()
	file := tu.FileFromReader(f, filepath.Base(filePath))
	reply := replyParams(replyTo)

	switch strings.ToLower(kind) {
	case "photo":
		msg, err := bot.SendPhoto(ctx, &telego.SendPhotoParams{ChatID: cid, Photo: file, Caption: caption, ParseMode: "HTML", ReplyParameters: reply})
		if err != nil {
			return SendResult{}, err
		}
		return sendResult(msg), nil
	case "video":
		msg, err := bot.SendVideo(ctx, &telego.SendVideoParams{ChatID: cid, Video: file, Caption: caption, ParseMode: "HTML", ReplyParameters: reply})
		if err != nil {
			return SendResult{}, err
		}
		return sendResult(msg), nil
	case "audio":
		msg, err := bot.SendAudio(ctx, &telego.SendAudioParams{ChatID: cid, Audio: file, Caption: caption, ParseMode: "HTML", ReplyParameters: reply})
		if err != nil {
			return SendResult{}, err
		}
		return sendResult(msg), nil
	default: // "document"
		msg, err := bot.SendDocument(ctx, &telego.SendDocumentParams{ChatID: cid, Document: file, Caption: caption, ParseMode: "HTML", ReplyParameters: reply})
		if err != nil {
			return SendResult{}, err
		}
		return sendResult(msg), nil
	}
}

// SendInlineButtons sends a text message with an inline keyboard built from
// buttons (a slice of rows).
func (c *BotClient) SendInlineButtons(ctx context.Context, chatID, text string, buttons [][]InlineButton, replyTo int64) (SendResult, error) {
	bot, cid, err := c.ready(chatID)
	if err != nil {
		return SendResult{}, err
	}
	params := &telego.SendMessageParams{
		ChatID:          cid,
		Text:            text,
		ReplyParameters: replyParams(replyTo),
	}
	if len(buttons) > 0 {
		rows := make([][]telego.InlineKeyboardButton, 0, len(buttons))
		for _, row := range buttons {
			r := make([]telego.InlineKeyboardButton, 0, len(row))
			for _, b := range row {
				r = append(r, telego.InlineKeyboardButton{
					Text:         b.Label,
					CallbackData: b.CallbackData,
				})
			}
			rows = append(rows, r)
		}
		params.ReplyMarkup = &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
	}
	msg, err := bot.SendMessage(ctx, params)
	if err != nil {
		return SendResult{}, err
	}
	return sendResult(msg), nil
}

// EditMessage edits the text of an existing message (used for streaming).
// messageID is the Telegram message id as a string.
func (c *BotClient) EditMessage(ctx context.Context, chatID, messageID, text, parseMode string) error {
	bot, cid, err := c.ready(chatID)
	if err != nil {
		return err
	}
	mid, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message_id %q: %w", messageID, err)
	}
	_, err = bot.EditMessageText(ctx, &telego.EditMessageTextParams{
		ChatID:    cid,
		MessageID: mid,
		Text:      text,
		ParseMode: parseMode,
	})
	return err
}

// DeleteMessage deletes a message from a chat. messageID is a string.
func (c *BotClient) DeleteMessage(ctx context.Context, chatID, messageID string) error {
	bot, cid, err := c.ready(chatID)
	if err != nil {
		return err
	}
	mid, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message_id %q: %w", messageID, err)
	}
	return bot.DeleteMessage(ctx, &telego.DeleteMessageParams{ChatID: cid, MessageID: mid})
}

// AnswerCallback answers a callback query (e.g. approval/deny).
func (c *BotClient) AnswerCallback(ctx context.Context, callbackQueryID, text string, showAlert bool) error {
	c.mu.RLock()
	bot := c.bot
	connected := c.state.Connected
	c.mu.RUnlock()
	if !connected || bot == nil {
		return ErrNotConnected
	}
	return bot.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQueryID,
		Text:            text,
		ShowAlert:       showAlert,
	})
}

// SendChatAction sends a typing/uploading indicator.
func (c *BotClient) SendChatAction(ctx context.Context, chatID, action string) error {
	bot, cid, err := c.ready(chatID)
	if err != nil {
		return err
	}
	return bot.SendChatAction(ctx, &telego.SendChatActionParams{ChatID: cid, Action: action})
}

// RequestSync is a no-op for the cloud Bot API, which cannot backfill messages
// that arrived before polling started. The tool handler adds the explanatory
// hint; this hook exists so a future local-Bot-API/MTProto path can implement
// real backfill without changing the tool contract.
func (c *BotClient) RequestSync(ctx context.Context, chatID string) error {
	return nil
}

// --- internals ---------------------------------------------------------------

// ready returns an active bot and parsed chat id, or an error.
func (c *BotClient) ready(chatID string) (*telego.Bot, telego.ChatID, error) {
	c.mu.RLock()
	bot := c.bot
	connected := c.state.Connected
	c.mu.RUnlock()
	if !connected || bot == nil {
		return nil, telego.ChatID{}, ErrNotConnected
	}
	cid, err := parseChatID(chatID)
	if err != nil {
		return nil, telego.ChatID{}, err
	}
	return bot, cid, nil
}

func (c *BotClient) selfID() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.botID
}

// pump drains telego updates until the updates channel closes (pollCtx
// cancelled by Disconnect). Each update is normalized to a TelegramEvent and
// forwarded to the event channel, dropping inbound messages from senders not
// in the allowlist when privacy mode is on.
func (c *BotClient) pump(updates <-chan telego.Update) {
	for u := range updates {
		ev, ok := NormalizeUpdate(c.selfID(), u)
		if !ok {
			continue
		}
		if c.dropByPrivacy(ev) {
			continue
		}
		c.sendEvent(ev)
	}
}

// dropByPrivacy reports whether an inbound event should be dropped because
// privacy mode is on and the sender is not allowlisted. Outbound messages
// (from the bot) and events without a sender (channel posts) are never dropped.
func (c *BotClient) dropByPrivacy(e TelegramEvent) bool {
	if e.FromMe() {
		return false
	}
	sender := e.SenderID()
	if sender == "" {
		return false
	}
	if !c.privacyEnforced() {
		return false
	}
	return !c.senderAllowed(sender)
}

func (c *BotClient) privacyEnforced() bool {
	if c.store == nil {
		return false
	}
	v, _ := c.store.GetMeta(context.Background(), privacyModeMetaKey)
	return v == "1"
}

// senderAllowed reports whether senderID is in the allowlist. On read error it
// fails open (allows) so a transient store error never silently drops traffic.
func (c *BotClient) senderAllowed(senderID string) bool {
	if c.store == nil {
		return true
	}
	list, err := c.store.ListAllowlist(context.Background())
	if err != nil {
		return true
	}
	for _, id := range list {
		if id == senderID {
			return true
		}
	}
	return false
}

// sendEvent pushes an event to the channel, dropping (with a log line) if the
// backlog is full so the polling loop never blocks.
func (c *BotClient) sendEvent(ev any) {
	select {
	case c.events <- ev:
	default:
		stderr("event channel full, dropping %T", ev)
	}
}

// --- helpers ----------------------------------------------------------------

// parseChatID parses a chat id string. "@channel" forms a username chat id;
// any other value is parsed as a signed int64.
func parseChatID(s string) (telego.ChatID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return telego.ChatID{}, errors.New("chat_id is empty")
	}
	if strings.HasPrefix(s, "@") {
		return telego.ChatID{Username: s}, nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return telego.ChatID{}, fmt.Errorf("invalid chat_id %q: %w", s, err)
	}
	return telego.ChatID{ID: id}, nil
}

// replyParams builds a ReplyParameters pointer for replyTo, or nil if none.
func replyParams(replyTo int64) *telego.ReplyParameters {
	if replyTo <= 0 {
		return nil
	}
	return &telego.ReplyParameters{MessageID: int(replyTo)}
}

// sendResult converts a telego Message into a SendResult.
func sendResult(msg *telego.Message) SendResult {
	if msg == nil {
		return SendResult{}
	}
	return SendResult{
		MessageID: int64(msg.MessageID),
		Timestamp: time.Unix(msg.Date, 0),
	}
}
