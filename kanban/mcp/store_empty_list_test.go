package main

import (
	"encoding/json"
	"testing"
)

// TestListFuncsReturnNonNilSliceOnEmpty guards against regressing to
// `var out []T`, which returns a nil slice when empty. A nil slice
// marshals to `structuredContent: null` in the MCP result; the plugin UI
// client then returns the whole envelope object as the array, crashing
// React with "X is not iterable". Empty lists must marshal to `[]`.
func TestListFuncsReturnNonNilSliceOnEmpty(t *testing.T) {
	s := NewStore("") // no file → empty store, Load not called

	// GetColumns on a project with no columns.
	cols := s.GetColumns("nonexistent-project")
	if cols == nil {
		t.Fatal("GetColumns returned nil slice; want non-nil empty slice so structuredContent marshals to []")
	}
	if len(cols) != 0 {
		t.Errorf("GetColumns len = %d, want 0", len(cols))
	}
	if mustJSON(cols) != "[]" {
		t.Errorf("GetColumns JSON = %s, want []", mustJSON(cols))
	}

	// ListTickets with no tickets.
	tix := s.ListTickets(TicketFilters{})
	if tix == nil {
		t.Fatal("ListTickets returned nil slice; want non-nil empty slice")
	}
	if len(tix) != 0 {
		t.Errorf("ListTickets len = %d, want 0", len(tix))
	}
	if mustJSON(tix) != "[]" {
		t.Errorf("ListTickets JSON = %s, want []", mustJSON(tix))
	}

	// GetSubtasks for a ticket with no children.
	subs := s.GetSubtasks("nonexistent-ticket")
	if subs == nil {
		t.Fatal("GetSubtasks returned nil slice; want non-nil empty slice")
	}
	if len(subs) != 0 {
		t.Errorf("GetSubtasks len = %d, want 0", len(subs))
	}
	if mustJSON(subs) != "[]" {
		t.Errorf("GetSubtasks JSON = %s, want []", mustJSON(subs))
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<err>"
	}
	return string(b)
}
