package main

import (
	"context"
	"testing"
	"time"
)

func TestGetMediaPreservesLocalPathAndSHA256(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	media := EventMedia{
		ChatJID:     "15550000001@s.whatsapp.net",
		SenderJID:   "15550000001@s.whatsapp.net",
		ID:          "MEDIA1",
		Caption:     "a photo",
		Timestamp:   time.Unix(1700000000, 0),
		Kind:        "image",
		MimeType:    "image/jpeg",
		Size:        42,
		DownloadRef: "opaque-ref",
	}
	if err := store.UpsertMedia(ctx, media); err != nil {
		t.Fatalf("UpsertMedia: %v", err)
	}
	if err := store.UpdateMediaDownloaded(ctx, media.ChatJID, media.ID, "/tmp/photo.jpg", "abc123", 123); err != nil {
		t.Fatalf("UpdateMediaDownloaded: %v", err)
	}

	got, err := store.GetMedia(ctx, media.ChatJID, media.ID)
	if err != nil {
		t.Fatalf("GetMedia: %v", err)
	}
	if got == nil {
		t.Fatal("GetMedia returned nil")
	}
	if got.LocalPath != "/tmp/photo.jpg" {
		t.Errorf("LocalPath = %q, want %q", got.LocalPath, "/tmp/photo.jpg")
	}
	if got.SHA256 != "abc123" {
		t.Errorf("SHA256 = %q, want %q", got.SHA256, "abc123")
	}
	if !got.Downloaded || got.Size != 123 {
		t.Errorf("download metadata = %+v, want downloaded=true size=123", got)
	}
}
