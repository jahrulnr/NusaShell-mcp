package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the Telegram plugin's application database: chats, messages,
// approvals, and the allowlist. It uses SQLite in WAL mode with an FTS5
// virtual table for full-text message search.
//
// The ingester is the sole writer (single connection, SetMaxOpenConns(1));
// read-side tool handlers query directly and never compete for write locks
// (WAL allows concurrent readers while the ingester writes).
type Store struct {
	db      *sql.DB
	dataDir string
}

// ChatRow is a chat list entry.
type ChatRow struct {
	ID            string `json:"id"`
	Type          string `json:"type"` // dm | group | channel
	Name          string `json:"name"`
	LastMessage   string `json:"last_message"`
	LastMessageAt int64  `json:"last_message_at"`
	UnreadCount   int    `json:"unread_count"`
}

// MessageRow is a message in a chat.
type MessageRow struct {
	ID         string `json:"id"`
	ChatID     string `json:"chat_id"`
	SenderName string `json:"sender_name"`
	Text       string `json:"text"`
	Timestamp  int64  `json:"timestamp"`
	FromMe     bool   `json:"from_me"`
	EditedAt   *int64 `json:"edited_at,omitempty"`
}

// ApprovalRow is an approval prompt created by send_inline_buttons and
// resolved by answer_callback. Status is "pending", "approved", or "denied".
type ApprovalRow struct {
	ID        string `json:"id"`
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	SenderID  string `json:"sender_id"`
	Time      int64  `json:"time"`
	Status    string `json:"status"`
}

// NewStore opens (or creates) the application database at dataDir/telegram.db.
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// Forward slashes in the DSN — SQLite URI parsing expects them regardless
	// of OS (Windows filepath.Join produces backslashes).
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_foreign_keys=on",
		filepath.ToSlash(filepath.Join(dataDir, "telegram.db")))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open telegram db: %w", err)
	}
	// Sole-writer pattern: a single connection serializes all writes and
	// avoids SQLITE_BUSY on concurrent writes from this process. WAL still
	// allows multiple concurrent readers.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, dataDir: dataDir}
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
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL DEFAULT 'dm',
			name TEXT NOT NULL DEFAULT '',
			last_message TEXT NOT NULL DEFAULT '',
			last_message_at INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			sender_name TEXT NOT NULL DEFAULT '',
			text TEXT,
			timestamp INTEGER NOT NULL DEFAULT 0,
			from_me INTEGER NOT NULL DEFAULT 0,
			edited_at INTEGER,
			created_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS messages_ts_idx ON messages(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS messages_chat_ts_idx ON messages(chat_id, timestamp DESC)`,
		`CREATE TABLE IF NOT EXISTS approvals (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL DEFAULT '',
			message_id TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL DEFAULT '',
			sender_id TEXT NOT NULL DEFAULT '',
			time INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending'
		)`,
		`CREATE INDEX IF NOT EXISTS approvals_status_idx ON approvals(status)`,
		`CREATE TABLE IF NOT EXISTS allowlist (
			user_id TEXT PRIMARY KEY,
			added_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS fts_messages USING fts5(
			message_id,
			chat_id,
			text
		)`,
		// FTS5 triggers keep the index in sync with the messages table.
		`CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO fts_messages(message_id, chat_id, text)
			VALUES (new.id, new.chat_id, COALESCE(new.text, ''));
		END`,
		`CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
			INSERT INTO fts_messages(fts_messages, message_id, chat_id, text)
			VALUES('delete', old.id, old.chat_id, COALESCE(old.text, ''));
		END`,
		`CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
			INSERT INTO fts_messages(fts_messages, message_id, chat_id, text)
			VALUES('delete', old.id, old.chat_id, COALESCE(old.text, ''));
			INSERT INTO fts_messages(message_id, chat_id, text)
			VALUES (new.id, new.chat_id, COALESCE(new.text, ''));
		END`,
		// FTS rebuild for databases created before v0.2: the old definition used
		// content='messages' (external content). That requires the content table
		// to expose a rowid, but messages has a composite TEXT primary key and is
		// therefore WITHOUT ROWID — the external-content index is unwritable and
		// search_messages fails with "no such column". We drop it and recreate
		// the FTS table as a self-contained regular FTS5 table, then repopulate
		// from messages. Idempotent: on fresh DBs this is a no-op re-create.
		`DROP TABLE IF EXISTS fts_messages`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS fts_messages USING fts5(
			message_id,
			chat_id,
			text
		)`,
		`INSERT INTO fts_messages(message_id, chat_id, text)
		 SELECT id, chat_id, COALESCE(text, '') FROM messages`,
	}
	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration: %w\nsql: %s", err, m)
		}
	}
	return nil
}

