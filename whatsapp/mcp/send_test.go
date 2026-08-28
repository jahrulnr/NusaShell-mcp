// Outbound wiring tests: verifies that SendText/SendMedia compose the
// pipeline (markdown conversion + chunking + reply-quote on first chunk)
// correctly. We don't dial the network — these tests exercise the pure
// helpers in the same package that SendText calls into.
package main

import (
	"strings"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow"
)

func TestSendTextPipeline_MarkdownThenChunk(t *testing.T) {
	// Bold and strikethrough get rewritten; the resulting body must still
	// be chunkable at the limit.
	body := "**hello** ~~world~~"
	formatted := markdownToWhatsApp(body)
	chunks := chunkText(formatted, maxMessageLen)

	if len(chunks) != 1 {
		t.Fatalf("short body chunked into %d parts, want 1", len(chunks))
	}
	if !strings.Contains(chunks[0], "*hello*") {
		t.Errorf("chunk[0] = %q, want bold rewritten to *hello*", chunks[0])
	}
	if !strings.Contains(chunks[0], "~world~") {
		t.Errorf("chunk[0] = %q, want strikethrough rewritten to ~world~", chunks[0])
	}
}

func TestSendTextPipeline_LongBodyChunksAtLimit(t *testing.T) {
	// Build a body > maxMessageLen to force multi-chunk.
	body := strings.Repeat("a", maxMessageLen+500)
	formatted := markdownToWhatsApp(body)
	chunks := chunkText(formatted, maxMessageLen)

	if len(chunks) < 2 {
		t.Fatalf("len=%d, want ≥2", len(chunks))
	}
	if len(chunks[0]) > maxMessageLen {
		t.Errorf("chunk[0] len=%d exceeds maxMessageLen=%d", len(chunks[0]), maxMessageLen)
	}
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	if total != len(body) {
		t.Errorf("sum chunk len = %d, want %d (no data lost)", total, len(body))
	}
}

func TestSendTextPipeline_ReplyGoesOnFirstChunkOnly(t *testing.T) {
	chatJID := types.NewJID("15550000010", types.DefaultUserServer)
	body := strings.Repeat("x", maxMessageLen+100)
	replyToID := "QUOTED123"

	formatted := markdownToWhatsApp(body)
	chunks := chunkText(formatted, maxMessageLen)

	if len(chunks) < 2 {
		t.Fatalf("setup error: got %d chunks, want ≥2", len(chunks))
	}

	// Mirror the SendText branching: quote only on first chunk.
	for i, chunk := range chunks {
		quoteOnThis := i == 0 && replyToID != ""
		var msg *waE2E.Message
		if quoteOnThis {
			msg = buildTextMessage(chunk, replyToID, chatJID)
		} else {
			msg = buildTextMessage(chunk, "", types.EmptyJID)
		}

		if i == 0 {
			ext := msg.GetExtendedTextMessage()
			if ext == nil {
				t.Errorf("chunk[%d] reply wiring produced non-Extended message", i)
				continue
			}
			if ext.GetContextInfo().GetStanzaID() != replyToID {
				t.Errorf("chunk[%d] stanzaID = %q, want %q", i,
					ext.GetContextInfo().GetStanzaID(), replyToID)
			}
		} else {
			if msg.GetExtendedTextMessage() != nil {
				t.Errorf("chunk[%d] carried a quote; only the first chunk should", i)
			}
		}
	}
}

func TestSendTextPipeline_ReplyEmptyIsPlainConversation(t *testing.T) {
	got := buildTextMessage("plain", "", types.EmptyJID)
	if got.GetConversation() != "plain" {
		t.Errorf("no-reply text = %q, want plain (Conversation, not Extended)", got.GetConversation())
	}
	if got.GetExtendedTextMessage() != nil {
		t.Errorf("no-reply should not allocate ExtendedTextMessage submessage")
	}
}

func TestSendMediaPipeline_CaptionRoundTrips(t *testing.T) {
	// SendMedia converts the caption via markdownToWhatsApp before passing
	// it to buildMediaMessage; the conversion is the same pipeline as
	// text. This test asserts the round-trip — the (already-converted)
	// caption ends up in the media submessage unchanged.
	converted := markdownToWhatsApp("**photo** — see below")
	got := buildMediaMessage(string(whatsmeow.MediaImage), stubUpload(), "image/jpeg", converted)
	if got == nil {
		t.Fatal("buildMediaMessage(image) = nil")
	}
	if got.GetImageMessage().GetCaption() != converted {
		t.Errorf("caption = %q, want %q", got.GetImageMessage().GetCaption(), converted)
	}
}

func TestSendMediaPipeline_EmptyCaptionIsEmpty(t *testing.T) {
	got := buildMediaMessage(string(whatsmeow.MediaImage), stubUpload(), "image/jpeg", "")
	if got == nil {
		t.Fatal("buildMediaMessage(image) = nil")
	}
	if got.GetImageMessage().GetCaption() != "" {
		t.Errorf("empty caption got = %q, want empty", got.GetImageMessage().GetCaption())
	}
}

// stubUpload is a minimal whatsmeow.UploadResponse literal for tests.
func stubUpload() whatsmeow.UploadResponse {
	return whatsmeow.UploadResponse{URL: "https://example/"}
}
