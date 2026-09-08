package main

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHandleGetMessagesResetsUnread(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	chatJID := "15550000001@s.whatsapp.net"
	if err := store.UpsertChat(ctx, chatJID, "dm", "", "hello", 1700000000); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}
	if err := store.IncrementUnread(ctx, chatJID); err != nil {
		t.Fatalf("IncrementUnread: %v", err)
	}
	if err := store.InsertMessage(ctx, EventMessage{
		ChatJID:   chatJID,
		ID:        "READ1",
		Text:      "hello",
		Timestamp: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	h := handleGetMessages(store)
	res, err := h(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      toolGetMessages,
		Arguments: map[string]any{"chat_jid": chatJID, "limit": 50},
	}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("handler result = %#v, want success", res)
	}

	chat, err := store.GetChat(ctx, chatJID)
	if err != nil || chat == nil {
		t.Fatalf("GetChat: chat=%+v err=%v", chat, err)
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("unread_count = %d after get_messages, want 0", chat.UnreadCount)
	}
}
