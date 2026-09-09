package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// progressStub is a minimal Client that records SendText / EditMessage for
// progress-tool tests without touching SQLite.
type progressStub struct {
	mu        sync.Mutex
	nextID    int64
	sends     []progressSend
	edits     []progressEdit
	failEdits map[string]bool // message_id -> force edit failure
}

type progressSend struct {
	ChatID, Text, ParseMode string
}

type progressEdit struct {
	ChatID, MessageID, Text, ParseMode string
}

func newProgressStub() *progressStub {
	return &progressStub{nextID: 5000, failEdits: map[string]bool{}}
}

func (c *progressStub) State() PairState { return PairState{Paired: true, Connected: true} }
func (c *progressStub) Login(ctx context.Context, token string) (PairState, error) {
	return c.State(), nil
}
func (c *progressStub) Logout(ctx context.Context) error { return nil }
func (c *progressStub) SendMedia(ctx context.Context, chatID, filePath, kind, caption string, replyTo int64) (SendResult, error) {
	return SendResult{}, fmt.Errorf("not implemented")
}
func (c *progressStub) SendInlineButtons(ctx context.Context, chatID, text string, buttons [][]InlineButton, replyTo int64) (SendResult, error) {
	return SendResult{}, fmt.Errorf("not implemented")
}
func (c *progressStub) DeleteMessage(ctx context.Context, chatID, messageID string) error {
	return fmt.Errorf("not implemented")
}
func (c *progressStub) AnswerCallback(ctx context.Context, callbackQueryID, text string, showAlert bool) error {
	return fmt.Errorf("not implemented")
}
func (c *progressStub) SendChatAction(ctx context.Context, chatID, action string) error {
	return nil
}
func (c *progressStub) RequestSync(ctx context.Context, chatID string) error { return nil }

func (c *progressStub) SendText(ctx context.Context, chatID, text string, replyTo int64, parseMode string, disableNotification bool) (SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	c.sends = append(c.sends, progressSend{ChatID: chatID, Text: text, ParseMode: parseMode})
	return SendResult{MessageID: c.nextID, Timestamp: time.Now()}, nil
}

func (c *progressStub) EditMessage(ctx context.Context, chatID, messageID, text, parseMode string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.edits = append(c.edits, progressEdit{ChatID: chatID, MessageID: messageID, Text: text, ParseMode: parseMode})
	if c.failEdits[messageID] {
		return fmt.Errorf("message to edit not found")
	}
	return nil
}

func (c *progressStub) lastSend() progressSend {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sends) == 0 {
		return progressSend{}
	}
	return c.sends[len(c.sends)-1]
}

func TestSendProgress_SendPath(t *testing.T) {
	cli := newProgressStub()
	h := handleSendProgress(cli)
	res := callHandler(t, h, map[string]any{
		"chat_id":    "520213916",
		"event_type": "tool_started",
		"status":     "running",
		"title":      "search_messages",
		"detail":     "query=hello",
	})
	m := decodeResult(t, res)
	if m["edited"] != false {
		t.Errorf("edited = %v, want false", m["edited"])
	}
	if m["chat_id"] != "520213916" {
		t.Errorf("chat_id = %v", m["chat_id"])
	}
	mid, _ := m["message_id"].(string)
	if mid == "" {
		t.Fatalf("message_id missing or not string: %#v", m["message_id"])
	}
	sent := cli.lastSend()
	if sent.ParseMode != "HTML" {
		t.Errorf("parse_mode = %q, want HTML", sent.ParseMode)
	}
	if !strings.Contains(sent.Text, "Tool started") {
		t.Errorf("payload missing event label: %q", sent.Text)
	}
	if !strings.Contains(sent.Text, "running") {
		t.Errorf("payload missing status: %q", sent.Text)
	}
	if !strings.Contains(sent.Text, "search_messages") || !strings.Contains(sent.Text, "query=hello") {
		t.Errorf("payload missing title/detail: %q", sent.Text)
	}
	if len(cli.edits) != 0 {
		t.Errorf("unexpected edits: %+v", cli.edits)
	}
}

func TestSendProgress_EditPath(t *testing.T) {
	cli := newProgressStub()
	h := handleSendProgress(cli)
	res := callHandler(t, h, map[string]any{
		"chat_id":    "520213916",
		"event_type": "tool_ended",
		"status":     "ok",
		"title":      "search_messages",
		"message_id": "42",
	})
	m := decodeResult(t, res)
	if m["edited"] != true {
		t.Errorf("edited = %v, want true", m["edited"])
	}
	if m["message_id"] != "42" {
		t.Errorf("message_id = %v, want 42", m["message_id"])
	}
	if len(cli.sends) != 0 {
		t.Errorf("unexpected sends on successful edit: %+v", cli.sends)
	}
	if len(cli.edits) != 1 || cli.edits[0].MessageID != "42" {
		t.Errorf("edits = %+v, want one edit of 42", cli.edits)
	}
	if cli.edits[0].ParseMode != "HTML" {
		t.Errorf("edit parse_mode = %q", cli.edits[0].ParseMode)
	}
}

