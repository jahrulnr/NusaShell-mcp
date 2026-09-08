package main

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
	"github.com/mymmrac/telego"
)

func TestInboundEventParamsUsesGenericBusinessEventContract(t *testing.T) {
	event := TelegramEvent{
		Type: EventMessage,
		Data: map[string]any{
			kChatID:         "520213916",
			kChatType:       "dm",
			kChatName:       "Jahrul",
			kMessageID:      "42",
			kText:           "halo dari Telegram",
			kSenderID:       "520213916",
			kSenderUsername: "jahrulnr",
			kSenderName:     "Jahrul",
			kFromMe:         false,
		},
		Timestamp: 1700000000,
	}

	got := inboundEventParams(event)
	if got["schema_version"] != mcpkit.BusinessEventSchemaVersion {
		t.Fatalf("schema_version = %#v, want %d", got["schema_version"], mcpkit.BusinessEventSchemaVersion)
	}
	if got["event_id"] != "message:520213916:42" {
		t.Fatalf("event_id = %#v, want stable message identity", got["event_id"])
	}
	if got["type"] != "telegram.message" {
		t.Fatalf("type = %#v, want telegram.message", got["type"])
	}
	if got["occurred_at"] != time.Unix(1700000000, 0).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("occurred_at = %#v, want event timestamp", got["occurred_at"])
	}
	if got["subject"] != "Jahrul" {
		t.Fatalf("subject = %#v, want sender name", got["subject"])
	}

	attrs, ok := got["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("attributes = %#v, want object", got["attributes"])
	}
	for key, want := range map[string]any{
		"chat_id":         "520213916",
		"chat_type":       "dm",
		"message_id":      "42",
		"sender_id":       "520213916",
		"sender_username": "jahrulnr",
		"sender_name":     "Jahrul",
		"text":            "halo dari Telegram",
		"from_me":         false,
	} {
		if attrs[key] != want {
			t.Errorf("attributes[%q] = %#v, want %#v", key, attrs[key], want)
		}
	}
	data, ok := got["data"].(map[string]any)
	if !ok || data["text"] != "halo dari Telegram" {
		t.Fatalf("data = %#v, want bounded text object", got["data"])
	}
}

func TestInboundEventParamsFallsBackToChatAsSubject(t *testing.T) {
	event := TelegramEvent{
		Data: map[string]any{
			kChatID:    "-1009",
			kChatName:  "Ops",
			kMessageID: "7",
		},
	}
	got := inboundEventParams(event)
	if got["subject"] != "Ops" {
		t.Fatalf("subject = %#v, want chat name fallback", got["subject"])
	}
	if _, ok := got["occurred_at"]; ok {
		t.Fatalf("zero timestamp emitted occurred_at: %#v", got["occurred_at"])
	}
}

func TestInboundEventParamsTruncatesTextToContractLimit(t *testing.T) {
	text := strings.Repeat("a", 200) + "z"
	event := TelegramEvent{
		Type: EventMessage,
		Data: map[string]any{
			kChatID:    "12345",
			kMessageID: "1",
			kText:      text,
		},
	}
	got := inboundEventParams(event)
	attrs := got["attributes"].(map[string]any)
	bounded := attrs["text"].(string)
	if gotRunes := len([]rune(bounded)); gotRunes != 200 {
		t.Fatalf("bounded text has %d runes, want 200: %q", gotRunes, bounded)
	}
	if !strings.HasSuffix(bounded, "…") {
		t.Fatalf("bounded text = %q, want ellipsis suffix", bounded)
	}
}

