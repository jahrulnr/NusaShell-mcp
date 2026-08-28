// Ported/extended from GoClaw (internal/channels/whatsapp).
//
// Covers three whatsmeow event types the live event handler did not
// previously route: events.LoggedOut (P0-1), events.HistorySync (P0-2),
// and LID addressing normalization in translateMessage (P0-4).
package main

import (
	"context"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// --- LoggedOut (P0-1) ---

func TestLoggedOutEventResetsStateAndDeletesDevice(t *testing.T) {
	_, container := newAuthTestDB(t)
	saveWhatsAppTestDevice(t, container, "15551112222")
	w := wireTestClient(t, container)
	w.newClient()

	// Simulate "previously connected and paired".
	w.mu.Lock()
	w.state = PairState{
		Paired:    true,
		Connected: true,
		DeviceJID: "15551112222@s.whatsapp.net",
	}
	w.mu.Unlock()

	// whatsmeow emits this when the phone removes the linked device.
	w.handleEvent(&events.LoggedOut{OnConnect: false})

	// State must be fully reset.
	if s := w.State(); s.Paired || s.Connected || s.AwaitingQR || s.DeviceJID != "" {
		t.Errorf("post-LoggedOut state = %+v, want zero PairState", s)
	}
	if w.client != nil {
		t.Error("post-LoggedOut client != nil, want nil so the next StartQR rebuilds")
	}
	// The handler deletes the stored device so the next StartQR cannot
	// accidentally reuse a dead session. A new device is allocated by
	// initStore/StartQR on the next login attempt; we don't pre-create it
	// here.
	if w.device != nil && !w.device.Deleted {
		t.Errorf("post-LoggedOut device still live: %+v, want deleted", w.device)
	}

	// Calling initStore after the LoggedOut must yield a fresh unpaired
	// device, confirming the dead-session guard works end-to-end.
	if err := w.initStore(context.Background()); err != nil {
		t.Fatalf("post-LoggedOut initStore() error = %v", err)
	}
	if w.device == nil {
		t.Fatal("post-LoggedOut initStore() device = nil")
	}
	if w.device.ID != nil {
		t.Errorf("post-LoggedOut initStore() device.ID = %v, want nil (unpaired)", w.device.ID)
	}

	// And the boundary event must reach the ingester channel so UIs can
	// surface "re-link required" without polling.
	select {
	case ev := <-w.eventCh:
		logout, ok := ev.(EventLoggedOut)
		if !ok {
			t.Fatalf("eventCh first event = %T, want EventLoggedOut", ev)
		}
		if logout.Reason == "" {
			t.Errorf("EventLoggedOut.Reason = empty, want something from ConnectFailureReason")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventLoggedOut on eventCh")
	}
}

func TestLoggedOutFromUnpairedStateIsNoop(t *testing.T) {
	_, container := newAuthTestDB(t)
	w := wireTestClient(t, container)
	// No device saved — LoggedOut during an unpaired attempt must not panic.
	w.handleEvent(&events.LoggedOut{OnConnect: true, Reason: 401})
	// State stays empty; nothing pushed.
	if s := w.State(); s.Paired || s.Connected {
		t.Errorf("state = %+v, want zero", s)
	}
}

// --- HistorySync (P0-2) ---

func makeHistorySync(t *testing.T, convs ...*waHistorySync.Conversation) *events.HistorySync {
	t.Helper()
	hs := &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		Conversations: convs,
	}
	return &events.HistorySync{Data: hs}
}

func webMessageInfo(id, text, remoteJID, participant string, fromMe bool) *waWeb.WebMessageInfo {
	wmi := &waWeb.WebMessageInfo{
		Key: &waCommon.MessageKey{
			RemoteJID: proto.String(remoteJID),
			ID:        proto.String(id),
			FromMe:    proto.Bool(fromMe),
		},
		Message:          &waE2E.Message{Conversation: proto.String(text)},
		MessageTimestamp: proto.Uint64(uint64(time.Now().Unix())),
	}
	if participant != "" {
		// For group messages, whatsmeow needs the sender on either the
		// top-level WebMessageInfo or the key (it checks both).
		wmi.Key.Participant = proto.String(participant)
	}
	return wmi
}

func TestHistorySyncTranslatesConversationsToEvents(t *testing.T) {
	_, container := newAuthTestDB(t)
	w := wireTestClient(t, container)
	// A client is required to call ParseWebMessage. Build one without
	// calling Connect (no network).
	w.newClient()

	dmChat := types.NewJID("15550000001", types.DefaultUserServer)
	dmConv := &waHistorySync.Conversation{
		ID:       proto.String(dmChat.String()),
		Messages: []*waHistorySync.HistorySyncMsg{{Message: webMessageInfo("HS1", "backfilled dm", dmChat.String(), "", false)}},
	}

	groupJID := types.NewJID("120363000000000001", types.GroupServer)
	groupSender := types.NewJID("15550000042", types.DefaultUserServer)
	groupConv := &waHistorySync.Conversation{
		ID:   proto.String(groupJID.String()),
		Name: proto.String("Test Group"),
		Messages: []*waHistorySync.HistorySyncMsg{
			{Message: webMessageInfo("HS2", "group message 1", groupJID.String(), groupSender.String(), false)},
		},
	}

	hs := makeHistorySync(t, dmConv, groupConv)
	w.handleEvent(hs)

	// Expect: EventMessage for the DM, EventMessage for the group message.
	// Order: ParseWebMessage→translateMessage for each msg in declaration order.
	var got []EventMessage
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-w.eventCh:
			if m, ok := ev.(EventMessage); ok {
				got = append(got, m)
			}
		case <-timeout:
			t.Fatalf("timed out — got %d EventMessage, want 2. seen: %#v", len(got), got)
		}
	}

	if got[0].ChatJID != dmChat.String() || got[0].Text != "backfilled dm" || got[0].ID != "HS1" {
		t.Errorf("event[0] = %+v, want DM backfill", got[0])
	}
	if got[1].ChatJID != groupJID.String() || got[1].Text != "group message 1" || got[1].ID != "HS2" {
		t.Errorf("event[1] = %+v, want group backfill", got[1])
	}
}

