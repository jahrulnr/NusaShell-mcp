package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/NusaShell-mcp/mcpkit"
)

func TestInboundEventParamsUsesGenericBusinessEventContract(t *testing.T) {
	occurredAt := time.Unix(1700000000, 123456789).UTC()
	event := EventMessage{
		ChatJID:   "15550000001@s.whatsapp.net",
		SenderJID: "15550000001@s.whatsapp.net",
		ID:        "MSG42",
		Text:      "halo dari WhatsApp",
		Timestamp: occurredAt,
	}

	got := inboundEventParams(event)
	if got["schema_version"] != mcpkit.BusinessEventSchemaVersion {
		t.Fatalf("schema_version = %#v, want %d", got["schema_version"], mcpkit.BusinessEventSchemaVersion)
	}
	if got["event_id"] != "message:15550000001@s.whatsapp.net:MSG42" {
		t.Fatalf("event_id = %#v, want stable message identity", got["event_id"])
	}
	if got["type"] != "whatsapp.message_received" {
		t.Fatalf("type = %#v, want whatsapp.message_received", got["type"])
	}
	if got["occurred_at"] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("occurred_at = %#v, want event timestamp", got["occurred_at"])
	}
	if got["subject"] != event.SenderJID {
		t.Fatalf("subject = %#v, want sender JID fallback", got["subject"])
	}
	if _, ok := got["source"]; ok {
		t.Fatalf("publisher supplied host-owned source: %#v", got["source"])
	}

	attrs, ok := got["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("attributes = %#v, want object", got["attributes"])
	}
	for key, want := range map[string]any{
		"chat_id":    event.ChatJID,
		"chat_jid":   event.ChatJID,
		"chat_type":  "dm",
		"message_id": event.ID,
		"sender_id":  event.SenderJID,
		"sender_jid": event.SenderJID,
		"text":       event.Text,
		"from_me":    false,
		"kind":       "text",
	} {
		if attrs[key] != want {
			t.Errorf("attributes[%q] = %#v, want %#v", key, attrs[key], want)
		}
	}
	data, ok := got["data"].(map[string]any)
	if !ok || data["text"] != event.Text || data["kind"] != "text" {
		t.Fatalf("data = %#v, want message facts", got["data"])
	}
}

func TestInboundEventParamsTruncatesTextToContractLimit(t *testing.T) {
	text := strings.Repeat("a", 200) + "z"
	event := EventMessage{
		ChatJID:   "15550000001@s.whatsapp.net",
		SenderJID: "15550000001@s.whatsapp.net",
		ID:        "LONG1",
		Text:      text,
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

func TestIngester_NotifiesOnlyInboundMessages(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	ing := NewIngester(store)
	notified := 0
	ing.WithInboundNotify(func(any) { notified++ })

	base := EventMessage{
		ChatJID:   "15550000001@s.whatsapp.net",
		SenderJID: "15550000002@s.whatsapp.net",
		Timestamp: time.Unix(1700000000, 0),
	}
	inbound := base
	inbound.ID = "INBOUND1"
	ing.handle(ctx, inbound)
	outbound := base
	outbound.ID = "OUTBOUND1"
	outbound.FromMe = true
	ing.handle(ctx, outbound)

	media := EventMedia{
		ChatJID:   base.ChatJID,
		SenderJID: base.SenderJID,
		ID:        "MEDIA-INBOUND1",
		Kind:      "image",
		Timestamp: base.Timestamp,
	}
	ing.handle(ctx, media)
	media.FromMe = true
	media.ID = "MEDIA-OUTBOUND1"
	ing.handle(ctx, media)

	if notified != 2 {
		t.Fatalf("notifications = %d, want one each for inbound text and media", notified)
	}
}

func TestIngester_DuplicateMediaRefreshesDownloadReference(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	ing := NewIngester(store)
	media := EventMedia{
		ChatJID:   "15550000001@s.whatsapp.net",
		SenderJID: "15550000001@s.whatsapp.net",
		ID:        "MEDIA-ECHO-1",
		Kind:      "image",
		MimeType:  "image/jpeg",
		Size:      123,
		FromMe:    true,
		Timestamp: time.Unix(1700000000, 0),
	}
	ing.handle(ctx, media)
	media.DownloadRef = "provider-download-ref"
	ing.handle(ctx, media)

	got, err := store.GetMediaDownloadRef(ctx, media.ChatJID, media.ID)
	if err != nil {
		t.Fatalf("GetMediaDownloadRef: err=%v", err)
	}
	if got != media.DownloadRef {
		t.Fatalf("download_ref = %q, want provider echo %q", got, media.DownloadRef)
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
	ing.WithInboundNotify(func(any) { notified++ })
	event := EventMessage{
		ChatJID:   "15550000001@s.whatsapp.net",
		SenderJID: "15550000001@s.whatsapp.net",
		ID:        "DUP1",
		Text:      "retry me",
		Timestamp: time.Unix(1700000000, 0),
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
	chat, err := store.GetChat(context.Background(), event.ChatJID)
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
