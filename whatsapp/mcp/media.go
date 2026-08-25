package main

import (
	"encoding/base64"
	"strings"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// buildMediaMessage constructs a waE2E.Message for the given media kind
// using an already-uploaded attachment. Returns nil for unsupported kinds.
func buildMediaMessage(kind string, uploaded whatsmeow.UploadResponse, mimeType, caption string) *waE2E.Message {
	switch whatsmeow.MediaType(kind) {
	case whatsmeow.MediaImage:
		return &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(mimeType),
				Caption:       proto.String(caption),
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uploaded.FileLength),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
			},
		}
	case whatsmeow.MediaVideo:
		return &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(mimeType),
				Caption:       proto.String(caption),
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uploaded.FileLength),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
			},
		}
	case whatsmeow.MediaAudio:
		return &waE2E.Message{
			AudioMessage: &waE2E.AudioMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(mimeType),
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uploaded.FileLength),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
			},
		}
	case whatsmeow.MediaDocument:
		return &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(mimeType),
				Title:         proto.String(caption),
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uploaded.FileLength),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
			},
		}
	default:
		return nil
	}
}

// extractMediaInfo determines the kind, MIME type, and caption from a
// whatsmeow Message event by checking which media submessage is present.
func extractMediaInfo(msg *waE2E.Message) (kind, mimeType, caption string) {
	switch {
	case msg.GetImageMessage() != nil:
		kind = "image"
		mimeType = msg.GetImageMessage().GetMimetype()
		caption = msg.GetImageMessage().GetCaption()
	case msg.GetVideoMessage() != nil:
		kind = "video"
		mimeType = msg.GetVideoMessage().GetMimetype()
		caption = msg.GetVideoMessage().GetCaption()
	case msg.GetAudioMessage() != nil:
		kind = "audio"
		mimeType = msg.GetAudioMessage().GetMimetype()
		if msg.GetAudioMessage().GetPTT() {
			kind = "voice"
		}
	case msg.GetDocumentMessage() != nil:
		kind = "document"
		mimeType = msg.GetDocumentMessage().GetMimetype()
		caption = msg.GetDocumentMessage().GetCaption()
	case msg.GetStickerMessage() != nil:
		kind = "sticker"
		mimeType = msg.GetStickerMessage().GetMimetype()
	default:
		return "", "", ""
	}
	return kind, mimeType, caption
}

// mediaMeta extracts mime/size/dimensions/duration from any media submessage.
func mediaMeta(msg *waE2E.Message) (mime string, size int64, width, height, duration int) {
	if m := msg.GetImageMessage(); m != nil {
		return m.GetMimetype(), int64(m.GetFileLength()), int(m.GetWidth()), int(m.GetHeight()), 0
	}
	if m := msg.GetVideoMessage(); m != nil {
		return m.GetMimetype(), int64(m.GetFileLength()), int(m.GetWidth()), int(m.GetHeight()), int(m.GetSeconds())
	}
	if m := msg.GetAudioMessage(); m != nil {
		return m.GetMimetype(), int64(m.GetFileLength()), 0, 0, int(m.GetSeconds())
	}
	if m := msg.GetDocumentMessage(); m != nil {
		return m.GetMimetype(), int64(m.GetFileLength()), 0, 0, 0
	}
	if m := msg.GetStickerMessage(); m != nil {
		return m.GetMimetype(), int64(m.GetFileLength()), int(m.GetWidth()), int(m.GetHeight()), 0
	}
	return "", 0, 0, 0, 0
}

// extractMIME recovers the MIME type from a download-ref parent message
// (used by Download to populate DownloadResult.MimeType).
func extractMIME(msg *waE2E.Message) string {
	if m := msg.GetImageMessage(); m != nil {
		return m.GetMimetype()
	}
	if m := msg.GetVideoMessage(); m != nil {
		return m.GetMimetype()
	}
	if m := msg.GetAudioMessage(); m != nil {
		return m.GetMimetype()
	}
	if m := msg.GetDocumentMessage(); m != nil {
		return m.GetMimetype()
	}
	if m := msg.GetStickerMessage(); m != nil {
		return m.GetMimetype()
	}
	return "application/octet-stream"
}

// textOf extracts a plain-text body from a Message, considering both
// Conversation and ExtendedTextMessage variants.
func textOf(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if c := msg.GetConversation(); c != "" {
		return c
	}
	if et := msg.GetExtendedTextMessage(); et != nil {
		return et.GetText()
	}
	return ""
}

// encodeDownloadRef serializes raw protobuf bytes to a base64 string for
// storage in the media table.
func encodeDownloadRef(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

// decodeDownloadRef reverses encodeDownloadRef.
func decodeDownloadRef(ref string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(ref)
}

// inferMediaKind infers the whatsmeow MediaType from a MIME type or file
// extension. Used by send_media when the caller doesn't specify a kind.
func inferMediaKind(mimeType, filename string) string {
	mt := strings.ToLower(mimeType)
	fn := strings.ToLower(filename)

	if strings.HasPrefix(mt, "image/") || hasExt(fn, ".jpg", ".jpeg", ".png", ".webp", ".gif") {
		return string(whatsmeow.MediaImage)
	}
	if strings.HasPrefix(mt, "video/") || hasExt(fn, ".mp4", ".mov", ".avi", ".mkv", ".webm") {
		return string(whatsmeow.MediaVideo)
	}
	if strings.HasPrefix(mt, "audio/") || hasExt(fn, ".mp3", ".ogg", ".m4a", ".aac", ".wav", ".opus") {
		return string(whatsmeow.MediaAudio)
	}
	return string(whatsmeow.MediaDocument)
}

func hasExt(fn string, exts ...string) bool {
	for _, e := range exts {
		if strings.HasSuffix(fn, e) {
			return true
		}
	}
	return false
}

// formatJIDLabel returns a human-readable label for a JID for display.
func formatJIDLabel(jid, pushName string) string {
	if pushName != "" {
		return pushName
	}
	if idx := strings.Index(jid, "@"); idx > 0 {
		return jid[:idx]
	}
	return jid
}

// chatKindFromJID returns "group" for group JIDs, "dm" otherwise.
func chatKindFromJID(jid string) string {
	if strings.HasSuffix(jid, "@g.us") {
		return "group"
	}
	if strings.HasSuffix(jid, "@newsletter") {
		return "channel"
	}
	return "dm"
}

// buildTextMessage constructs a waE2E.Message for a text send, with optional
// reply context. Uses ExtendedTextMessage for replies (so ContextInfo can be
// set), Conversation for plain text.
func buildTextMessage(text, replyToID string, replyToSender types.JID) *waE2E.Message {
	if replyToID == "" {
		return &waE2E.Message{Conversation: proto.String(text)}
	}
	return &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:    proto.String(replyToID),
				Participant: proto.String(replyToSender.String()),
				QuotedMessage: &waE2E.Message{
					Conversation: proto.String(""),
				},
			},
		},
	}
}

// extractQuotedID pulls the quoted message ID from a message event.
func extractQuotedID(msg *waE2E.Message) string {
	if et := msg.GetExtendedTextMessage(); et != nil {
		if ci := et.GetContextInfo(); ci != nil {
			return ci.GetStanzaID()
		}
	}
	return ""
}

// unused guard — events import is used in whatsmeow.go
var _ = events.Message{}
