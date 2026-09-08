package main

import (
	"context"
	"testing"
)

func TestUpsertChatKeepsNewestMessagePreviewAcrossMetadataUpdate(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	const jid = "15550000001@s.whatsapp.net"
	if err := store.UpsertChat(ctx, jid, "direct", "Alice", "newest message", 200); err != nil {
		t.Fatalf("initial UpsertChat: %v", err)
	}
	if err := store.UpsertChat(ctx, jid, "direct", "Alice Renamed", "", 0); err != nil {
		t.Fatalf("metadata UpsertChat: %v", err)
	}
	if err := store.UpsertChat(ctx, jid, "direct", "", "stale message", 100); err != nil {
		t.Fatalf("stale UpsertChat: %v", err)
	}

	chat, err := store.GetChat(ctx, jid)
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if chat == nil {
		t.Fatal("GetChat returned nil")
	}
	if chat.Name != "Alice Renamed" {
		t.Errorf("Name = %q, want metadata update", chat.Name)
	}
	if chat.LastMessage != "newest message" || chat.LastMessageAt != 200 {
		t.Errorf("latest preview = (%q, %d), want (%q, %d)", chat.LastMessage, chat.LastMessageAt, "newest message", 200)
	}
}
