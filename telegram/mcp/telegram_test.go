package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ---- test harness ----------------------------------------------------------

// newTestRig builds a Store on a temp dir, a FakeClient backed by that store,
// and a fully registered MCPServer. Handlers are invoked directly through the
// package-level handler factories (handleXxx) — no network, no stdio.
func newTestRig(t *testing.T) (*Store, *FakeClient, *Ingester, *server.MCPServer) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cli := NewFakeClient(store)
	ing := NewIngester(store)
	srv := server.NewMCPServer("nusashell.telegram", "0.0.0-test",
		server.WithToolCapabilities(true))
	registerTools(srv, cli, store, ing)
	return store, cli, ing, srv
}

// callInvokes a tool handler directly with JSON-shaped arguments.
func callHandler(t *testing.T, h server.ToolHandlerFunc, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "test",
			Arguments: args,
		},
	}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	return res
}

// decodeResult extracts the structured content of a handler result as a map.
func decodeResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	if res.StructuredContent == nil {
		t.Fatalf("result has no structured content: %v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return m
}

// textOf flattens the text content of a result (used for error messages).
func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// asApprovals marshals the approvals slice ([]ApprovalRow) for assertions.
func asApprovals(t *testing.T, v any) []ApprovalRow {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal approvals: %v", err)
	}
	var out []ApprovalRow
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal approvals: %v", err)
	}
	return out
}

func asMessages(t *testing.T, v any) []MessageRow {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	var out []MessageRow
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	return out
}

// ---- status -----------------------------------------------------------------

func TestStatus_ZeroTimeUntilFirstEvent(t *testing.T) {
	_, cli, ing, _ := newTestRig(t)

	h := handleStatus(cli, newStoreOf(t), ing)
	res := callHandler(t, h, nil)
	m := decodeResult(t, res)

	// Before any event, last_event_at must be 0 (renderable as "—"), not a
	// year-1 epoch.
	if v, ok := m["last_event_at"].(float64); !ok || v != 0 {
		t.Errorf("last_event_at = %v, want 0", m["last_event_at"])
	}
	if m["paired"] != false {
		t.Errorf("paired = %v, want false", m["paired"])
	}
}

func TestStatus_IncludesAllowlistAndPrivacy(t *testing.T) {
	store, cli, ing, _ := newTestRig(t)
	ctx := context.Background()
	if err := store.AddToAllowlist(ctx, "111111111"); err != nil {
		t.Fatalf("AddToAllowlist: %v", err)
	}
	if err := store.SetMeta(ctx, privacyModeMetaKey, "1"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	h := handleStatus(cli, store, ing)
	m := decodeResult(t, callHandler(t, h, nil))

	if m["privacy_mode"] != true {
		t.Errorf("privacy_mode = %v, want true", m["privacy_mode"])
	}
	allow, ok := m["allowlist"].([]any)
	if !ok || len(allow) != 1 || allow[0] != "111111111" {
		t.Errorf("allowlist = %#v, want [111111111]", m["allowlist"])
	}
}

// ---- answer_callback / approvals ---------------------------------------------

func TestAnswerCallback_ResolvesApproval(t *testing.T) {
	// Use a store WITHOUT the seeded mock approvals (FakeClient with nil store
	// keeps its mock data in memory only; the handler still updates this real
	// store directly).
	store := newStoreOf(t)
	cli := NewFakeClient(nil)
	ctx := context.Background()

	// Seed a pending approval (as the ingester would after a button press).
	app := ApprovalRow{ID: "cq_1", ChatID: "111111111", MessageID: "99", Text: "approve:yes", SenderID: "222222222", Time: time.Now().Unix(), Status: "pending"}
	if err := store.UpsertApproval(ctx, app); err != nil {
		t.Fatalf("UpsertApproval: %v", err)
	}

	h := handleAnswerCallback(cli, store)

	// Approve path.
	res := callHandler(t, h, map[string]any{"callback_query_id": "cq_1", "text": "Approved ✓"})
	m := decodeResult(t, res)
	if m["resolution"] != "approved" {
		t.Errorf("resolution = %v, want approved", m["resolution"])
	}

	pending := mustPendingApprovals(t, store)
	if len(pending) != 0 {
		t.Errorf("approval still pending after approve: %+v", pending)
	}

	// Deny path on a fresh approval.
	app2 := ApprovalRow{ID: "cq_2", ChatID: "111111111", MessageID: "99", Text: "approve:no", SenderID: "222222222", Time: time.Now().Unix(), Status: "pending"}
	if err := store.UpsertApproval(ctx, app2); err != nil {
		t.Fatalf("UpsertApproval: %v", err)
	}
	res = callHandler(t, h, map[string]any{"callback_query_id": "cq_2", "text": "Denied ❌"})
	m = decodeResult(t, res)
	if m["resolution"] != "denied" {
		t.Errorf("resolution = %v, want denied", m["resolution"])
	}
	if pending := mustPendingApprovals(t, store); len(pending) != 0 {
		t.Errorf("approval still pending after deny: %+v", pending)
	}
}

func mustPendingApprovals(t *testing.T, store *Store) []ApprovalRow {
	t.Helper()
	rows, err := store.ListPendingApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListPendingApprovals: %v", err)
	}
	return rows
}

