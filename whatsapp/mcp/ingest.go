package main

import (
	"context"
	"time"
)

// Ingester drains the normalized event channel and writes to the store.
// It is the sole writer to whatsapp.db — read-side tool handlers query
// directly without competing for write locks (WAL mode allows concurrent
// readers while the ingester writes).
type Ingester struct {
	store *Store
	lastEventAt time.Time
}

// NewIngester creates an ingester that writes to the given store.
func NewIngester(store *Store) *Ingester {
	return &Ingester{store: store}
}

// Run drains the event channel until ctx is cancelled. It blocks — call
// from a dedicated goroutine.
func (in *Ingester) Run(ctx context.Context, events <-chan any) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			in.handle(ctx, ev)
			in.lastEventAt = time.Now()
		}
	}
}

// LastEventAt returns the timestamp of the last ingested event.
func (in *Ingester) LastEventAt() time.Time {
	return in.lastEventAt
}

// handle dispatches a normalized event to the appropriate store method.
func (in *Ingester) handle(ctx context.Context, ev any) {
	switch e := ev.(type) {
	case EventMessage:
		in.handleMessage(ctx, e)
	case EventMedia:
		in.handleMedia(ctx, e)
	case EventEdit:
		in.handleEdit(ctx, e)
	case EventDelete:
		in.handleDelete(ctx, e)
	case EventReaction:
		in.handleReaction(ctx, e)
	case EventContact:
		in.handleContact(ctx, e)
	case EventGroupInfo:
		in.handleGroupInfo(ctx, e)
	}
}

func (in *Ingester) handleMessage(ctx context.Context, e EventMessage) {
	kind := chatKindFromJID(e.ChatJID)
	name := ""
	if kind == "dm" {
		// For DMs, the chat name is the sender's push name (resolved later).
		name = ""
	}

	// Upsert the chat with the latest message info.
	if err := in.store.UpsertChat(ctx, e.ChatJID, kind, name, e.Text, e.Timestamp.Unix()); err != nil {
		stderr("ingest: upsert chat: %s", err)
		return
	}

	// Insert the message (INSERT OR IGNORE — live ingester wins on duplicate).
	if err := in.store.InsertMessage(ctx, e); err != nil {
		stderr("ingest: insert message: %s", err)
		return
	}

	// Upsert the sender as a contact (for name resolution).
	if !e.FromMe {
		if err := in.store.UpsertContact(ctx, e.SenderJID, "", "", "", e.Timestamp.Unix()); err != nil {
			stderr("ingest: upsert contact: %s", err)
		}
		// Increment unread for inbound messages.
		if err := in.store.IncrementUnread(ctx, e.ChatJID); err != nil {
			stderr("ingest: increment unread: %s", err)
		}
	}
}

func (in *Ingester) handleMedia(ctx context.Context, e EventMedia) {
	kind := chatKindFromJID(e.ChatJID)

	// Upsert the chat.
	if err := in.store.UpsertChat(ctx, e.ChatJID, kind, "", e.Caption, e.Timestamp.Unix()); err != nil {
		stderr("ingest: upsert chat (media): %s", err)
		return
	}

	// Insert the media message row (caption as text for searchability).
	if err := in.store.InsertMediaMessage(ctx, e); err != nil {
		stderr("ingest: insert media message: %s", err)
		return
	}

	// Upsert the media metadata.
	if err := in.store.UpsertMedia(ctx, e); err != nil {
		stderr("ingest: upsert media: %s", err)
		return
	}

	// Upsert the sender as a contact.
	if !e.FromMe {
		if err := in.store.UpsertContact(ctx, e.SenderJID, "", "", "", e.Timestamp.Unix()); err != nil {
			stderr("ingest: upsert contact (media): %s", err)
		}
		if err := in.store.IncrementUnread(ctx, e.ChatJID); err != nil {
			stderr("ingest: increment unread (media): %s", err)
		}
	}
}

func (in *Ingester) handleEdit(ctx context.Context, e EventEdit) {
	if err := in.store.UpdateMessageEdited(ctx, e.ChatJID, e.ID, e.NewText, e.EditedAt.Unix()); err != nil {
		stderr("ingest: update edited: %s", err)
	}
}

func (in *Ingester) handleDelete(ctx context.Context, e EventDelete) {
	if err := in.store.TombstoneMessage(ctx, e.ChatJID, e.ID, e.DeletedAt.Unix()); err != nil {
		stderr("ingest: tombstone: %s", err)
	}
}

func (in *Ingester) handleReaction(ctx context.Context, e EventReaction) {
	if err := in.store.UpsertReaction(ctx, e.ChatJID, e.TargetID, e.FromJID, e.Emoji, e.Timestamp.Unix()); err != nil {
		stderr("ingest: upsert reaction: %s", err)
	}
}

func (in *Ingester) handleContact(ctx context.Context, e EventContact) {
	if err := in.store.UpsertContact(ctx, e.JID, e.PushName, e.BusinessName, e.Phone, e.UpdatedAt.Unix()); err != nil {
		stderr("ingest: upsert contact: %s", err)
	}
}

func (in *Ingester) handleGroupInfo(ctx context.Context, e EventGroupInfo) {
	if err := in.store.UpsertGroup(ctx, e.JID, e.Name, e.Topic, e.OwnerJID, e.UpdatedAt.Unix()); err != nil {
		stderr("ingest: upsert group: %s", err)
		return
	}
	if len(e.Participants) > 0 {
		if err := in.store.SetGroupParticipants(ctx, e.JID, e.Participants); err != nil {
			stderr("ingest: set group participants: %s", err)
		}
	}
	// Update the chat name for the group.
	if e.Name != "" {
		if err := in.store.UpsertChat(ctx, e.JID, "group", e.Name, "", 0); err != nil {
			stderr("ingest: upsert group chat: %s", err)
		}
	}
}
