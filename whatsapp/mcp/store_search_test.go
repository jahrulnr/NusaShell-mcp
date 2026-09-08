package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchMessagesReturnsInsertedText(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	message := EventMessage{
		ChatJID:   "15550000001@s.whatsapp.net",
		SenderJID: "15550000001@s.whatsapp.net",
		ID:        "SEARCH1",
		Text:      "the searchable WhatsApp message",
		Timestamp: time.Unix(1700000000, 0),
	}
	if err := store.UpsertChat(ctx, message.ChatJID, "dm", "", message.Text, message.Timestamp.Unix()); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}
	if err := store.InsertMessage(ctx, message); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	messages, err := store.SearchMessages(ctx, "searchable", "", "", 0, 0, 50)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("SearchMessages returned %d rows, want 1: %#v", len(messages), messages)
	}
	if messages[0].ID != message.ID || messages[0].Text != message.Text {
		t.Fatalf("SearchMessages row = %#v, want id=%q text=%q", messages[0], message.ID, message.Text)
	}
}

func TestMigrateRepairsLegacyWhatsAppFTSIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "whatsapp.db")
	dsn := "file:" + filepath.ToSlash(dbPath) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_foreign_keys=on"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	legacy := []string{
		`CREATE TABLE messages (
			id TEXT NOT NULL, chat_jid TEXT NOT NULL, sender_jid TEXT NOT NULL,
			text TEXT, timestamp INTEGER NOT NULL DEFAULT 0, from_me INTEGER NOT NULL DEFAULT 0,
			kind TEXT NOT NULL DEFAULT 'text', quoted_id TEXT NOT NULL DEFAULT '',
			edited_at INTEGER, deleted_at INTEGER, created_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat_jid, id))`,
		`CREATE VIRTUAL TABLE fts_messages USING fts5(
			message_id UNINDEXED, chat_jid UNINDEXED, text,
			content='messages', content_rowid='rowid')`,
		`INSERT INTO messages (id, chat_jid, sender_jid, text, timestamp, from_me)
			VALUES ('1', '15550000001@s.whatsapp.net', '15550000001@s.whatsapp.net', 'legacy searchable text', 100, 0)`,
	}
	for _, query := range legacy {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("legacy exec %q: %v", query, err)
		}
	}
	if _, err := db.Query(`SELECT message_id FROM fts_messages`); err == nil {
		t.Fatalf("expected legacy FTS index to be broken, but it queries fine")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore after migration: %v", err)
	}
	defer store.Close()

	rows, err := store.SearchMessages(context.Background(), "legacy", "", "", 0, 0, 50)
	if err != nil {
		t.Fatalf("SearchMessages after migration: %v", err)
	}
	if len(rows) != 1 || rows[0].Text != "legacy searchable text" {
		t.Fatalf("search after migration = %+v, want legacy row", rows)
	}
}