// ---- outbound mirror ---------------------------------------------------------

func TestSendMessage_MirrorsOutboundImmediately(t *testing.T) {
	store, cli, _, _ := newTestRig(t)
	// FakeClient Login so sends are allowed.
	if _, err := cli.Login(context.Background(), "123:FAKE"); err != nil {
		t.Fatalf("login: %v", err)
	}

	h := handleSendMessage(cli, store)
	res := callHandler(t, h, map[string]any{
		"chat_id": "-1001234567890",
		"text":    "hello from the test",
	})
	m := decodeResult(t, res)
	if m["message_id"] == nil {
		t.Fatalf("no message_id in result: %s", textOf(t, res))
	}

	rows, err := store.GetMessagesCursor(context.Background(), "-1001234567890", 0, 10)
	if err != nil {
		t.Fatalf("GetMessagesCursor: %v", err)
	}
	var mirrored bool
	for _, r := range rows {
		if r.FromMe && r.Text == "hello from the test" {
			mirrored = true
		}
	}
	if !mirrored {
		t.Errorf("outbound text not found in store: %+v", rows)
	}

	// The chat row must now exist with a last_message preview.
	chat, err := store.GetChat(context.Background(), "-1001234567890")
	if err != nil || chat == nil {
		t.Fatalf("chat row missing after send: %v (err %v)", chat, err)
	}
	if !strings.Contains(chat.LastMessage, "hello from the test") {
		t.Errorf("chat last_message = %q, want preview", chat.LastMessage)
	}
}

// ---- get_messages marks read --------------------------------------------------

func TestGetMessages_MarksChatRead(t *testing.T) {
	store, _, _, _ := newTestRig(t)
	ctx := context.Background()
	chatID := "111111111"

	if err := store.IncrementUnread(ctx, chatID); err != nil {
		t.Fatalf("IncrementUnread: %v", err)
	}
	// Seed one message so the handler has something to read.
	if err := store.InsertMessage(ctx, MessageRow{ID: "1", ChatID: chatID, SenderName: "Andi", Text: "hi", Timestamp: time.Now().Unix()}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := store.UpsertChat(ctx, chatID, "dm", "Andi", "hi", time.Now().Unix()); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}

	h := handleGetMessages(store)
	callHandler(t, h, map[string]any{"chat_id": chatID, "limit": float64(10)})

	chat, err := store.GetChat(ctx, chatID)
	if err != nil || chat == nil {
		t.Fatalf("chat row missing: %v", err)
	}
	if chat.UnreadCount != 0 {
		t.Errorf("unread_count = %d after read, want 0", chat.UnreadCount)
	}
}

// ---- helpers (ported/shared assembly) ----------------------------------------

func newStoreOf(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAllowlistMatch(t *testing.T) {
	cases := []struct {
		name           string
		entry          string
		senderID       string
		senderUsername string
		senderName     string
		want           bool
	}{
		{"numeric id matches", "111222", "111222", "", "Jahrulnr", true},
		{"numeric id with @ case-insensitive", "@111222", "111222", "", "", true},
		{"username matches with @", "@Jahrulnr", "999", "jahrulnr", "Jahrulnr", true},
		{"username matches without @", "Jahrulnr", "999", "jahrulnr", "Jahrulnr", true},
		{"username case-insensitive", "jahrulnr", "999", "Jahrulnr", "Dummy", true},
		{"display name matches", "Jahrulnr", "999", "", "jahrulnr", true},
		{"mismatch returns false", "@OrangLain", "999", "jahrulnr", "Jahrulnr", false},
		{"empty entry never matches even with empty sender", "", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowlistMatch(tc.entry, tc.senderID, tc.senderUsername, tc.senderName); got != tc.want {
				t.Errorf("allowlistMatch(%q, %q, %q, %q) = %v, want %v",
					tc.entry, tc.senderID, tc.senderUsername, tc.senderName, got, tc.want)
			}
		})
	}
}