func TestSendProgress_StaleMessageFallsBackToSend(t *testing.T) {
	cli := newProgressStub()
	cli.failEdits["999"] = true
	h := handleSendProgress(cli)
	res := callHandler(t, h, map[string]any{
		"chat_id":    "520213916",
		"event_type": "step_ended",
		"message_id": "999",
	})
	m := decodeResult(t, res)
	if m["edited"] != false {
		t.Errorf("edited = %v, want false after fallback", m["edited"])
	}
	mid, _ := m["message_id"].(string)
	if mid == "" || mid == "999" {
		t.Errorf("message_id = %q, want a new id (not the stale 999)", mid)
	}
	if len(cli.edits) != 1 || len(cli.sends) != 1 {
		t.Errorf("want 1 edit attempt + 1 send, got edits=%d sends=%d", len(cli.edits), len(cli.sends))
	}
}

func TestSendProgress_Validation(t *testing.T) {
	cli := newProgressStub()
	h := handleSendProgress(cli)

	badChat := callHandler(t, h, map[string]any{
		"chat_id":    "not-a-chat",
		"event_type": "text",
	})
	if !badChat.IsError {
		t.Fatalf("bad chat_id should error, got %#v", badChat.StructuredContent)
	}

	badEvent := callHandler(t, h, map[string]any{
		"chat_id":    "520213916",
		"event_type": "not_an_event",
	})
	if !badEvent.IsError {
		t.Fatalf("bad event_type should error")
	}
	if len(cli.sends) != 0 || len(cli.edits) != 0 {
		t.Errorf("validation failures must not call Telegram: sends=%d edits=%d", len(cli.sends), len(cli.edits))
	}
}

func TestSendProgress_NoStoreMirror(t *testing.T) {
	store := newStoreOf(t)
	before, err := store.CountMessages(context.Background())
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}

	// Handler takes only Client — no *Store — so even a live store wired like
	// other tests cannot gain rows from progress traffic.
	cli := newProgressStub()
	h := handleSendProgress(cli)
	_ = decodeResult(t, callHandler(t, h, map[string]any{
		"chat_id":    "520213916",
		"event_type": "reasoning",
		"title":      "thinking",
		"detail":     "plan next step",
	}))

	after, err := store.CountMessages(context.Background())
	if err != nil {
		t.Fatalf("CountMessages after: %v", err)
	}
	if after != before {
		t.Errorf("store message count %d → %d; progress must not mirror", before, after)
	}
}

func TestSendProgress_DefaultStatusOK(t *testing.T) {
	cli := newProgressStub()
	h := handleSendProgress(cli)
	_ = decodeResult(t, callHandler(t, h, map[string]any{
		"chat_id":    "520213916",
		"event_type": "text",
		"title":      "hi",
	}))
	if !strings.Contains(cli.lastSend().Text, "ok") {
		t.Errorf("default status missing from payload: %q", cli.lastSend().Text)
	}
}

func TestSendProgress_EscapesHTML(t *testing.T) {
	cli := newProgressStub()
	h := handleSendProgress(cli)
	_ = decodeResult(t, callHandler(t, h, map[string]any{
		"chat_id":    "520213916",
		"event_type": "text",
		"title":      "<script>",
		"detail":     "a & b",
	}))
	text := cli.lastSend().Text
	if strings.Contains(text, "<script>") {
		t.Errorf("title not escaped: %q", text)
	}
	if !strings.Contains(text, "&lt;script&gt;") || !strings.Contains(text, "a &amp; b") {
		t.Errorf("expected escaped HTML entities in %q", text)
	}
}

func TestRegisterTools_ProgressDualAlias(t *testing.T) {
	_, _, _, srv := newTestRig(t)
	tools := srv.ListTools()
	if tools[toolInternalSendProgress] == nil {
		t.Errorf("missing tool %s", toolInternalSendProgress)
	}
	if tools[toolAdminSendProgress] == nil {
		t.Errorf("missing tool %s", toolAdminSendProgress)
	}
	// Same handler instance for both aliases.
	if tools[toolInternalSendProgress] != nil && tools[toolAdminSendProgress] != nil {
		// Handlers are distinct closures from the same factory call site in
		// registerTools (one progressHandler shared) — assert shared pointer.
		if fmt.Sprintf("%p", tools[toolInternalSendProgress].Handler) != fmt.Sprintf("%p", tools[toolAdminSendProgress].Handler) {
			t.Errorf("aliases should share the same handler func value")
		}
	}
}

func TestFormatProgressHTML(t *testing.T) {
	got := formatProgressHTML("tool_started", "running", "foo", "bar")
	if !strings.Contains(got, "<b>Tool started</b>") {
		t.Errorf("label missing: %q", got)
	}
	if !strings.Contains(got, "running") || !strings.Contains(got, "foo") || !strings.Contains(got, "bar") {
		t.Errorf("fields missing: %q", got)
	}
}

// Ensure progressStub satisfies Client at compile time.
var _ Client = (*progressStub)(nil)
