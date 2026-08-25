package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"google.golang.org/protobuf/proto"
)

// WhatsmeowClient implements Client using the whatsmeow library.
type WhatsmeowClient struct {
	dataDir string
	log     waLog.Logger

	mu        sync.Mutex
	client    *whatsmeow.Client
	container *sqlstore.Container
	device    *store.Device

	// eventCh is the single channel the ingester drains. whatsmeow's
	// AddEventHandler callback pushes normalized events onto it.
	eventCh chan any

	// state is protected by mu.
	state PairState

	// qrCh holds the active QR login channel (nil when no login in progress).
	qrCtx context.Context
	qrCxl context.CancelFunc
}

// NewWhatsmeowClient creates a client backed by a SQLite session store at
// dataDir/session.db. It does NOT connect — call Connect or StartQR.
func NewWhatsmeowClient(dataDir string, verbose bool) *WhatsmeowClient {
	level := "WARN"
	if verbose {
		level = "DEBUG"
	}
	return &WhatsmeowClient{
		dataDir: dataDir,
		log:     waLog.Stdout("whatsmeow", level, true),
		eventCh: make(chan any, 256),
	}
}

// initStore opens the whatsmeow session SQLite database. WAL + busy_timeout
// are critical: post-pair sync runs parallel prekey/identity/history writes
// that serialize and surface as SQLITE_BUSY without WAL.
func (w *WhatsmeowClient) initStore(ctx context.Context) error {
	dsn := sessionDSN(w.dataDir)
	container, err := sqlstore.New(ctx, "sqlite", dsn, w.log)
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}
	w.container = container
	w.device = device
	return nil
}

// sessionDSN builds the SQLite DSN for the whatsmeow session store.
//
// modernc.org/sqlite uses _pragma=<name>(<value>) query syntax (NOT
// mattn/go-sqlite3's _<pragma>=<value>). foreign_keys must be ON or
// whatsmeow's schema upgrade aborts with "foreign keys are not enabled".
// Forward slashes are required in the DSN — SQLite URI parsing expects
// them regardless of OS (Windows filepath.Join produces backslashes).
func sessionDSN(dataDir string) string {
	return fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)",
		filepath.ToSlash(filepath.Join(dataDir, "session.db")))
}

func (w *WhatsmeowClient) newClient() {
	w.client = whatsmeow.NewClient(w.device, w.log)
	w.client.AddEventHandler(w.handleEvent)
	w.client.EnableAutoReconnect = true
	w.client.AutoReconnectErrors = 10
}

// Events returns the normalized event channel. The ingester drains it.
func (w *WhatsmeowClient) Events(ctx context.Context) <-chan any {
	return w.eventCh
}

// State reports the current pairing/connection state.
func (w *WhatsmeowClient) State() PairState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

// Connect brings the socket up using stored credentials.
func (w *WhatsmeowClient) Connect(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.client == nil {
		if err := w.initStore(ctx); err != nil {
			return err
		}
		w.newClient()
	}

	if w.client.Store.ID == nil {
		w.state.AwaitingQR = false
		return ErrNotPaired
	}

	if err := w.client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	w.state.Paired = true
	w.state.Connected = true
	w.state.DeviceJID = w.client.Store.ID.String()
	return nil
}

// StartQR begins a QR login flow. The returned channel emits rotating QR
// codes until the QR is scanned or expires.
func (w *WhatsmeowClient) StartQR(ctx context.Context) (<-chan QRCode, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.client == nil {
		if err := w.initStore(ctx); err != nil {
			return nil, err
		}
		w.newClient()
	}

	// Cancel any prior QR flow.
	if w.qrCxl != nil {
		w.qrCxl()
	}
	qrCtx, cancel := context.WithCancel(ctx)
	w.qrCtx = qrCtx
	w.qrCxl = cancel
	w.state.AwaitingQR = true

	// GetQRChannel must be called BEFORE Connect for the QR flow to work.
	qrChan, err := w.client.GetQRChannel(qrCtx)
	if err != nil {
		cancel()
		w.state.AwaitingQR = false
		return nil, fmt.Errorf("get qr channel: %w", err)
	}

	if err := w.client.Connect(); err != nil {
		cancel()
		w.state.AwaitingQR = false
		return nil, fmt.Errorf("connect for qr: %w", err)
	}

	out := make(chan QRCode, 8)
	go func() {
		defer close(out)
		for {
			select {
			case <-qrCtx.Done():
				return
			case evt, ok := <-qrChan:
				if !ok {
					return
				}
				switch evt.Event {
				case "code":
					qr := QRCode{
						Code:      evt.Code,
						ExpiresAt: time.Now().Add(60 * time.Second),
					}
					select {
					case out <- qr:
					case <-qrCtx.Done():
						return
					}
				case "success":
					return
				}
			}
		}
	}()

	return out, nil
}

