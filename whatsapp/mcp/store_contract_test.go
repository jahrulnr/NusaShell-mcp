package main

import (
	"context"
	"reflect"
	"testing"
)

func TestEmptyReadSlicesAreJSONArrays(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	checks := []struct {
		name string
		got  any
	}{
		{"chats", func() any { v, _ := store.ListChats(ctx, "", 50, 0); return v }()},
		{"participants", func() any { v, _ := store.GetGroupParticipants(ctx, "missing@g.us"); return v }()},
		{"contacts", func() any { v, _ := store.ListContacts(ctx, "", 50); return v }()},
		{"groups", func() any { v, _ := store.ListGroups(ctx, "", 50); return v }()},
		{"messages", func() any { v, _ := store.GetMessages(ctx, "missing@s.whatsapp.net", 0, 50); return v }()},
		{"search", func() any { v, _ := store.SearchMessages(ctx, "missing", "", "", 0, 0, 50); return v }()},
		{"reactions", func() any { v, _ := store.GetReactions(ctx, "missing@s.whatsapp.net", "missing"); return v }()},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			rv := reflect.ValueOf(check.got)
			if !rv.IsValid() || (rv.Kind() == reflect.Slice && rv.IsNil()) {
				t.Fatalf("%s returned nil slice, want empty non-nil slice", check.name)
			}
		})
	}
}