func TestHistorySyncEmitsPushnamesAsContactEvents(t *testing.T) {
	_, container := newAuthTestDB(t)
	w := wireTestClient(t, container)
	w.newClient()

	hs := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		Pushnames: []*waHistorySync.Pushname{
			{ID: proto.String("15550000099@s.whatsapp.net"), Pushname: proto.String("Alice")},
			{ID: proto.String("15550000088@s.whatsapp.net"), Pushname: proto.String("Bob")},
		},
	}
	w.handleEvent(&events.HistorySync{Data: hs})

	got := map[string]string{}
	timeout := time.After(time.Second)
	for len(got) < 2 {
		select {
		case ev := <-w.eventCh:
			if c, ok := ev.(EventContact); ok {
				got[c.JID] = c.PushName
			}
		case <-timeout:
			t.Fatalf("timed out — got %d contacts, want 2. map=%+v", len(got), got)
		}
	}
	if got["15550000099@s.whatsapp.net"] != "Alice" {
		t.Errorf("Alice pushname = %q, want Alice", got["15550000099@s.whatsapp.net"])
	}
	if got["15550000088@s.whatsapp.net"] != "Bob" {
		t.Errorf("Bob pushname = %q, want Bob", got["15550000088@s.whatsapp.net"])
	}
}