// Disconnect closes the socket cleanly.
func (w *WhatsmeowClient) Disconnect() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.qrCxl != nil {
		w.qrCxl()
		w.qrCxl = nil
	}
	if w.client != nil {
		w.client.Disconnect()
	}
	w.state.Connected = false
}

// Logout disconnects and clears stored credentials.
func (w *WhatsmeowClient) Logout(ctx context.Context) error {
	w.mu.Lock()
	if w.client != nil {
		w.client.Disconnect()
		if w.client.Store.ID != nil {
			if err := w.client.Logout(ctx); err != nil {
				w.mu.Unlock()
				return fmt.Errorf("logout: %w", err)
			}
		}
	}
	w.state = PairState{}
	w.client = nil
	w.device = nil
	w.container = nil
	w.mu.Unlock()

	// Re-init a fresh device for the next login.
	return w.initStore(ctx)
}

// SendText sends a text message.
func (w *WhatsmeowClient) SendText(ctx context.Context, chatJID, text, replyToID string) (SendResult, error) {
	w.mu.Lock()
	client := w.client
	w.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return SendResult{}, ErrNotConnected
	}

	jid, err := parseJID(chatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse chat jid: %w", err)
	}

	msg := buildTextMessage(text, replyToID, types.EmptyJID)

	resp, err := client.SendMessage(ctx, jid, msg)
	if err != nil {
		return SendResult{}, fmt.Errorf("send text: %w", err)
	}
	return SendResult{MessageID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// SendMedia uploads bytes and sends a media message.
func (w *WhatsmeowClient) SendMedia(ctx context.Context, chatJID, kind string, data []byte, mimeType, caption, replyToID string) (SendResult, error) {
	w.mu.Lock()
	client := w.client
	w.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return SendResult{}, ErrNotConnected
	}

	jid, err := parseJID(chatJID)
	if err != nil {
		return SendResult{}, fmt.Errorf("parse chat jid: %w", err)
	}

	uploaded, err := client.Upload(ctx, data, whatsmeow.MediaType(kind))
	if err != nil {
		return SendResult{}, fmt.Errorf("upload media: %w", err)
	}

	msg := buildMediaMessage(kind, uploaded, mimeType, caption)
	if msg == nil {
		return SendResult{}, fmt.Errorf("unsupported media kind: %s", kind)
	}

	resp, err := client.SendMessage(ctx, jid, msg)
	if err != nil {
		return SendResult{}, fmt.Errorf("send media: %w", err)
	}
	return SendResult{MessageID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// React adds or removes a reaction.
func (w *WhatsmeowClient) React(ctx context.Context, chatJID, messageID, emoji string) error {
	w.mu.Lock()
	client := w.client
	w.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return ErrNotConnected
	}

	jid, err := parseJID(chatJID)
	if err != nil {
		return fmt.Errorf("parse chat jid: %w", err)
	}

	// BuildReaction creates the reaction protocol message.
	sender := client.Store.ID
	if sender == nil {
		sender = &jid
	}
	reaction := client.BuildReaction(jid, *sender, messageID, emoji)
	_, err = client.SendMessage(ctx, jid, reaction)
	if err != nil {
		return fmt.Errorf("send reaction: %w", err)
	}
	return nil
}

// MarkRead marks a chat read.
func (w *WhatsmeowClient) MarkRead(ctx context.Context, chatJID, upToMessageID string) error {
	w.mu.Lock()
	client := w.client
	w.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return ErrNotConnected
	}

	jid, err := parseJID(chatJID)
	if err != nil {
		return fmt.Errorf("parse chat jid: %w", err)
	}

	var msgIDs []types.MessageID
	if upToMessageID != "" {
		msgIDs = []types.MessageID{upToMessageID}
	}
	// MarkRead signature: (ctx, ids, timestamp, chat, sender, receiptTypeExtra...)
	// For DMs, sender == chat. For groups, sender is the group JID.
	return client.MarkRead(ctx, msgIDs, time.Now(), jid, jid)
}

