package main

import (
	"context"
	"testing"
)

func TestUpsertGroupKeepsKnownMetadataWhenDeltaOmitsIt(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	const jid = "120363000000000001@g.us"
	if err := store.UpsertGroup(ctx, jid, "Engineering", "Release planning", "owner@s.whatsapp.net", 100); err != nil {
		t.Fatalf("initial UpsertGroup: %v", err)
	}
	if err := store.UpsertGroup(ctx, jid, "", "", "", 200); err != nil {
		t.Fatalf("delta UpsertGroup: %v", err)
	}

	groups, err := store.ListGroups(ctx, jid, 1)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("ListGroups returned %d rows, want 1", len(groups))
	}
	got := groups[0]
	if got.Name != "Engineering" || got.Topic != "Release planning" || got.OwnerJID != "owner@s.whatsapp.net" || got.UpdatedAt != 200 {
		t.Errorf("group = %+v, want preserved metadata and updated_at=200", got)
	}
}