func TestHistorySyncSkipsConversationsWithBadJID(t *testing.T) {
	_, container := newAuthTestDB(t)
	w := wireTestClient(t, container)
	w.newClient()

	bad := &waHistorySync.Conversation{
		ID:       proto.String("not a valid jid @@@"),
		Messages: []*waHistorySync.HistorySyncMsg{{Message: webMessageInfo("BAD", "x", "y", "", false)}},
	}
	good := &waHistorySync.Conversation{
		ID: proto.String("15550000077@s.whatsapp.net"),
		Messages: []*waHistorySync.HistorySyncMsg{
			{Message: webMessageInfo("OK1", "hello", "15550000077@s.whatsapp.net", "", false)},
		},
	}
	w.handleEvent(makeHistorySync(t, bad, good))

	select {
	case ev := <-w.eventCh:
		if m, ok := ev.(EventMessage); !ok || m.ID != "OK1" {
			t.Errorf("first event = %#v, want EventMessage ID=OK1", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the good conversation's message")
	}
	// And there should be no second event from the bad JID.
	select {
	case ev := <-w.eventCh:
		t.Errorf("unexpected second event from bad JID: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHistorySyncNilDataIsNoop(t *testing.T) {
	_, container := newAuthTestDB(t)
	w := wireTestClient(t, container)
	w.handleEvent(&events.HistorySync{Data: nil})
	// No events, no panic.
	select {
	case ev := <-w.eventCh:
		t.Errorf("unexpected event: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

// --- LID normalization (P0-4) ---

func TestTranslateMessageNormalizesLIDSenderToPhoneJID(t *testing.T) {
	_, container := newAuthTestDB(t)
	w := wireTestClient(t, container)

	phone := types.NewJID("15550000055", types.DefaultUserServer)
	lid := types.NewJID("12345", types.HostedLIDServer)
	chat := types.NewJID("120363000000000010", types.GroupServer)

	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:           chat,
				Sender:         lid,
				SenderAlt:      phone,
				AddressingMode: types.AddressingModeLID,
				IsFromMe:       false,
			},
			ID:        "LID1",
			Timestamp: time.Now(),
			PushName:  "Carol",
		},
		Message: &waE2E.Message{Conversation: proto.String("hi from LID")},
	}
	w.translateMessage(evt)

	select {
	case raw := <-w.eventCh:
		// First event is EventMessage, second is EventContact (push name).
		// We assert the EventMessage specifically.
		m, ok := raw.(EventMessage)
		if !ok {
			t.Fatalf("first event = %T, want EventMessage", raw)
		}
		if m.SenderJID != phone.String() {
			t.Errorf("SenderJID = %q, want %q (LID normalized to phone)", m.SenderJID, phone.String())
		}
		if m.ChatJID != chat.String() {
			t.Errorf("ChatJID = %q, want %q (chat JID untouched)", m.ChatJID, chat.String())
		}
		// Drain the EventContact for cleanliness.
		select {
		case <-w.eventCh:
		case <-time.After(50 * time.Millisecond):
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normalized EventMessage")
	}
}

func TestTranslateMessagePreservesPhoneSenderJID(t *testing.T) {
	_, container := newAuthTestDB(t)
	w := wireTestClient(t, container)

	phone := types.NewJID("15550000066", types.DefaultUserServer)
	chat := types.NewJID("15550000066", types.DefaultUserServer)
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:           chat,
				Sender:         phone,
				AddressingMode: types.AddressingModePN,
				IsFromMe:       false,
			},
			ID:        "PN1",
			Timestamp: time.Now(),
		},
		Message: &waE2E.Message{Conversation: proto.String("plain pn")},
	}
	w.translateMessage(evt)

	select {
	case raw := <-w.eventCh:
		m, ok := raw.(EventMessage)
		if !ok {
			t.Fatalf("first event = %T, want EventMessage", raw)
		}
		if m.SenderJID != phone.String() {
			t.Errorf("SenderJID = %q, want %q (PN untouched)", m.SenderJID, phone.String())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

// --- sanity: compile-time guards for the generated message types we
// re-alias in this file (keeps go vet quiet without polluting the import
// block when the type names collide with the other test files). ---

// keep the unused-import detector happy if a future test stops using these.
var (
	_ = events.PairSuccess{}
)