// Download fetches an encrypted media blob. The downloadRef is the base64-
// encoded serialized *waE2E.Message with the media info.
func (w *WhatsmeowClient) Download(ctx context.Context, downloadRef string) (DownloadResult, error) {
	w.mu.Lock()
	client := w.client
	w.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return DownloadResult{}, ErrNotConnected
	}

	msgBytes, err := decodeDownloadRef(downloadRef)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("decode download ref: %w", err)
	}

	var msg waE2E.Message
	if err := proto.Unmarshal(msgBytes, &msg); err != nil {
		return DownloadResult{}, fmt.Errorf("unmarshal message: %w", err)
	}

	data, err := client.DownloadAny(ctx, &msg)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("download: %w", err)
	}

	return DownloadResult{
		Bytes:    data,
		MimeType: extractMIME(&msg),
	}, nil
}

// RequestSync asks WhatsApp to backfill history for a chat. whatsmeow doesn't
// expose a direct per-chat backfill method; we trigger an app state fetch
// which causes the server to push history sync events.
func (w *WhatsmeowClient) RequestSync(ctx context.Context, chatJID string) error {
	w.mu.Lock()
	client := w.client
	w.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return ErrNotConnected
	}

	// FetchAppState triggers a resync of the given app state type. For
	// per-chat history, there's no direct API — we fetch the critical
	// patches which include chat list and message history updates.
	return client.FetchAppState(ctx, appstate.WAPatchCriticalUnblockLow, false, false)
}

// handleEvent is the whatsmeow event handler. It translates raw whatsmeow
// events into normalized events and pushes them onto the event channel.
// This is the ONLY place whatsmeow event types are referenced.
//
// In this whatsmeow version, all message types (text, media, edits, deletes,
// reactions) arrive as *events.Message. We pick apart the message struct to
// determine the kind and emit the appropriate normalized event.
func (w *WhatsmeowClient) handleEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Message:
		w.translateMessage(evt)
	case *events.GroupInfo:
		w.translateGroupInfo(evt)
	case *events.Connected:
		w.mu.Lock()
		w.state.Connected = true
		w.state.AwaitingQR = false
		if w.client != nil && w.client.Store.ID != nil {
			w.state.Paired = true
			w.state.DeviceJID = w.client.Store.ID.String()
		}
		w.mu.Unlock()
	case *events.Disconnected:
		w.mu.Lock()
		w.state.Connected = false
		w.mu.Unlock()
	case *events.PairSuccess:
		w.mu.Lock()
		w.state.Paired = true
		w.state.AwaitingQR = false
		if w.client != nil && w.client.Store.ID != nil {
			w.state.DeviceJID = w.client.Store.ID.String()
		}
		w.mu.Unlock()
		// Request app state sync after pairing.
		if w.client != nil {
			go w.client.FetchAppState(context.Background(), appstate.WAPatchCriticalUnblockLow, true, false)
		}
	case *events.QR:
		// QR codes are handled via the StartQR channel, not here.
	}
}

