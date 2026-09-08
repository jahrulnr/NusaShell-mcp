package main

import (
	"context"
	"testing"
	"time"
)

func TestApplyGroupParticipantDeltaPreservesUnaffectedRoster(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	const groupJID = "120363000000000001@g.us"
	if err := store.UpsertGroup(ctx, groupJID, "Engineering", "", "", 1); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}
	if err := store.SetGroupParticipants(ctx, groupJID, []EventGroupParticipant{
		{JID: "alice@s.whatsapp.net", JoinedAt: time.Unix(10, 0)},
		{JID: "bob@s.whatsapp.net", IsAdmin: true, JoinedAt: time.Unix(20, 0)},
	}); err != nil {
		t.Fatalf("SetGroupParticipants: %v", err)
	}

	if err := store.ApplyGroupParticipantDelta(ctx, groupJID,
		[]EventGroupParticipant{{JID: "carol@s.whatsapp.net", JoinedAt: time.Unix(30, 0)}},
		[]string{"alice@s.whatsapp.net"},
		[]string{"carol@s.whatsapp.net"},
		[]string{"bob@s.whatsapp.net"},
	); err != nil {
		t.Fatalf("ApplyGroupParticipantDelta: %v", err)
	}

	participants, err := store.GetGroupParticipants(ctx, groupJID)
	if err != nil {
		t.Fatalf("GetGroupParticipants: %v", err)
	}
	if len(participants) != 2 {
		t.Fatalf("GetGroupParticipants returned %d rows, want 2: %+v", len(participants), participants)
	}
	byJID := make(map[string]GroupParticipantRow, len(participants))
	for _, participant := range participants {
		byJID[participant.JID] = participant
	}
	if _, ok := byJID["alice@s.whatsapp.net"]; ok {
		t.Error("alice remained after leave delta")
	}
	if got := byJID["bob@s.whatsapp.net"]; got.IsAdmin || got.JoinedAt != 20 {
		t.Errorf("bob = %+v, want existing joined_at=20 and demoted", got)
	}
	if got := byJID["carol@s.whatsapp.net"]; !got.IsAdmin || got.JoinedAt != 30 {
		t.Errorf("carol = %+v, want joined_at=30 and promoted", got)
	}
}

func TestIngesterGroupDeltaDoesNotReplaceExistingRoster(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	const groupJID = "120363000000000002@g.us"
	if err := store.UpsertGroup(ctx, groupJID, "Engineering", "", "", 1); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}
	if err := store.SetGroupParticipants(ctx, groupJID, []EventGroupParticipant{{JID: "alice@s.whatsapp.net", JoinedAt: time.Unix(10, 0)}}); err != nil {
		t.Fatalf("SetGroupParticipants: %v", err)
	}

	NewIngester(store).handleGroupInfo(ctx, EventGroupInfo{
		JID:       groupJID,
		UpdatedAt: time.Unix(20, 0),
		Joined:    []EventGroupParticipant{{JID: "bob@s.whatsapp.net", JoinedAt: time.Unix(20, 0)}},
	})

	participants, err := store.GetGroupParticipants(ctx, groupJID)
	if err != nil {
		t.Fatalf("GetGroupParticipants: %v", err)
	}
	if len(participants) != 2 {
		t.Fatalf("participants = %+v, want existing alice plus joined bob", participants)
	}
}
