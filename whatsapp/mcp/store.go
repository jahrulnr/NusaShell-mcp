package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the application database: messages, contacts, groups, media
// metadata. It uses SQLite in WAL mode with FTS5 for full-text search.
// The ingester is the sole writer; read-side tool handlers query directly.
type Store struct {
	db       *sql.DB
	dataDir  string
	mediaDir string
}

// ChatRow is a chat list entry.
type ChatRow struct {
	JID           string `json:"chat_jid"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	LastMessage   string `json:"last_message"`
	LastMessageAt int64  `json:"last_message_at"`
	UnreadCount   int    `json:"unread_count"`
}

// MessageRow is a message in a chat.
type MessageRow struct {
	ID         string `json:"message_id"`
	ChatJID    string `json:"chat_jid"`
	SenderJID  string `json:"sender_jid"`
	SenderName string `json:"sender_name"`
	Text       string `json:"text"`
	Timestamp  int64  `json:"timestamp"`
	FromMe     bool   `json:"from_me"`
	Kind       string `json:"kind"`
	QuotedID   string `json:"quoted_id,omitempty"`
	EditedAt   *int64 `json:"edited_at,omitempty"`
	DeletedAt  *int64 `json:"deleted_at,omitempty"`
	HasMedia   bool   `json:"has_media"`
}

// ContactRow is a contact.
type ContactRow struct {
	JID          string `json:"jid"`
	PushName     string `json:"push_name"`
	BusinessName string `json:"business_name"`
	Phone        string `json:"phone"`
	UpdatedAt    int64  `json:"updated_at"`
}

// GroupRow is a group.
type GroupRow struct {
	JID       string `json:"jid"`
	Name      string `json:"name"`
	Topic     string `json:"topic"`
	OwnerJID  string `json:"owner_jid"`
	UpdatedAt int64  `json:"updated_at"`
}

// GroupParticipantRow is a group member.
type GroupParticipantRow struct {
	JID      string `json:"jid"`
	IsAdmin  bool   `json:"is_admin"`
	JoinedAt int64  `json:"joined_at"`
}

// MediaRow is media metadata for a message.
type MediaRow struct {
	ChatJID     string `json:"chat_jid"`
	MessageID   string `json:"message_id"`
	Kind        string `json:"kind"`
	MimeType    string `json:"mime_type"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	DurationSec int    `json:"duration_sec"`
	Caption     string `json:"caption"`
	DownloadRef string `json:"-"`
	LocalPath   string `json:"local_path,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Downloaded  bool   `json:"downloaded"`
}

// appDSN uses modernc.org/sqlite pragma syntax and forward slashes so the
// application database behaves consistently across supported platforms.
func appDSN(dataDir string) string {
	return fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)",
		filepath.ToSlash(filepath.Join(dataDir, "whatsapp.db")))
}

// NewStore opens (or creates) the application database at dataDir/whatsapp.db.
func NewStore(dataDir string) (*Store, error) {
	mediaDir := filepath.Join(dataDir, "media")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return nil, fmt.Errorf("create media dir: %w", err)
	}

	db, err := sql.Open("sqlite", appDSN(dataDir))
	if err != nil {
		return nil, fmt.Errorf("open whatsapp db: %w", err)
	}
	// Single writer pattern: the ingester is the sole writer. Allow multiple
	// readers via WAL. Set MaxOpenConns to 1 for the writer connection to
	// avoid SQLITE_BUSY on concurrent writes from the same process.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, dataDir: dataDir, mediaDir: mediaDir}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			kind TEXT NOT NULL DEFAULT 'dm',
			name TEXT NOT NULL DEFAULT '',
			last_message TEXT NOT NULL DEFAULT '',
			last_message_at INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS contacts (
			jid TEXT PRIMARY KEY,
			push_name TEXT NOT NULL DEFAULT '',
			business_name TEXT NOT NULL DEFAULT '',
			phone TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS groups (
			jid TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			topic TEXT NOT NULL DEFAULT '',
			owner_jid TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS group_participants (
			group_jid TEXT NOT NULL,
			jid TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			joined_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (group_jid, jid),
			FOREIGN KEY (group_jid) REFERENCES groups(jid) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT NOT NULL,
			chat_jid TEXT NOT NULL,
			sender_jid TEXT NOT NULL,
			text TEXT,
			timestamp INTEGER NOT NULL DEFAULT 0,
			from_me INTEGER NOT NULL DEFAULT 0,
			kind TEXT NOT NULL DEFAULT 'text',
			quoted_id TEXT NOT NULL DEFAULT '',
			edited_at INTEGER,
			deleted_at INTEGER,
			created_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat_jid, id)
		)`,
		`CREATE INDEX IF NOT EXISTS messages_ts_idx ON messages(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS messages_chat_ts_idx ON messages(chat_jid, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS messages_sender_ts_idx ON messages(sender_jid, timestamp DESC)`,
		`CREATE TABLE IF NOT EXISTS media (
			chat_jid TEXT NOT NULL,
			message_id TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			duration_sec INTEGER NOT NULL DEFAULT 0,
			caption TEXT NOT NULL DEFAULT '',
			download_ref TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			downloaded INTEGER NOT NULL DEFAULT 0,
			downloaded_at INTEGER,
			PRIMARY KEY (chat_jid, message_id)
		)`,
		`CREATE TABLE IF NOT EXISTS reactions (
			chat_jid TEXT NOT NULL,
			message_id TEXT NOT NULL,
			from_jid TEXT NOT NULL,
			emoji TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat_jid, message_id, from_jid)
		)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
		`DROP TRIGGER IF EXISTS messages_ai`,
		`DROP TRIGGER IF EXISTS messages_ad`,
		`DROP TRIGGER IF EXISTS messages_au`,
		// Rebuild the FTS table on every startup. Older releases used an
		// external-content table whose message_id column did not map to the
		// messages.id column, causing MATCH/column reads to fail. A
		// self-contained index is small enough for this local mirror and works
		// for both fresh and old DBs.
		`DROP TABLE IF EXISTS fts_messages`,
		`CREATE VIRTUAL TABLE fts_messages USING fts5(
			message_id UNINDEXED,
			chat_jid UNINDEXED,
			text
		)`,
		`INSERT INTO fts_messages(message_id, chat_jid, text)
			SELECT id, chat_jid, COALESCE(text, '') FROM messages`,
		// FTS5 triggers to keep the self-contained index in sync with messages.
		`CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO fts_messages(message_id, chat_jid, text)
			VALUES (new.id, new.chat_jid, COALESCE(new.text, ''));
		END`,
		`CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
			DELETE FROM fts_messages WHERE message_id = old.id AND chat_jid = old.chat_jid;
		END`,
		`CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
			DELETE FROM fts_messages WHERE message_id = old.id AND chat_jid = old.chat_jid;
			INSERT INTO fts_messages(message_id, chat_jid, text)
			VALUES (new.id, new.chat_jid, COALESCE(new.text, ''));
		END`,
	}
	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration: %w\nsql: %s", err, m)
		}
	}
	return nil
}

// --- Write methods (called by the ingester — sole writer) ---

// UpsertChat inserts or updates a chat row.
func (s *Store) UpsertChat(ctx context.Context, jid, kind, name, lastMessage string, lastMessageAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chats (jid, kind, name, last_message, last_message_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(jid) DO UPDATE SET
		   kind=excluded.kind,
		   name=CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
		   last_message=CASE
		     WHEN excluded.last_message_at >= chats.last_message_at AND excluded.last_message != '' THEN excluded.last_message
		     ELSE chats.last_message
		   END,
		   last_message_at=MAX(excluded.last_message_at, chats.last_message_at)`,
		jid, kind, name, lastMessage, lastMessageAt, time.Now().Unix())
	return err
}

