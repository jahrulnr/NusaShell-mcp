package mcpkit

import (
	"reflect"
	"testing"
	"time"
)

func TestBusinessEventParamsBuildsVersionedEnvelope(t *testing.T) {
	occurredAt := time.Date(2026, 9, 8, 10, 0, 0, 123456789, time.FixedZone("UTC+7", 7*60*60))
	attrs := map[string]any{"chat_id": "123", "from_me": false}
	data := map[string]any{"text": "hello"}

	got := BusinessEventParams("message:123:42", "telegram.message", occurredAt, "Alice", attrs, data)

	if got["schema_version"] != BusinessEventSchemaVersion {
		t.Fatalf("schema_version = %#v, want %d", got["schema_version"], BusinessEventSchemaVersion)
	}
	if got["event_id"] != "message:123:42" || got["type"] != "telegram.message" {
		t.Fatalf("identity = %#v, want message:123:42 / telegram.message", got)
	}
	if got["occurred_at"] != "2026-09-08T10:00:00.123456789+07:00" {
		t.Fatalf("occurred_at = %#v, want RFC3339Nano", got["occurred_at"])
	}
	if got["subject"] != "Alice" {
		t.Fatalf("subject = %#v, want Alice", got["subject"])
	}
	if !reflect.DeepEqual(got["attributes"], attrs) || !reflect.DeepEqual(got["data"], data) {
		t.Fatalf("payload values changed: %#v", got)
	}
}

func TestBusinessEventParamsOmitsEmptyOptionalFields(t *testing.T) {
	got := BusinessEventParams("evt-1", "trading.tick", time.Time{}, "", nil, nil)

	if len(got) != 3 {
		t.Fatalf("envelope fields = %#v, want only required fields", got)
	}
	for _, key := range []string{"occurred_at", "subject", "attributes", "data"} {
		if _, ok := got[key]; ok {
			t.Errorf("optional field %q present in %#v", key, got)
		}
	}
}
