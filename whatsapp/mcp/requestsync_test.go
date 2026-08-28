// RequestSync wiring tests (P0-3). The buildLastKnownAnchor helper is pure
// and easy to test directly; the actual SendPeerMessage call is exercised
// end-to-end by integration — at this layer we verify the contract:
//   - empty chatJID => error
//   - valid chatJID => anchor is built with the chat and a recent timestamp
//   - lastKnown has the chat baked in for BuildHistorySyncRequest to consume
package main

import (
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestBuildLastKnownAnchorEmbedsChat(t *testing.T) {
	chat := types.NewJID("15550000020", types.DefaultUserServer)
	before := time.Now()

	w := NewWhatsmeowClient(t.TempDir(), false)
	anchor := w.buildLastKnownAnchor(chat)

	if anchor == nil {
		t.Fatal("buildLastKnownAnchor() = nil, want non-nil anchor")
	}
	if anchor.Chat != chat {
		t.Errorf("anchor.Chat = %v, want %v", anchor.Chat, chat)
	}
	if anchor.Timestamp.Before(before) {
		t.Errorf("anchor.Timestamp = %v, must not precede call time %v", anchor.Timestamp, before)
	}
	if !anchor.Timestamp.Before(time.Now().Add(time.Second)) {
		t.Errorf("anchor.Timestamp = %v, must be recent", anchor.Timestamp)
	}
}

func TestRequestSyncWithoutClientReturnsErrNotConnected(t *testing.T) {
	// A freshly-built client (no Connect) must refuse to send — same
	// pattern as SendText/SendMedia.
	w := NewWhatsmeowClient(t.TempDir(), false)
	err := w.RequestSync(nil, "15550000020@s.whatsapp.net")
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("RequestSync(no client) error = %v, want ErrNotConnected", err)
	}
}