// --- Write methods (called by the ingester — sole writer) ---

// UpsertChat inserts or updates a chat row. A non-empty name overrides the
// stored name; an empty name preserves the existing value so incremental
// updates that only carry a new last_message don't wipe a resolved name.
// The same applies to type: an empty type preserves the stored classification
// (used when an outbound send has no chat-type evidence yet).
func (s *Store) UpsertChat(ctx context.Context, id, typ, name, lastMsg string, lastMsgAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chats (id, type, name, last_message, last_message_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   type=CASE WHEN excluded.type != '' THEN excluded.type ELSE chats.type END,
		   name=CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
		   last_message=excluded.last_message,
		   last_message_at=excluded.last_message_at`,
		id, typ, name, lastMsg, lastMsgAt, time.Now().Unix())
	return err
}

// InsertMessage inserts a message. INSERT OR IGNORE — the live ingester wins
// on duplicate so reconnect/backfill doesn't double-insert.
func (s *Store) InsertMessage(ctx context.Context, msg MessageRow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages (id, chat_id, sender_name, text, timestamp, from_me, edited_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.ChatID, msg.SenderName, msg.Text, msg.Timestamp, msg.FromMe, msg.EditedAt, time.Now().Unix())
	return err
}

// UpdateMessageEdited sets new text and edited_at for a message.
func (s *Store) UpdateMessageEdited(ctx context.Context, chatID, id, newText string, editedAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET text = ?, edited_at = ? WHERE chat_id = ? AND id = ?`,
		newText, editedAt, chatID, id)
	return err
}

// IncrementUnread increments the unread count for a chat.
func (s *Store) IncrementUnread(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET unread_count = unread_count + 1 WHERE id = ?`, id)
	return err
}

// ResetUnread sets the unread count to 0 for a chat.
func (s *Store) ResetUnread(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET unread_count = 0 WHERE id = ?`, id)
	return err
}

// UpsertApproval inserts or updates an approval row. The status of an existing
// approval is only overwritten when a non-empty status is supplied, so a
// re-ingested callback doesn't clobber an already-resolved approval.
func (s *Store) UpsertApproval(ctx context.Context, app ApprovalRow) error {
	status := app.Status
	if status == "" {
		status = "pending"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO approvals (id, chat_id, message_id, text, sender_id, time, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   chat_id=excluded.chat_id, message_id=excluded.message_id, text=excluded.text,
		   sender_id=excluded.sender_id, time=excluded.time,
		   status=CASE WHEN excluded.status != '' THEN excluded.status ELSE approvals.status END`,
		app.ID, app.ChatID, app.MessageID, app.Text, app.SenderID, app.Time, status)
	return err
}

// UpdateApprovalStatus sets the status of an approval (pending/approved/denied).
func (s *Store) UpdateApprovalStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ? WHERE id = ?`, status, id)
	return err
}

// AddToAllowlist adds a user/chat id to the allowlist (idempotent).
func (s *Store) AddToAllowlist(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO allowlist (user_id, added_at) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET added_at=excluded.added_at`,
		userID, time.Now().Unix())
	return err
}

// RemoveFromAllowlist removes a user/chat id from the allowlist.
func (s *Store) RemoveFromAllowlist(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM allowlist WHERE user_id = ?`, userID)
	return err
}

// SetMeta stores a key-value metadata entry (used for the ingestion watermark
// and other plugin state).
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

// --- Read methods (called by tool handlers) ---

