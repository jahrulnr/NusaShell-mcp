package main

import (
	"context"
	"errors"
	"time"
)

// ErrNotConnected is returned by Client methods when the WhatsApp socket is
// not connected. Tool handlers detect it via errors.Is to flag retryable
// errors so the LLM knows the call can be retried.
var ErrNotConnected = errors.New("whatsapp not connected")

// ErrNotPaired is returned when no WhatsApp account is linked.
var ErrNotPaired = errors.New("whatsapp not paired — run login first")

// SendResult is what every successful send returns.
type SendResult struct {
	MessageID string
	Timestamp time.Time
}

// DownloadResult is the decrypted media payload + metadata.
type DownloadResult struct {
	Bytes    []byte
	MimeType string
	SHA256   []byte
}

// QRCode is a QR login event delivered to the UI.
type QRCode struct {
	Code      string    // the QR string to render
	ExpiresAt time.Time // when the QR expires
}

// PairState describes the current linking state for the status/login tools.
type PairState struct {
	Paired     bool   // true if a WhatsApp account is linked
	Connected  bool   // true if the socket is currently connected
	DeviceJID  string // linked device JID (empty if not paired)
	AwaitingQR bool   // true if a QR login flow is in progress
}

// Client is the only WhatsApp-touching interface in this plugin.
// The production implementation wraps whatsmeow; tests can substitute a fake.
type Client interface {
	// Events returns a channel that receives normalized events until ctx is
	// cancelled. The ingester drains this channel.
	Events(ctx context.Context) <-chan any

	// State reports the current pairing/connection state.
	State() PairState

	// StartQR begins a QR login flow. Returns a channel of QR codes (the QR
	// rotates periodically until scanned or expired). The caller (login tool
	// or UI) renders each QR. A successful scan is signaled by State().Paired
	// becoming true and the events channel emitting a PairSuccess-equivalent.
	StartQR(ctx context.Context) (<-chan QRCode, error)

	// Connect brings the WhatsApp socket up using stored credentials. If no
	// credentials exist, returns ErrNotPaired so the caller can start QR login.
	Connect(ctx context.Context) error

	// Disconnect closes the WhatsApp socket cleanly.
	Disconnect()

	// Logout disconnects and clears stored credentials.
	Logout(ctx context.Context) error

	// SendText sends a text message; replyToID may be empty for no quote.
	SendText(ctx context.Context, chatJID, text, replyToID string) (SendResult, error)

	// SendMedia uploads bytes and sends a media message. Kind is the whatsmeow
	// MediaType ("image", "video", "document", "audio").
	SendMedia(ctx context.Context, chatJID, kind string, data []byte, mimeType, caption, replyToID string) (SendResult, error)

	// React adds (or removes, if emoji is "") a reaction on a target message.
	React(ctx context.Context, chatJID, messageID, emoji string) error

	// MarkRead marks a chat read up to upToMessageID (or latest if empty).
	MarkRead(ctx context.Context, chatJID, upToMessageID string) error

	// Download fetches an encrypted media blob given the opaque download
	// reference recorded in the media table. The implementation decodes the ref.
	Download(ctx context.Context, downloadRef string) (DownloadResult, error)

	// RequestSync asks WhatsApp to backfill history for a chat. We build a
	// PEER_DATA_OPERATION_REQUEST (history sync on demand) anchored at the
	// newest message we already have, so the server backfills older messages
	// from that point. If we have no stored message for the chat, we send an
	// empty anchor with the current time — the server treats that as
	// "give me the most recent messages".
	//
	// The request is sent to our own JID; legacy clients route it to the
	// phone, which pushes the matching HistorySync blob back.
	RequestSync(ctx context.Context, chatJID string) error
}
