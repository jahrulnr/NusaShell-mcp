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
		{"messages", func() any { v, _ := store.GetMessages(ctx, "missing", 50, 0); return v }()},
		{"search", func() any { v, _ := store.SearchMessages(ctx, "missing"); return v }()},
		{"approvals", func() any { v, _ := store.ListPendingApprovals(ctx); return v }()},
		{"allowlist", func() any { v, _ := store.ListAllowlist(ctx); return v }()},
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