// IncrementUnread increments the unread count for a chat.
func (s *Store) IncrementUnread(ctx context.Context, jid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET unread_count = unread_count + 1 WHERE jid = ?`, jid)
	return err
}

// ResetUnread sets unread count to 0 for a chat.
func (s *Store) ResetUnread(ctx context.Context, jid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET unread_count = 0 WHERE jid = ?`, jid)
	return err
}

// UpsertContact inserts or updates a contact. Newer updated_at wins.
func (s *Store) UpsertContact(ctx context.Context, jid, pushName, businessName, phone string, updatedAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO contacts (jid, push_name, business_name, phone, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(jid) DO UPDATE SET
		   push_name=CASE WHEN excluded.updated_at > contacts.updated_at AND excluded.push_name != '' THEN excluded.push_name ELSE contacts.push_name END,
		   business_name=CASE WHEN excluded.updated_at > contacts.updated_at THEN excluded.business_name ELSE contacts.business_name END,
		   phone=CASE WHEN excluded.updated_at > contacts.updated_at THEN excluded.phone ELSE contacts.phone END,
		   updated_at=MAX(excluded.updated_at, contacts.updated_at)`,
		jid, pushName, businessName, phone, updatedAt)
	return err
}

// UpsertGroup inserts or updates a group.
func (s *Store) UpsertGroup(ctx context.Context, jid, name, topic, ownerJID string, updatedAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (jid, name, topic, owner_jid, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(jid) DO UPDATE SET
		   name=CASE WHEN excluded.name != '' THEN excluded.name ELSE groups.name END,
		   topic=CASE WHEN excluded.topic != '' THEN excluded.topic ELSE groups.topic END,
		   owner_jid=CASE WHEN excluded.owner_jid != '' THEN excluded.owner_jid ELSE groups.owner_jid END,
		   updated_at=MAX(excluded.updated_at, groups.updated_at)`,
		jid, name, topic, ownerJID, updatedAt)
	return err
}