// TestValidateChatID ensures the accepted chat_id shapes match Telegram's
// (int64-as-string, optional "-100" prefix, or @username).
// TestStatus_UnreadDMCount feeds the automation gate: status exposes a scalar
// unread_dm_count so a workflow can cheaply check "is there a DM waiting"
// (uses: Telegram.status) and only spawn an agent step when it is != 0.
func TestStatus_UnreadDMCount(t *testing.T) {
	store, cli, ing, _ := newTestRig(t)
	ctx := context.Background()
	now := time.Now().Unix()

	// Seeded mock data has unread on a group + channel, none on DMs.
	h := handleStatus(cli, store, ing)
	if m := decodeResult(t, callHandler(t, h, nil)); m["unread_dm_count"] != float64(0) {
		t.Errorf("unread_dm_count = %v, want 0 (seed has no unread DM)", m["unread_dm_count"])
	}

	// An unread DM flips the counter; an unread group does not.
	if err := store.UpsertChat(ctx, "111111111", "dm", "Andi", "halo", now); err != nil {
		t.Fatalf("UpsertChat dm: %v", err)
	}
	if err := store.IncrementUnread(ctx, "111111111"); err != nil {
		t.Fatalf("IncrementUnread: %v", err)
	}
	if err := store.UpsertChat(ctx, "-1001234567890", "group", "Dev", "x", now); err != nil {
		t.Fatalf("UpsertChat group: %v", err)
	}
	if err := store.IncrementUnread(ctx, "-1001234567890"); err != nil {
		t.Fatalf("IncrementUnread group: %v", err)
	}
	m := decodeResult(t, callHandler(t, h, nil))
	if m["unread_dm_count"] != float64(1) {
		t.Errorf("unread_dm_count = %v, want 1 (one unread DM, group ignored)", m["unread_dm_count"])
	}
}

