package main

import (
	"context"
	"reflect"
	"testing"
)

func TestEmptyCursorHistorySlicesAreJSONArrays(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	cursor, err := store.GetMessagesCursor(ctx, "missing", 0, 50)
	if err != nil {
		t.Fatalf("GetMessagesCursor: %v", err)
	}
	if reflect.ValueOf(cursor).IsNil() {
		t.Fatal("GetMessagesCursor returned nil slice, want empty non-nil slice")
	}

	history, err := store.GetChatHistory(ctx, "missing")
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}
	if reflect.ValueOf(history).IsNil() {
		t.Fatal("GetChatHistory returned nil slice, want empty non-nil slice")
	}
}