// ListChats returns chats ordered by last activity, with an optional type
// filter (dm/group/channel).
func (s *Store) ListChats(ctx context.Context, typ string, limit, offset int) ([]ChatRow, error) {
	q := `SELECT id, type, name, last_message, last_message_at, unread_count FROM chats`
	args := []any{}
	if typ != "" {
		q += ` WHERE type = ?`
		args = append(args, typ)
	}
	q += ` ORDER BY last_message_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatRow
	for rows.Next() {
		var c ChatRow
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.LastMessage, &c.LastMessageAt, &c.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChat returns a single chat by id, or (nil, nil) if not found.
func (s *Store) GetChat(ctx context.Context, id string) (*ChatRow, error) {
	var c ChatRow
	err := s.db.QueryRowContext(ctx,
		`SELECT id, type, name, last_message, last_message_at, unread_count FROM chats WHERE id = ?`, id).
		Scan(&c.ID, &c.Type, &c.Name, &c.LastMessage, &c.LastMessageAt, &c.UnreadCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetMessages returns messages for a chat, newest first, with limit/offset
// pagination.
func (s *Store) GetMessages(ctx context.Context, chatID string, limit, offset int) ([]MessageRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, sender_name, text, timestamp, from_me, edited_at
		 FROM messages WHERE chat_id = ?
		 ORDER BY timestamp DESC LIMIT ? OFFSET ?`,
		chatID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var fromMe int
		var text sql.NullString
		if err := rows.Scan(&m.ID, &m.ChatID, &m.SenderName, &text, &m.Timestamp, &fromMe, &m.EditedAt); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.FromMe = fromMe != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// SearchMessages performs FTS5 full-text search across all stored message text.
func (s *Store) SearchMessages(ctx context.Context, query string) ([]MessageRow, error) {
	ftsQuery := query
	if !isValidFTS5Query(query) {
		ftsQuery = `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.chat_id, m.sender_name, m.text, m.timestamp, m.from_me, m.edited_at
		 FROM fts_messages fts
		 JOIN messages m ON m.id = fts.message_id AND m.chat_id = fts.chat_id
		 WHERE fts_messages MATCH ?
		 ORDER BY m.timestamp DESC`,
		ftsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var fromMe int
		var text sql.NullString
		if err := rows.Scan(&m.ID, &m.ChatID, &m.SenderName, &text, &m.Timestamp, &fromMe, &m.EditedAt); err != nil {
			return nil, err
		}
		m.Text = text.String
		m.FromMe = fromMe != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListPendingApprovals returns all approvals with status "pending", oldest
// first.
func (s *Store) ListPendingApprovals(ctx context.Context) ([]ApprovalRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, message_id, text, sender_id, time, status
		 FROM approvals WHERE status = 'pending'
		 ORDER BY time ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ApprovalRow
	for rows.Next() {
		var a ApprovalRow
		if err := rows.Scan(&a.ID, &a.ChatID, &a.MessageID, &a.Text, &a.SenderID, &a.Time, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAllowlist returns all allowlisted user/chat ids, most recently added
// first.
func (s *Store) ListAllowlist(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM allowlist ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GetMeta retrieves a metadata value, or "" if the key is absent.
func (s *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
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

// GetOrCreateDefaultProject is a compatibility shim with the Kanban plugin's
// store contract. Telegram has no projects, so it simply records a sentinel
// meta entry. It never fails in a way that blocks startup.
func (s *Store) GetOrCreateDefaultProject() error {
	ctx := context.Background()
	v, err := s.GetMeta(ctx, "default_project")
	if err != nil {
		return err
	}
	if v == "" {
		return s.SetMeta(ctx, "default_project", "telegram")
	}
	return nil
}

// isValidFTS5Query returns true if the query looks like a raw FTS5 expression
// (contains FTS5 operators). Otherwise the caller should wrap it in quotes for
// a phrase search.
func isValidFTS5Query(q string) bool {
	for _, op := range []string{" AND ", " OR ", " NOT ", " NEAR ", "*", ":"} {
		if strings.Contains(q, op) {
			return true
		}
	}
	return false
}