// SetGroupParticipants replaces all participants for a group in a transaction.
func (s *Store) SetGroupParticipants(ctx context.Context, groupJID string, participants []EventGroupParticipant) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM group_participants WHERE group_jid = ?`, groupJID); err != nil {
		return err
	}
	for _, p := range participants {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO group_participants (group_jid, jid, is_admin, joined_at) VALUES (?, ?, ?, ?)`,
			groupJID, p.JID, p.IsAdmin, p.JoinedAt.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyGroupParticipantDelta atomically applies WhatsApp's incremental group
// membership updates without replacing members that were not mentioned.
func (s *Store) ApplyGroupParticipantDelta(ctx context.Context, groupJID string, joined []EventGroupParticipant, left, promoted, demoted []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, participant := range joined {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO group_participants (group_jid, jid, is_admin, joined_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(group_jid, jid) DO UPDATE SET
			   is_admin=excluded.is_admin,
			   joined_at=CASE WHEN group_participants.joined_at > 0 THEN group_participants.joined_at ELSE excluded.joined_at END`,
			groupJID, participant.JID, participant.IsAdmin, participant.JoinedAt.Unix()); err != nil {
			return err
		}
	}
	for _, jid := range promoted {
		if _, err := tx.ExecContext(ctx,
			`UPDATE group_participants SET is_admin = 1 WHERE group_jid = ? AND jid = ?`, groupJID, jid); err != nil {
			return err
		}
	}
	for _, jid := range demoted {
		if _, err := tx.ExecContext(ctx,
			`UPDATE group_participants SET is_admin = 0 WHERE group_jid = ? AND jid = ?`, groupJID, jid); err != nil {
			return err
		}
	}
	for _, jid := range left {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM group_participants WHERE group_jid = ? AND jid = ?`, groupJID, jid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertMessage inserts a message. INSERT OR IGNORE — live ingester wins on
// duplicate, so reconnect/history-sync backfill doesn't double-insert.
func (s *Store) InsertMessage(ctx context.Context, m EventMessage) error {
	_, err := s.InsertMessageIfNew(ctx, m)
	return err
}

// InsertMessageIfNew inserts a message and reports whether a new row was
// created. The inserted flag lets the ingester make unread counters and push
// notifications idempotent when WhatsApp retries the same event.
func (s *Store) InsertMessageIfNew(ctx context.Context, m EventMessage) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages (id, chat_jid, sender_jid, text, timestamp, from_me, kind, quoted_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ChatJID, m.SenderJID, m.Text, m.Timestamp.Unix(), m.FromMe, "text", m.QuotedID, time.Now().Unix())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// UpsertMedia inserts or updates media metadata.
func (s *Store) UpsertMedia(ctx context.Context, m EventMedia) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO media (chat_jid, message_id, kind, mime_type, size, width, height, duration_sec, caption, download_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(chat_jid, message_id) DO UPDATE SET
		   kind=excluded.kind, mime_type=excluded.mime_type, size=excluded.size,
		   width=excluded.width, height=excluded.height, duration_sec=excluded.duration_sec,
		   caption=excluded.caption, download_ref=CASE WHEN excluded.download_ref != '' THEN excluded.download_ref ELSE media.download_ref END`,
		m.ChatJID, m.ID, m.Kind, m.MimeType, m.Size, m.Width, m.Height, m.DurationSec, m.Caption, m.DownloadRef)
	return err
}

// InsertMediaMessage inserts a message row for a media message (with empty
// text so it shows up in get_messages but search uses the caption via FTS).
func (s *Store) InsertMediaMessage(ctx context.Context, m EventMedia) error {
	_, err := s.InsertMediaMessageIfNew(ctx, m)
	return err
}

// InsertMediaMessageIfNew is the idempotent variant used by the ingester to
// avoid counting or notifying duplicate media deliveries.
func (s *Store) InsertMediaMessageIfNew(ctx context.Context, m EventMedia) (bool, error) {
	text := m.Caption // caption is searchable; media-only messages use caption as text
	kind := m.Kind
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages (id, chat_jid, sender_jid, text, timestamp, from_me, kind, quoted_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)`,
		m.ID, m.ChatJID, m.SenderJID, text, m.Timestamp.Unix(), m.FromMe, kind, time.Now().Unix())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// UpdateMessageEdited sets new text and edited_at for a message.
func (s *Store) UpdateMessageEdited(ctx context.Context, chatJID, id, newText string, editedAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET text = ?, edited_at = ? WHERE chat_jid = ? AND id = ?`,
		newText, editedAt, chatJID, id)
	return err
}

// TombstoneMessage sets text=NULL and deleted_at for a deleted message.
func (s *Store) TombstoneMessage(ctx context.Context, chatJID, id string, deletedAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET text = NULL, deleted_at = ? WHERE chat_jid = ? AND id = ?`,
		deletedAt, chatJID, id)
	return err
}

// UpdateMessageReactions reads the existing reactions JSON, mutates, writes back.
// Reactions are stored as a simple per-message emoji→from_jid map.
func (s *Store) UpsertReaction(ctx context.Context, chatJID, messageID, fromJID, emoji string, timestamp int64) error {
	if emoji == "" {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM reactions WHERE chat_jid = ? AND message_id = ? AND from_jid = ?`,
			chatJID, messageID, fromJID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reactions (chat_jid, message_id, from_jid, emoji, timestamp)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(chat_jid, message_id, from_jid) DO UPDATE SET emoji=excluded.emoji, timestamp=excluded.timestamp`,
		chatJID, messageID, fromJID, emoji, timestamp)
	return err
}

// UpdateMediaDownloaded marks a media row as downloaded with the local path.
func (s *Store) UpdateMediaDownloaded(ctx context.Context, chatJID, messageID, localPath, sha256 string, size int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE media SET local_path = ?, sha256 = ?, size = ?, downloaded = 1, downloaded_at = ?
		 WHERE chat_jid = ? AND message_id = ?`,
		localPath, sha256, size, time.Now().Unix(), chatJID, messageID)
	return err
}

// SetMeta stores a key-value metadata entry.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

// --- Read methods (called by tool handlers) ---

// ListChats returns chats ordered by last activity, with optional kind filter.
func (s *Store) ListChats(ctx context.Context, kind string, limit, offset int) ([]ChatRow, error) {
	q := `SELECT jid, kind, name, last_message, last_message_at, unread_count FROM chats`
	args := []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY last_message_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ChatRow, 0)
	for rows.Next() {
		var c ChatRow
		if err := rows.Scan(&c.JID, &c.Kind, &c.Name, &c.LastMessage, &c.LastMessageAt, &c.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChat returns a single chat by JID.
func (s *Store) GetChat(ctx context.Context, jid string) (*ChatRow, error) {
	var c ChatRow
	err := s.db.QueryRowContext(ctx,
		`SELECT jid, kind, name, last_message, last_message_at, unread_count FROM chats WHERE jid = ?`, jid).
		Scan(&c.JID, &c.Kind, &c.Name, &c.LastMessage, &c.LastMessageAt, &c.UnreadCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetGroupParticipants returns participants for a group.
func (s *Store) GetGroupParticipants(ctx context.Context, groupJID string) ([]GroupParticipantRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT jid, is_admin, joined_at FROM group_participants WHERE group_jid = ? ORDER BY is_admin DESC, jid`, groupJID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GroupParticipantRow, 0)
	for rows.Next() {
		var p GroupParticipantRow
		var isAdmin int
		if err := rows.Scan(&p.JID, &isAdmin, &p.JoinedAt); err != nil {
			return nil, err
		}
		p.IsAdmin = isAdmin != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListContacts returns contacts matching a substring query.
func (s *Store) ListContacts(ctx context.Context, query string, limit int) ([]ContactRow, error) {
	q := `SELECT jid, push_name, business_name, phone, updated_at FROM contacts`
	args := []any{}
	if query != "" {
		q += ` WHERE push_name LIKE ? OR business_name LIKE ? OR phone LIKE ? OR jid LIKE ?`
		pattern := "%" + query + "%"
		args = append(args, pattern, pattern, pattern, pattern)
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ContactRow, 0)
	for rows.Next() {
		var c ContactRow
		if err := rows.Scan(&c.JID, &c.PushName, &c.BusinessName, &c.Phone, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListGroups returns groups matching a substring query.
func (s *Store) ListGroups(ctx context.Context, query string, limit int) ([]GroupRow, error) {
	q := `SELECT jid, name, topic, owner_jid, updated_at FROM groups`
	args := []any{}
	if query != "" {
		q += ` WHERE name LIKE ? OR topic LIKE ? OR jid LIKE ?`
		pattern := "%" + query + "%"
		args = append(args, pattern, pattern, pattern)
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GroupRow, 0)
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(&g.JID, &g.Name, &g.Topic, &g.OwnerJID, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetMessages returns messages for a chat, newest first, with cursor-based
// pagination. cursor is the timestamp of the oldest message in the previous
// page (0 for first page).
func (s *Store) GetMessages(ctx context.Context, chatJID string, cursor int64, limit int) ([]MessageRow, error) {
	q := `SELECT m.id, m.chat_jid, m.sender_jid,
			COALESCE(c.push_name, '') as sender_name,
			m.text, m.timestamp, m.from_me, m.kind, m.quoted_id, m.edited_at, m.deleted_at,
			EXISTS(SELECT 1 FROM media med WHERE med.chat_jid = m.chat_jid AND med.message_id = m.id) as has_media
		FROM messages m
		LEFT JOIN contacts c ON c.jid = m.sender_jid
		WHERE m.chat_jid = ?`
	args := []any{chatJID}
	if cursor > 0 {
		q += ` AND m.timestamp < ?`
		args = append(args, cursor)
	}
	q += ` ORDER BY m.timestamp DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]MessageRow, 0)
	for rows.Next() {
		var m MessageRow
		var fromMe int
		var hasMedia int
		var text sql.NullString
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.SenderName, &text, &m.Timestamp, &fromMe, &m.Kind, &m.QuotedID, &m.EditedAt, &m.DeletedAt, &hasMedia); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.FromMe = fromMe != 0
		m.HasMedia = hasMedia != 0
		if m.SenderName == "" {
			m.SenderName = formatJIDLabel(m.SenderJID, "")
		}
		if m.FromMe {
			m.SenderName = "You"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SearchMessages performs FTS5 full-text search across all message text.
func (s *Store) SearchMessages(ctx context.Context, query, chatJID, senderJID string, since, until int64, limit int) ([]MessageRow, error) {
	// FTS5 query — use the user's query directly if it's a valid FTS5 query,
	// otherwise wrap in quotes for a phrase search.
	ftsQuery := query
	if !isValidFTS5Query(query) {
		ftsQuery = `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	}

	q := `SELECT m.id, m.chat_jid, m.sender_jid,
			COALESCE(c.push_name, '') as sender_name,
			m.text, m.timestamp, m.from_me, m.kind, m.quoted_id, m.edited_at, m.deleted_at,
			EXISTS(SELECT 1 FROM media med WHERE med.chat_jid = m.chat_jid AND med.message_id = m.id) as has_media
		FROM fts_messages fts
		JOIN messages m ON m.id = fts.message_id AND m.chat_jid = fts.chat_jid
		LEFT JOIN contacts c ON c.jid = m.sender_jid
		WHERE fts_messages MATCH ?`
	args := []any{ftsQuery}
	if chatJID != "" {
		q += ` AND m.chat_jid = ?`
		args = append(args, chatJID)
	}
	if senderJID != "" {
		q += ` AND m.sender_jid = ?`
		args = append(args, senderJID)
	}
	if since > 0 {
		q += ` AND m.timestamp >= ?`
		args = append(args, since)
	}
	if until > 0 {
		q += ` AND m.timestamp <= ?`
		args = append(args, until)
	}
	q += ` ORDER BY m.timestamp DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]MessageRow, 0)
	for rows.Next() {
		var m MessageRow
		var fromMe int
		var hasMedia int
		var text sql.NullString
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.SenderName, &text, &m.Timestamp, &fromMe, &m.Kind, &m.QuotedID, &m.EditedAt, &m.DeletedAt, &hasMedia); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.FromMe = fromMe != 0
		m.HasMedia = hasMedia != 0
		if m.SenderName == "" {
			m.SenderName = formatJIDLabel(m.SenderJID, "")
		}
		if m.FromMe {
			m.SenderName = "You"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMedia returns media metadata for a message.
func (s *Store) GetMedia(ctx context.Context, chatJID, messageID string) (*MediaRow, error) {
	var m MediaRow
	var downloaded int
	var downloadedAt sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT chat_jid, message_id, kind, mime_type, size, width, height, duration_sec, caption, local_path, sha256, downloaded, downloaded_at
		 FROM media WHERE chat_jid = ? AND message_id = ?`, chatJID, messageID).
		Scan(&m.ChatJID, &m.MessageID, &m.Kind, &m.MimeType, &m.Size, &m.Width, &m.Height, &m.DurationSec, &m.Caption, &m.LocalPath, &m.SHA256, &downloaded, &downloadedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.Downloaded = downloaded != 0
	return &m, nil
}

// GetMediaDownloadRef returns the opaque download reference for a media message.
func (s *Store) GetMediaDownloadRef(ctx context.Context, chatJID, messageID string) (string, error) {
	var ref string
	err := s.db.QueryRowContext(ctx,
		`SELECT download_ref FROM media WHERE chat_jid = ? AND message_id = ?`, chatJID, messageID).
		Scan(&ref)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return ref, err
}

// CountMessages returns the total message count.
func (s *Store) CountMessages(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n)
	return n, err
}

// CountChats returns the total chat count.
func (s *Store) CountChats(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chats`).Scan(&n)
	return n, err
}

// CountContacts returns the total contact count.
func (s *Store) CountContacts(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts`).Scan(&n)
	return n, err
}

// GetMeta retrieves a metadata value.
func (s *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// isValidFTS5Query returns true if the query looks like a valid FTS5
// expression (contains FTS5 operators). Otherwise the caller should wrap
// it in quotes for a phrase search.
func isValidFTS5Query(q string) bool {
	// Simple heuristic: if it contains FTS5 operators, treat as raw.
	for _, op := range []string{" AND ", " OR ", " NOT ", " NEAR ", "*", ":"} {
		if strings.Contains(q, op) {
			return true
		}
	}
	return false
}

// MediaPath returns the full path for a cached media file.
func (s *Store) MediaPath(sha256, ext string) string {
	return filepath.Join(s.mediaDir, sha256+"."+ext)
}

// MarshalReactions returns the reactions for a message as a JSON array.
func (s *Store) GetReactions(ctx context.Context, chatJID, messageID string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_jid, emoji FROM reactions WHERE chat_jid = ? AND message_id = ?`, chatJID, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0)
	for rows.Next() {
		var fromJID, emoji string
		if err := rows.Scan(&fromJID, &emoji); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"from_jid": fromJID, "emoji": emoji})
	}
	return out, rows.Err()
}

// jsonReactions is a helper to marshal reactions for a message result.
func jsonReactions(r []map[string]any) string {
	if len(r) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(r)
	return string(b)
}
