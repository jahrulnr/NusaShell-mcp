package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
)

// stderr is the package logger for the Telegram plugin. It writes to stderr
// (never stdout, which is reserved for the MCP JSON-RPC transport). Defined
// here because the ingester is its primary caller; other files in this
// package reuse it rather than redefining it.
func stderr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[nusashell-telegram] "+format+"\n", args...)
}

// watermarkKey is the meta key under which the last ingested Telegram update_id
// is persisted. On restart the client reads it to set the getUpdates offset
// (offset = last_update_id + 1) so no updates are missed or reprocessed.
const watermarkKey = "last_update_id"

// Ingester drains the normalized event channel and writes to the store. It is
// the sole writer to telegram.db — read-side tool handlers query directly
// without competing for write locks (WAL allows concurrent readers while the
// ingester writes).
//
// It persists the high-water update_id after each event so a restart resumes
// incrementally. If the events channel closes (long-poll dropped, network
// blip), it attempts to reconnect via the callback set with WithReconnect.
type Ingester struct {
	store     *Store
	reconnect func(ctx context.Context) (<-chan any, error)

	lastEventAt  time.Time
	lastUpdateID int
}

// NewIngester creates an ingester that writes to the given store. It loads the
// persisted watermark so Watermark() is correct before the first event lands.
func NewIngester(store *Store) *Ingester {
	in := &Ingester{store: store}
	if v, _ := store.GetMeta(context.Background(), watermarkKey); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			in.lastUpdateID = n
		}
	}
	return in
}

// WithReconnect registers a callback used to obtain a fresh events channel
// when the current one closes. Without it, Run returns when the channel
// closes. The callback should block until a new channel is available or ctx
// is cancelled.
func (in *Ingester) WithReconnect(fn func(ctx context.Context) (<-chan any, error)) *Ingester {
	in.reconnect = fn
	return in
}

// Run drains the event channel until ctx is cancelled. It blocks — call from
// a dedicated goroutine. If the channel closes and a reconnect callback is
// registered, it retries with backoff until a new channel is obtained or ctx
// is cancelled.
func (in *Ingester) Run(ctx context.Context, events <-chan any) {
	for events != nil {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				events = in.reconnectChannel(ctx)
				continue
			}
			in.handle(ctx, ev)
			in.lastEventAt = time.Now()
		}
	}
}

// reconnectChannel tries the reconnect callback with exponential-ish backoff
// until it yields a new channel. Returns nil (stopping Run) if no callback is
// registered or ctx is cancelled.
func (in *Ingester) reconnectChannel(ctx context.Context) <-chan any {
	if in.reconnect == nil {
		return nil
	}
	backoff := time.Second
	for {
		ch, err := in.reconnect(ctx)
		if err == nil && ch != nil {
			return ch
		}
		if err != nil {
			stderr("ingest: reconnect: %s", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// LastEventAt returns the timestamp of the last successfully ingested event.
func (in *Ingester) LastEventAt() time.Time { return in.lastEventAt }

// Watermark returns the highest update_id persisted so far. The client uses
// this on startup to set the getUpdates offset.
func (in *Ingester) Watermark() int { return in.lastUpdateID }

// handle dispatches a normalized event to the appropriate store method and
// advances the watermark.
func (in *Ingester) handle(ctx context.Context, ev any) {
	e, ok := ev.(TelegramEvent)
	if !ok {
		return
	}

	switch e.Type {
	case EventMessage:
		in.handleMessage(ctx, e)
	case EventEditedMessage:
		in.handleEditedMessage(ctx, e)
	case EventChannelPost:
		in.handleMessage(ctx, e) // channel posts are stored like messages
	case EventCallbackQuery:
		in.handleCallbackQuery(ctx, e)
	}

	in.advanceWatermark(ctx, e.UpdateID())
}

// advanceWatermark persists the new high-water update_id when it advances.
// Telegram update_ids are monotonic per bot, so this is a safe watermark.
func (in *Ingester) advanceWatermark(ctx context.Context, updateID int) {
	if updateID <= in.lastUpdateID {
		return
	}
	in.lastUpdateID = updateID
	if err := in.store.SetMeta(ctx, watermarkKey, strconv.Itoa(updateID)); err != nil {
		stderr("ingest: persist watermark: %s", err)
	}
}

// handleMessage upserts the chat and inserts the message. Inbound messages
// (not from the bot) increment the chat's unread counter.
func (in *Ingester) handleMessage(ctx context.Context, e TelegramEvent) {
	if err := in.store.UpsertChat(ctx, e.ChatID(), e.ChatType(), e.ChatName(), e.Text(), e.Timestamp); err != nil {
		stderr("ingest: upsert chat: %s", err)
		return
	}

	if err := in.store.InsertMessage(ctx, MessageRow{
		ID:         e.MessageID(),
		ChatID:     e.ChatID(),
		SenderName: e.SenderName(),
		Text:       e.Text(),
		Timestamp:  e.Timestamp,
		FromMe:     e.FromMe(),
	}); err != nil {
		stderr("ingest: insert message: %s", err)
		return
	}

	if !e.FromMe() {
		if err := in.store.IncrementUnread(ctx, e.ChatID()); err != nil {
			stderr("ingest: increment unread: %s", err)
		}
	}
}

// handleEditedMessage updates the stored message text and records edited_at.
// If the original message isn't stored yet (bot joined after it was sent),
// the update is a no-op — Telegram only sends edits for messages the bot
// already knows, so this is rare.
func (in *Ingester) handleEditedMessage(ctx context.Context, e TelegramEvent) {
	editedAt := e.EditedAt()
	if editedAt == 0 {
		editedAt = e.Timestamp
	}
	if err := in.store.UpdateMessageEdited(ctx, e.ChatID(), e.MessageID(), e.Text(), editedAt); err != nil {
		stderr("ingest: update edited: %s", err)
	}
	if err := in.store.UpsertChat(ctx, e.ChatID(), e.ChatType(), e.ChatName(), e.Text(), e.Timestamp); err != nil {
		stderr("ingest: upsert chat (edited): %s", err)
	}
}

// handleCallbackQuery records the button press as a pending approval row so
// list_pending_approvals can surface it. The answer_callback tool resolves it
// (approved/denied). The callback query id is the approval id; the callback
// data is stored as the text/payload.
func (in *Ingester) handleCallbackQuery(ctx context.Context, e TelegramEvent) {
	app := ApprovalRow{
		ID:        e.CallbackID(),
		ChatID:    e.ChatID(),
		MessageID: e.MessageID(),
		Text:      e.CallbackData(),
		SenderID:  e.SenderID(),
		Time:      e.Timestamp,
		Status:    "pending",
	}
	if err := in.store.UpsertApproval(ctx, app); err != nil {
		stderr("ingest: upsert approval: %s", err)
	}
}