// translateMessage picks apart an events.Message into one of the normalized
// ingest types. Order matters: ProtocolMessage (edit/delete) and Reaction
// must be checked before falling through to text/media.
func (w *WhatsmeowClient) translateMessage(e *events.Message) {
	msg := e.Message
	if msg == nil {
		return
	}

	chatJID := e.Info.Chat.String()
	senderJID := e.Info.Sender.String()
	timestamp := e.Info.Timestamp
	id := e.Info.ID
	fromMe := e.Info.IsFromMe

	// Edits and revokes (deletes) arrive as ProtocolMessage submessages.
	if pm := msg.GetProtocolMessage(); pm != nil {
		key := pm.GetKey()
		if key == nil {
			return
		}
		targetID := key.GetID()
		switch pm.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			w.pushEvent(EventDelete{
				ChatJID:   chatJID,
				ID:        targetID,
				DeletedAt: timestamp,
			})
			return
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			edited := pm.GetEditedMessage()
			if edited != nil {
				w.pushEvent(EventEdit{
					ChatJID:  chatJID,
					ID:       targetID,
					NewText:  textOf(edited),
					EditedAt: timestamp,
				})
			}
			return
		}
		return
	}

	// Reactions
	if rxn := msg.GetReactionMessage(); rxn != nil {
		targetID := ""
		if rxn.GetKey() != nil {
			targetID = rxn.GetKey().GetID()
		}
		w.pushEvent(EventReaction{
			ChatJID:   chatJID,
			TargetID:  targetID,
			FromJID:   senderJID,
			Emoji:     rxn.GetText(),
			Timestamp: timestamp,
		})
		return
	}

	// Media kinds
	kind, mimeType, caption := extractMediaInfo(msg)
	if kind != "" {
		mime, size, width, height, dur := mediaMeta(msg)
		_ = mime
		_ = size
		_ = width
		_ = height
		_ = dur

		// Serialize the full message for later download.
		refBytes, err := proto.Marshal(msg)
		if err != nil {
			return
		}
		downloadRef := encodeDownloadRef(refBytes)

		w.pushEvent(EventMedia{
			ChatJID:     chatJID,
			SenderJID:   senderJID,
			ID:          id,
			Caption:     caption,
			Timestamp:   timestamp,
			Kind:        kind,
			MimeType:    mimeType,
			DownloadRef: downloadRef,
			FromMe:      fromMe,
		})
		return
	}

	// Text variants — Conversation (plain) or ExtendedTextMessage (with context).
	text := textOf(msg)
	if text == "" {
		return
	}
	quotedID := extractQuotedID(msg)

	w.pushEvent(EventMessage{
		ChatJID:   chatJID,
		SenderJID: senderJID,
		ID:        id,
		Text:      text,
		Timestamp: timestamp,
		QuotedID:  quotedID,
		FromMe:    fromMe,
	})

	// Also upsert the sender's push name as a contact if present.
	if e.Info.PushName != "" && !fromMe {
		w.pushEvent(EventContact{
			JID:       senderJID,
			PushName:  e.Info.PushName,
			Phone:     e.Info.Sender.User,
			UpdatedAt: timestamp,
		})
	}
}

// translateGroupInfo flattens an events.GroupInfo into a normalized event.
// GroupInfo carries deltas (Join/Leave/Promote/Demote), not a full roster.
// We upsert the group name/topic and emit join/promote events. For a full
// participant list, the store would need to call GetGroupInfo separately.
func (w *WhatsmeowClient) translateGroupInfo(e *events.GroupInfo) {
	name := ""
	topic := ""
	if e.Name != nil {
		name = e.Name.Name
	}
	if e.Topic != nil {
		topic = e.Topic.Topic
	}

	ownerJID := ""
	if e.Sender != nil {
		ownerJID = e.Sender.String()
	}

	// Build participant list from Join + Promote (these are the users present).
	// This is a delta, not a full roster — the ingester upserts.
	participants := make([]EventGroupParticipant, 0)
	for _, jid := range e.Join {
		participants = append(participants, EventGroupParticipant{
			JID:      jid.String(),
			JoinedAt: e.Timestamp,
		})
	}
	for _, jid := range e.Promote {
		participants = append(participants, EventGroupParticipant{
			JID:      jid.String(),
			IsAdmin:  true,
			JoinedAt: e.Timestamp,
		})
	}

	w.pushEvent(EventGroupInfo{
		JID:          e.JID.String(),
		Name:         name,
		Topic:        topic,
		OwnerJID:     ownerJID,
		UpdatedAt:    e.Timestamp,
		Participants: participants,
	})
}

func (w *WhatsmeowClient) pushEvent(ev any) {
	select {
	case w.eventCh <- ev:
	default:
		// Channel full — drop oldest and push. This prevents the ingester
		// from blocking the whatsmeow event loop if ingestion is slow.
		select {
		case <-w.eventCh:
		default:
		}
		select {
		case w.eventCh <- ev:
		default:
		}
	}
}

// parseJID converts a string JID to a types.JID.
func parseJID(s string) (types.JID, error) {
	return types.ParseJID(s)
}
