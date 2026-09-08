package main

import (
	"context"
	"testing"
	"time"
)

func TestRecordOutboundMessageMirrorsChatAndMessage(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	chatJID := "15550000001@s.whatsapp.net"
	ts := time.Unix(1700000000, 0)
	recordOutboundMessage(ctx, store, chatJID, SendResult{MessageID: "OUT1", Timestamp: ts}, "sent text", "QUOTE1")

	messages, err := store.GetMessages(ctx, chatJID, 0, 50)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("GetMessages returned %d rows, want 1", len(messages))
	}
	got := messages[0]
	if got.ID != "OUT1" || got.Text != "sent text" || !got.FromMe || got.QuotedID != "QUOTE1" {
		t.Fatalf("outbound row = %+v, want mirrored from-me message", got)
	}
	chat, err := store.GetChat(ctx, chatJID)
	if err != nil || chat == nil {
		t.Fatalf("GetChat: chat=%+v err=%v", chat, err)
	}
	if chat.LastMessage != "sent text" || chat.LastMessageAt != ts.Unix() {
		t.Fatalf("outbound chat = %+v, want latest text/time", chat)
	}
}

func TestRecordOutboundMediaMirrorsMediaAndMessage(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	chatJID := "120363000000000000@g.us"
	ts := time.Unix(1700000000, 0)
	recordOutboundMedia(ctx, store, chatJID, SendResult{MessageID: "OUTMEDIA1", Timestamp: ts}, "image", "image/jpeg", 123, "caption")

	messages, err := store.GetMessages(ctx, chatJID, 0, 50)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(messages) != 1 || !messages[0].FromMe || !messages[0].HasMedia || messages[0].Text != "caption" {
		t.Fatalf("outbound media message = %+v, want from-me media with caption", messages)
	}
	media, err := store.GetMedia(ctx, chatJID, "OUTMEDIA1")
	if err != nil || media == nil {
		t.Fatalf("GetMedia: media=%+v err=%v", media, err)
	}
	if media.Kind != "image" || media.MimeType != "image/jpeg" || media.Size != 123 {
		t.Fatalf("outbound media metadata = %+v, want image/jpeg size=123", media)
	}
}