func TestIngester_DuplicateInboundDoesNotNotifyOrIncrementUnread(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ing := NewIngester(store)
	notified := 0
	ing.WithInboundNotify(func(TelegramEvent) { notified++ })
	event := TelegramEvent{
		Type: EventMessage,
		Data: map[string]any{
			kChatID:     "520213916",
			kChatType:   "dm",
			kChatName:   "Jahrul",
			kMessageID:  "42",
			kText:       "retry me",
			kSenderName: "Jahrul",
			kFromMe:     false,
			kUpdateID:   int64(42),
		},
		Timestamp: 1700000000,
	}

	ing.handle(context.Background(), event)
	ing.handle(context.Background(), event)

	count, err := store.CountMessages(context.Background())
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if count != 1 {
		t.Fatalf("message count = %d, want 1 after duplicate delivery", count)
	}
	chat, err := store.GetChat(context.Background(), event.ChatID())
	if err != nil || chat == nil {
		t.Fatalf("GetChat: chat=%+v err=%v", chat, err)
	}
	if chat.UnreadCount != 1 {
		t.Errorf("unread_count = %d, want 1 after duplicate delivery", chat.UnreadCount)
	}
	if notified != 1 {
		t.Errorf("notifications = %d, want 1 after duplicate delivery", notified)
	}
}

func TestIngesterStatusAccessorsAreSafeDuringIngest(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ing := NewIngester(store)
	events := make(chan any, 8)
	done := make(chan struct{})
	go func() {
		ing.Run(context.Background(), events)
		close(done)
	}()

	stopReaders := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
					ing.LastEventAt()
					ing.Watermark()
				}
			}
		}()
	}

	for i := 1; i <= 100; i++ {
		events <- TelegramEvent{
			Type: EventMessage,
			Data: map[string]any{
				kChatID:    "520213916",
				kMessageID: strconv.Itoa(i),
				kUpdateID:  int64(i),
				kText:      "message",
			},
			Timestamp: 1700000000 + int64(i),
		}
	}
	close(events)
	<-done
	close(stopReaders)
	readers.Wait()
}

func TestIngesterNotificationHookIsSafeDuringIngest(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ing := NewIngester(store)
	stop := make(chan struct{})
	var setters sync.WaitGroup
	setters.Add(1)
	go func() {
		defer setters.Done()
		for {
			select {
			case <-stop:
				return
			default:
				ing.WithInboundNotify(func(TelegramEvent) {})
			}
		}
	}()

	for i := 1; i <= 100; i++ {
		ing.handle(context.Background(), TelegramEvent{
			Type: EventMessage,
			Data: map[string]any{
				kChatID:    "520213916",
				kMessageID: strconv.Itoa(i),
				kText:      "message",
				kUpdateID:  int64(i),
				kFromMe:    false,
			},
			Timestamp: 1700000000 + int64(i),
		})
	}
	close(stop)
	setters.Wait()
}

func TestEditedChannelPostUpdatesExistingMessage(t *testing.T) {
	update := telego.Update{
		UpdateID: 99,
		EditedChannelPost: &telego.Message{
			MessageID: 7,
			Date:      1700000000,
			EditDate:  1700000010,
			Chat: telego.Chat{
				ID:    -1009,
				Type:  "channel",
				Title: "Ops",
			},
			Text: "edited announcement",
		},
	}

	event, ok := NormalizeUpdate(0, update)
	if !ok {
		t.Fatal("NormalizeUpdate returned false for edited channel post")
	}
	if event.Type != EventEditedChannelPost {
		t.Fatalf("event type = %q, want %q", event.Type, EventEditedChannelPost)
	}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.InsertMessage(ctx, MessageRow{
		ID: "7", ChatID: "-1009", Text: "original", Timestamp: 1700000000,
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	NewIngester(store).handle(ctx, event)
	messages, err := store.GetMessages(ctx, "-1009", 10, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one updated row", messages)
	}
	if messages[0].Text != "edited announcement" {
		t.Errorf("message text = %q, want edited announcement", messages[0].Text)
	}
	if messages[0].EditedAt == nil || *messages[0].EditedAt != 1700000010 {
		t.Errorf("edited_at = %v, want 1700000010", messages[0].EditedAt)
	}
}