// TestIngester_NotifiesOnInboundMessage verifies the push hook: after an
// inbound message (not from the bot) is stored, the ingester invokes the
// notify callback once with the event; outbound (bot) messages never notify.
func TestIngester_NotifiesOnInboundMessage(t *testing.T) {
	store := newStoreOf(t)
	ing := NewIngester(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var got []string
	ing.WithInboundNotify(func(ev TelegramEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev.ChatID()+"/"+ev.MessageID())
	})

	events := make(chan any, 4)
	go ing.Run(ctx, events)

	inbound := TelegramEvent{
		Type: EventMessage,
		Data: map[string]any{
			kChatID:         "111111111",
			kChatType:       "dm",
			kChatName:       "Andi",
			kMessageID:      "42",
			kText:           "halo",
			kSenderID:       "111111111",
			kSenderName:     "Andi",
			kFromMe:         false,
			kUpdateID:       int64(1),
			kSenderUsername: "andi",
		},
		Timestamp: time.Now().Unix(),
	}
	outbound := inbound
	outbound.Data = make(map[string]any, len(inbound.Data))
	for k, v := range inbound.Data {
		outbound.Data[k] = v
	}
	outbound.Data[kMessageID] = "43"
	outbound.Data[kFromMe] = true

	events <- inbound
	events <- outbound

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("notify callback was not invoked for inbound message")
		case <-time.After(20 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "111111111/42" {
		t.Fatalf("notify calls = %v, want exactly [111111111/42] (outbound must be skipped)", got)
	}
}

func TestValidateChatID(t *testing.T) {
	valid := []string{"-1001234567890", "123456789", "@somechannel", "-42"}
	for _, id := range valid {
		if err := validateChatID(id); err != nil {
			t.Errorf("validateChatID(%q) = %v, want nil", id, err)
		}
	}
	invalid := []string{"", "abc", "12a", "@a", "-", "0", "1.5"}
	for _, id := range invalid {
		if err := validateChatID(id); err == nil {
			t.Errorf("validateChatID(%q) = nil, want error", id)
		}
	}
}

func TestChunkText_RuneAwareNoLoss(t *testing.T) {
	long := strings.Repeat("é", 5000) + "\n\n" + strings.Repeat("x", 500)
	chunks := chunkText(long, 4096)
	if len(chunks) < 2 {
		t.Fatalf("expected ≥2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c)) > 4096 {
			t.Errorf("chunk is %d runes (>4096)", len([]rune(c)))
		}
	}
	joined := strings.Join(chunks, "")
	if joined != long {
		t.Errorf("chunks lost data (len %d vs %d)", len(joined), len(long))
	}
}

func TestMarkdownToHTML_EscapesCodeAndLinks(t *testing.T) {
	src := "## Title\n\n`code <tag>`\n\n```go\nfmt.Println(\"<b>\")\n```\n\n- one\n- two\n\n> quote line"
	out := markdownToHTML(src)
	if !strings.Contains(out, "<b>Title</b>") {
		t.Errorf("header not bolded: %s", out)
	}
	if !strings.Contains(out, "<code>code &lt;tag&gt;</code>") {
		t.Errorf("inline code not escaped: %s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;") {
		t.Errorf("fenced code content should be escaped: %s", out)
	}
	if !strings.Contains(out, "<blockquote>") || !strings.Contains(out, "quote line") {
		t.Errorf("blockquote missing: %s", out)
	}
	if !strings.Contains(out, "• one") {
		t.Errorf("list bullet missing: %s", out)
	}
}

func TestMarkdownToHTML_LinkEscape(t *testing.T) {
	out := markdownToHTML(`[docs](https://example.com/?a=1&b=2)`)
	if !strings.Contains(out, `href="https://example.com/?a=1&amp;b=2"`) {
		t.Errorf("link URL not escaped: %s", out)
	}
}

// TestMigrate_RepairsBrokenFTSIndex guards against the v0.1 schema bug where
// the FTS5 index used content='messages' (external content) against a
// WITHOUT ROWID table — that made every fts_messages query fail with
// "no such column: T.message_id". NewStore's migration must rebuild the FTS
// table as a self-contained regular FTS5 table and repopulate it, so search
// works on databases created before the fix.
func TestMigrate_RepairsBrokenFTSIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telegram.db")
	dsn := "file:" + filepath.ToSlash(dbPath) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_foreign_keys=on"

	// 1. Create the OLD broken schema exactly as v0.1 did.
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	legacy := []string{
		`CREATE TABLE messages (
			id TEXT NOT NULL, chat_id TEXT NOT NULL, sender_name TEXT NOT NULL DEFAULT '',
			text TEXT, timestamp INTEGER NOT NULL DEFAULT 0, from_me INTEGER NOT NULL DEFAULT 0,
			edited_at INTEGER, created_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat_id, id))`,
		`CREATE VIRTUAL TABLE fts_messages USING fts5(
			message_id UNINDEXED, chat_id UNINDEXED, text,
			content='messages', content_rowid='rowid')`,
		`CREATE TABLE chats (id TEXT PRIMARY KEY, type TEXT NOT NULL DEFAULT 'dm', name TEXT NOT NULL DEFAULT '',
			last_message TEXT NOT NULL DEFAULT '', last_message_at INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE approvals (id TEXT PRIMARY KEY, chat_id TEXT NOT NULL DEFAULT '',
			message_id TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '',
			sender_id TEXT NOT NULL DEFAULT '', time INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'pending')`,
		`CREATE TABLE allowlist (user_id TEXT PRIMARY KEY, added_at INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO messages (id, chat_id, sender_name, text, timestamp, from_me) VALUES ('1','-1009','Citra','the release is out',100,0)`,
	}
	for _, q := range legacy {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("legacy exec %q: %v", q, err)
		}
	}
	// Confirm the legacy FTS is indeed broken (that's the bug we guard).
	if _, err := db.Query(`SELECT message_id FROM fts_messages`); err == nil {
		t.Fatalf("expected legacy FTS index to be broken, but it queries fine")
	}
	_ = db.Close()

	// 2. Open through NewStore → migration rebuilds + repopulates FTS.
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore after migration: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 3. All legacy rows must be searchable now.
	rows, err := store.SearchMessages(context.Background(), "release")
	if err != nil {
		t.Fatalf("SearchMessages after migration: %v", err)
	}
	if len(rows) != 1 || rows[0].Text != "the release is out" {
		t.Errorf("search after migration = %+v, want 1 row with legacy text", rows)
	}

	// 4. New writes stay indexed via the triggers.
	ctx := context.Background()
	if err := store.InsertMessage(ctx, MessageRow{ID: "2", ChatID: "-1009", SenderName: "Andi", Text: "review my PR", Timestamp: 200}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	rows, err = store.SearchMessages(ctx, "review")
	if err != nil {
		t.Fatalf("SearchMessages (new row): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "2" {
		t.Errorf("search new row = %+v, want id 2", rows)
	}
}
