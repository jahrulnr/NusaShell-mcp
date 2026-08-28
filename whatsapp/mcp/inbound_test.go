package main

import (
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Adapted 1:1 from GoClaw (internal/channels/whatsapp/inbound_test.go).
// GoClaw's extractTextContent/extractQuotedText map onto NusaShell's
// textOf (text body), extractQuotedID (reply context) and extractMediaInfo
// (media kind + caption). The translateMessage tests additionally cover the
// normalized-event layer that feeds the ingester.

func mustJID(t *testing.T, s string) types.JID {
	t.Helper()
	jid, err := types.ParseJID(s)
	if err != nil {
		t.Fatalf("parse JID %q: %v", s, err)
	}
	return jid
}

// --- textOf (GoClaw: extractTextContent) ---

func TestTextOf_Nil(t *testing.T) {
	got := textOf(nil)
	if got != "" {
		t.Errorf("textOf(nil) = %q, want empty", got)
	}
}

func TestTextOf_Conversation(t *testing.T) {
	hello := "hello world"
	msg := &waE2E.Message{Conversation: &hello}
	got := textOf(msg)
	if got != "hello world" {
		t.Errorf("textOf(Conversation) = %q, want %q", got, "hello world")
	}
}

func TestTextOf_ExtendedText(t *testing.T) {
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: func() *string { s := "extended message"; return &s }(),
		},
	}
	got := textOf(msg)
	if got != "extended message" {
		t.Errorf("textOf(ExtendedText) = %q, want %q", got, "extended message")
	}
}

func TestTextOf_EmptyMessage(t *testing.T) {
	msg := &waE2E.Message{}
	got := textOf(msg)
	if got != "" {
		t.Errorf("textOf(empty) = %q, want empty", got)
	}
}

// --- extractQuotedID (GoClaw: quoted-text context) ---

func TestExtractQuotedID_Nil(t *testing.T) {
	got := extractQuotedID(nil)
	if got != "" {
		t.Errorf("extractQuotedID(nil) = %q, want empty", got)
	}
}

func TestExtractQuotedID_NoContext(t *testing.T) {
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: func() *string { s := "no reply"; return &s }(),
		},
	}
	got := extractQuotedID(msg)
	if got != "" {
		t.Errorf("extractQuotedID(no context) = %q, want empty", got)
	}
}

func TestExtractQuotedID_WithStanzaID(t *testing.T) {
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: func() *string { s := "my reply"; return &s }(),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID: func() *string { s := "QUOTED123"; return &s }(),
			},
		},
	}
	got := extractQuotedID(msg)
	if got != "QUOTED123" {
		t.Errorf("extractQuotedID = %q, want QUOTED123", got)
	}
}

func TestExtractQuotedID_ConversationHasNoQuote(t *testing.T) {
	msg := &waE2E.Message{Conversation: func() *string { s := "plain"; return &s }()}
	got := extractQuotedID(msg)
	if got != "" {
		t.Errorf("extractQuotedID(Conversation) = %q, want empty", got)
	}
}

// --- extractMediaInfo (GoClaw: caption extraction per media type) ---

func TestExtractMediaInfo_ImageCaption(t *testing.T) {
	msg := &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			Caption:  func() *string { s := "look at this photo"; return &s }(),
			Mimetype: func() *string { s := "image/jpeg"; return &s }(),
		},
	}
	kind, mime, caption := extractMediaInfo(msg)
	if kind != "image" {
		t.Errorf("kind = %q, want image", kind)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	if caption != "look at this photo" {
		t.Errorf("caption = %q, want %q", caption, "look at this photo")
	}
}

func TestExtractMediaInfo_VideoCaption(t *testing.T) {
	msg := &waE2E.Message{
		VideoMessage: &waE2E.VideoMessage{
			Caption:  func() *string { s := "cool video"; return &s }(),
			Mimetype: func() *string { s := "video/mp4"; return &s }(),
		},
	}
	kind, _, caption := extractMediaInfo(msg)
	if kind != "video" {
		t.Errorf("kind = %q, want video", kind)
	}
	if caption != "cool video" {
		t.Errorf("caption = %q, want %q", caption, "cool video")
	}
}

func TestExtractMediaInfo_DocumentCaption(t *testing.T) {
	msg := &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{
			Caption:  func() *string { s := "see this document"; return &s }(),
			Mimetype: func() *string { s := "application/pdf"; return &s }(),
		},
	}
	kind, _, caption := extractMediaInfo(msg)
	if kind != "document" {
		t.Errorf("kind = %q, want document", kind)
	}
	if caption != "see this document" {
		t.Errorf("caption = %q, want %q", caption, "see this document")
	}
}

