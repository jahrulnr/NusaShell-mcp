package mcpkit

import "time"

const (
	// BusinessEventNotificationMethod is the NusaShell-specific MCP
	// server-to-client notification method for durable business facts.
	BusinessEventNotificationMethod = "notifications/nusashell/event"

	// BusinessEventSchemaVersion is the current wire-envelope version.
	BusinessEventSchemaVersion = 1
)

// BusinessEventParams builds the versioned notification envelope shared by
// first-party plugins. The host owns source identity, so publishers only
// provide the event identity and business payload. Optional fields are left
// out when empty so the result remains a compact, valid envelope.
//
// Callers should keep eventID stable for the same logical fact and ensure data
// is JSON-marshalable before passing the map to an MCP notification sender.
func BusinessEventParams(eventID, eventType string, occurredAt time.Time, subject string, attributes map[string]any, data any) map[string]any {
	params := map[string]any{
		"schema_version": BusinessEventSchemaVersion,
		"event_id":       eventID,
		"type":           eventType,
	}
	if !occurredAt.IsZero() {
		params["occurred_at"] = occurredAt.Format(time.RFC3339Nano)
	}
	if subject != "" {
		params["subject"] = subject
	}
	if attributes != nil {
		params["attributes"] = attributes
	}
	if data != nil {
		params["data"] = data
	}
	return params
}
