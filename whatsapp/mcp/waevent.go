package main

import "time"

// Normalized WhatsApp event types produced by the waclient translator and
// consumed by the ingester. whatsmeow types never cross this boundary — the
// ingester does a type switch on these concrete structs.
//
// Adapted from the wadb architecture (github.com/sausheong/wadb) where these
// live in a separate package to avoid an import cycle between the producer
// (waclient) and consumer (ingest). Here they stay in package main since the
// plugin is a single module, but the seam is the same: waclient produces,
// ingest consumes, neither imports whatsmeow event types directly past the
// translator.

// EventMessage is a text or conversation message.
type EventMessage struct {
	ChatJID   string
	SenderJID string
	ID        string
	Text      string
	Timestamp time.Time
	Kind      string // "" defaults to "text"
	QuotedID  string
	FromMe    bool
}

// EventMedia is a media message (image, video, audio, document, sticker).
type EventMedia struct {
	ChatJID     string
	SenderJID   string
	ID          string
	Caption     string
	Timestamp   time.Time
	Kind        string // "image"|"video"|"audio"|"voice"|"document"|"sticker"
	MimeType    string
	Size        int64
	Width       int
	Height      int
	DurationSec int
	DownloadRef string // opaque reference for later download
	FromMe      bool
}

// EventEdit is a message edit.
type EventEdit struct {
	ChatJID  string
	ID       string
	NewText  string
	EditedAt time.Time
}

// EventDelete is a message deletion (revoke).
type EventDelete struct {
	ChatJID   string
	ID        string
	DeletedAt time.Time
}

// EventReaction is a reaction added or removed on a message.
type EventReaction struct {
	ChatJID   string
	TargetID  string
	FromJID   string
	Emoji     string // "" removes reaction from FromJID
	Timestamp time.Time
}

// EventContact is a contact name update.
type EventContact struct {
	JID          string
	PushName     string
	BusinessName string
	Phone        string
	UpdatedAt    time.Time
}

// EventGroupInfo is a group metadata update.
type EventGroupInfo struct {
	JID         string
	Name        string
	Topic       string
	OwnerJID    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Participants []EventGroupParticipant
}

// EventGroupParticipant is a member of a group.
type EventGroupParticipant struct {
	JID      string
	IsAdmin  bool
	JoinedAt time.Time
}

// EventLoggedOut is a boundary event emitted when WhatsApp unlinks this
// device (session rotation or manual removal from Linked Devices). The
// ingester does not write anything for it — it exists so tool callers and
// the UI can surface "re-link required" without polling State().
type EventLoggedOut struct {
	Reason string
}