func TestExtractMediaInfo_AudioVoice(t *testing.T) {
	msg := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			Mimetype: func() *string { s := "audio/ogg"; return &s }(),
			PTT:      new(true),
		},
	}
	kind, mime, _ := extractMediaInfo(msg)
	if kind != "voice" {
		t.Errorf("kind = %q, want voice for PTT audio", kind)
	}
	if mime != "audio/ogg" {
		t.Errorf("mime = %q, want audio/ogg", mime)
	}
}

func TestExtractMediaInfo_AudioNotVoice(t *testing.T) {
	msg := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			Mimetype: func() *string { s := "audio/mpeg"; return &s }(),
		},
	}
	kind, _, _ := extractMediaInfo(msg)
	if kind != "audio" {
		t.Errorf("kind = %q, want audio for non-PTT", kind)
	}
}

func TestExtractMediaInfo_Sticker(t *testing.T) {
	msg := &waE2E.Message{
		StickerMessage: &waE2E.StickerMessage{
			Mimetype: func() *string { s := "image/webp"; return &s }(),
		},
	}
	kind, _, _ := extractMediaInfo(msg)
	if kind != "sticker" {
		t.Errorf("kind = %q, want sticker", kind)
	}
}

func TestExtractMediaInfo_NoMedia(t *testing.T) {
	msg := &waE2E.Message{Conversation: func() *string { s := "just text"; return &s }()}
	kind, mime, caption := extractMediaInfo(msg)
	if kind != "" || mime != "" || caption != "" {
		t.Errorf("extractMediaInfo(text) = (%q,%q,%q), want all empty", kind, mime, caption)
	}
}

// --- translateMessage (normalized-event layer feeding the ingester) ---

// newTestWA returns a WhatsmeowClient with only the event channel wired —
// translateMessage/pushEvent never touch the network or the store.
func newTestWA() *WhatsmeowClient {
	return &WhatsmeowClient{eventCh: make(chan any, 64)}
}

// drainEvents pulls every buffered event without blocking.
func drainEvents(w *WhatsmeowClient) []any {
	var out []any
	for {
		select {
		case ev := <-w.eventCh:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func makeTextEvt(t *testing.T, text, pushName string, fromMe bool) *events.Message {
	t.Helper()
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     mustJID(t, "1234567890@s.whatsapp.net"),
				Sender:   mustJID(t, "0987654321@s.whatsapp.net"),
				IsFromMe: fromMe,
			},
			ID:        "MSG1",
			PushName:  pushName,
			Timestamp: time.Unix(1700000000, 0),
		},
		Message: &waE2E.Message{Conversation: new(text)},
	}
}

func TestTranslateMessage_PlainText(t *testing.T) {
	w := newTestWA()
	w.translateMessage(makeTextEvt(t, "hello world", "", false))

	evs := drainEvents(w)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(evs), evs)
	}
	em, ok := evs[0].(EventMessage)
	if !ok {
		t.Fatalf("event type = %T, want EventMessage", evs[0])
	}
	if em.Text != "hello world" {
		t.Errorf("Text = %q, want %q", em.Text, "hello world")
	}
	if em.ID != "MSG1" {
		t.Errorf("ID = %q, want MSG1", em.ID)
	}
	if em.FromMe {
		t.Error("FromMe = true, want false")
	}
	if em.ChatJID != "1234567890@s.whatsapp.net" {
		t.Errorf("ChatJID = %q, want DM chat", em.ChatJID)
	}
}

func TestTranslateMessage_PushNameUpsertsContact(t *testing.T) {
	w := newTestWA()
	w.translateMessage(makeTextEvt(t, "hi", "Alice", false))

	evs := drainEvents(w)
	if len(evs) != 2 {
		t.Fatalf("expected 2 events (message + contact), got %d: %v", len(evs), evs)
	}
	if _, ok := evs[0].(EventMessage); !ok {
		t.Errorf("first event type = %T, want EventMessage", evs[0])
	}
	ec, ok := evs[1].(EventContact)
	if !ok {
		t.Fatalf("second event type = %T, want EventContact", evs[1])
	}
	if ec.PushName != "Alice" {
		t.Errorf("PushName = %q, want Alice", ec.PushName)
	}
}

func TestTranslateMessage_FromMeNoContact(t *testing.T) {
	w := newTestWA()
	// from_me with a push name must NOT emit a contact upsert.
	w.translateMessage(makeTextEvt(t, "my own msg", "Me", true))

	evs := drainEvents(w)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event (message only), got %d: %v", len(evs), evs)
	}
	if _, ok := evs[0].(EventMessage); !ok {
		t.Errorf("event type = %T, want EventMessage", evs[0])
	}
}

func TestTranslateMessage_QuoteSetsQuotedID(t *testing.T) {
	w := newTestWA()
	evt := makeTextEvt(t, "my reply", "", false)
	evt.Message = &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: func() *string { s := "my reply"; return &s }(),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID: func() *string { s := "ORIG42"; return &s }(),
			},
		},
	}
	w.translateMessage(evt)

	evs := drainEvents(w)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	em, ok := evs[0].(EventMessage)
	if !ok {
		t.Fatalf("event type = %T, want EventMessage", evs[0])
	}
	if em.QuotedID != "ORIG42" {
		t.Errorf("QuotedID = %q, want ORIG42", em.QuotedID)
	}
}

func TestTranslateMessage_ImageMedia(t *testing.T) {
	w := newTestWA()
	evt := makeTextEvt(t, "", "", false)
	evt.Message = &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			Caption:    func() *string { s := "a photo"; return &s }(),
			Mimetype:   func() *string { s := "image/jpeg"; return &s }(),
			FileLength: new(uint64(1234)),
		},
	}
	w.translateMessage(evt)

	evs := drainEvents(w)
	if len(evs) != 1 {
		t.Fatalf("expected 1 media event, got %d: %v", len(evs), evs)
	}
	em, ok := evs[0].(EventMedia)
	if !ok {
		t.Fatalf("event type = %T, want EventMedia", evs[0])
	}
	if em.Kind != "image" {
		t.Errorf("Kind = %q, want image", em.Kind)
	}
	if em.Caption != "a photo" {
		t.Errorf("Caption = %q, want %q", em.Caption, "a photo")
	}
	if em.DownloadRef == "" {
		t.Error("DownloadRef empty, want serialized message ref")
	}
}

func TestTranslateMessage_Reaction(t *testing.T) {
	w := newTestWA()
	evt := makeTextEvt(t, "", "", false)
	evt.Message = &waE2E.Message{
		ReactionMessage: &waE2E.ReactionMessage{
			Key:  &waCommon.MessageKey{ID: func() *string { s := "TARGET9"; return &s }()},
			Text: func() *string { s := "👍"; return &s }(),
		},
	}
	w.translateMessage(evt)

	evs := drainEvents(w)
	if len(evs) != 1 {
		t.Fatalf("expected 1 reaction event, got %d", len(evs))
	}
	er, ok := evs[0].(EventReaction)
	if !ok {
		t.Fatalf("event type = %T, want EventReaction", evs[0])
	}
	if er.TargetID != "TARGET9" {
		t.Errorf("TargetID = %q, want TARGET9", er.TargetID)
	}
	if er.Emoji != "👍" {
		t.Errorf("Emoji = %q, want 👍", er.Emoji)
	}
}

func TestTranslateMessage_Edit(t *testing.T) {
	w := newTestWA()
	evt := makeTextEvt(t, "", "", false)
	evt.Message = &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			Key:  &waCommon.MessageKey{ID: func() *string { s := "TARGET9"; return &s }()},
			EditedMessage: &waE2E.Message{
				Conversation: func() *string { s := "edited text"; return &s }(),
			},
		},
	}
	w.translateMessage(evt)

	evs := drainEvents(w)
	if len(evs) != 1 {
		t.Fatalf("expected 1 edit event, got %d", len(evs))
	}
	ee, ok := evs[0].(EventEdit)
	if !ok {
		t.Fatalf("event type = %T, want EventEdit", evs[0])
	}
	if ee.NewText != "edited text" {
		t.Errorf("NewText = %q, want %q", ee.NewText, "edited text")
	}
	if ee.ID != "TARGET9" {
		t.Errorf("ID = %q, want TARGET9", ee.ID)
	}
}

func TestTranslateMessage_Revoke(t *testing.T) {
	w := newTestWA()
	evt := makeTextEvt(t, "", "", false)
	evt.Message = &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_REVOKE.Enum(),
			Key:  &waCommon.MessageKey{ID: func() *string { s := "TARGET9"; return &s }()},
		},
	}
	w.translateMessage(evt)

	evs := drainEvents(w)
	if len(evs) != 1 {
		t.Fatalf("expected 1 delete event, got %d", len(evs))
	}
	ed, ok := evs[0].(EventDelete)
	if !ok {
		t.Fatalf("event type = %T, want EventDelete", evs[0])
	}
	if ed.ID != "TARGET9" {
		t.Errorf("ID = %q, want TARGET9", ed.ID)
	}
}

func TestTranslateMessage_NilMessageIgnored(t *testing.T) {
	w := newTestWA()
	evt := makeTextEvt(t, "", "", false)
	evt.Message = nil
	w.translateMessage(evt)

	if evs := drainEvents(w); len(evs) != 0 {
		t.Errorf("expected 0 events for nil message, got %d", len(evs))
	}
}

func TestTranslateMessage_EmptyTextIgnored(t *testing.T) {
	w := newTestWA()
	w.translateMessage(makeTextEvt(t, "", "", false))

	if evs := drainEvents(w); len(evs) != 0 {
		t.Errorf("expected 0 events for empty text, got %d", len(evs))
	}
}
