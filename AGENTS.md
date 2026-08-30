# AGENTS.md — NusaShell MCP Plugins

Instructions for humans and coding agents working in this repository: the
first-party [NusaShell](https://github.com/jahrulnr/NusaShell) MCP plugin
suite. Each plugin folder is self-contained (`manifest.json` + `CONTRACT.md` +
`mcp/` Go server + optional `ui/`). The MCP protocol runs on
[`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go).

## Repository layout

```
mcpkit/           shared Go module: data-file resolution, safe errors, stdio bootstrap
kanban/           kanban board plugin (project/column/ticket management)
notes/            note-taking plugin
whatsapp/         WhatsApp bridge (whatsmeow, QR/pair-code login)
telegram/         Telegram bridge (mymrac/telego, Bot API, approvals, FTS search)
.github/workflows/ci.yml   build+vet+test matrix, release, versions.json upkeep
versions.json     release bookkeeping (version, tag, releasedAt per plugin)
```

Each plugin's `mcp/` is an **independent Go module** that imports `mcpkit`
through a `replace` directive. The shipped artifact is the compiled binary at
`<plugin>/mcp/server` (gitignored — build it, never commit it).

## Non-negotiables

1. **Keep CI green for every changed plugin.** `go build -o server .`, `go vet
   ./...`, `go test ./...` must pass inside `<plugin>/mcp`. CI runs the same on
   Ubuntu, macOS, and Windows with `GO_VERSION=1.26`.
2. **Never break the MCP contract between the plugin and the host.** The host
   (NusaShell `transport/plugin_handler.go` + `infrastructure/tools`) reads
   `manifest.json` (`mcp.command`, `ui.entry`, `contract.entry`), serves the UI
   with an injected `window.shell.callTool(pluginId, toolName, args)` bridge,
   unwraps tool results as `{ result: CallToolResult }`, and renders
   `CONTRACT.md` via its `contract_read` tool (`plugin_contract_mode`:
   off/hint/require). A plugin declares `"contract": {"entry": "CONTRACT.md"}`
   and must keep that file truthful about every tool's side effects.
3. **Tool naming rule.** `domain` = last segment of the plugin id (e.g.
   `telegram` for `nusashell.telegram`). Tool names must **not** start with
   `${domain}_` and must **not** equal `domain`. Prefer short verbs
   (`status`, `login`, `list_chats`, `send_message`, `search_messages`).
   Preserve upstream tool names when wrapping an existing catalog.
4. **Secure by default.** Never put tokens/credentials in schemas, results,
   logs, or manifests. Tokens live in the plugin's data dir with mode `0600`.
   Never commit `mcp/server`, `telegram-data/`, `whatsapp-data/`, `*.test*`.
5. **No mock data in the UI.** The `ui/` talks to the backend solely through
   `window.shell.callTool`. Debug state is fine server-side via an explicit
   `MOCK_ENABLED=1` env gate, never a silent UI fallback.

## Steady-state JSON contract (used everywhere)

Backend results must be **snake_case** and **self-describing**; empty slices
must serialize as `[]`, never `null` (guard with `if x == nil { x = []T{} }`):

- Chats: `id`, `type` (dm|group|channel), `name`, `last_message`,
  `last_message_at` (unix seconds), `unread_count`
- Messages: `id` (string), `chat_id`, `sender_name`, `text`,
  `timestamp` (unix seconds), `from_me` (bool), `edited_at?`
- Status: `paired`, `connected`, `bot_id`, `bot_name`, `privacy_mode`,
  `message_count`, `chat_count`, `last_event_at` (0 until first event — never a
  year-1 epoch), `allowlist` (array)

Send results carry `message_id`, `timestamp`, `chat_id`; paged reads carry
`cursor`/`next_cursor` (timestamp-based). `chat_id` and message ids are strings
(int64-as-string) to avoid JS precision loss; Telegram's `-100…` supergroup
prefix is significant.

## Tool handler pattern (Go)

Mirror `telegram/mcp/tools.go` / `whatsapp/mcp/tools.go`:

- Each tool is `mcp.NewTool(name, mcp.WithDescription(...), mcp.WithString(...))`
  registered via `s.AddTool`, handler = `handleXxx(cli, store, ...)` factory
  returning `server.ToolHandlerFunc`.
- Results via `jsonResult(data)` (sets both text and `StructuredContent`);
  failures via `errorResult(err)` (`IsError: true`, sanitized text).
- Strict input validation (`validateChatID`, enum-checked args, bounded
  lengths); safe error text (`safeErrorMessage`: strip control chars, cap).
- Diagnostics go to **stderr** only (`stderr(...)` helper); stdout is reserved
  for MCP JSON-RPC.
- Data lives under `{NUSASHELL_USER_DATA or NUSASHELL_DATA_DIR}/plugins-data/
  <plugin-id>/` via `mcpkit.MustResolveDataFile(envOverride, pluginID, filename,
  fallbackDir)`.

## UI conventions (plain HTML/JS, no build step)

- Identify the bridge: `window.shell.callTool(pluginId, tool, args)` →
  envelope `{ result: CallToolResult }`. Unwrap `result`, surface tool-level
  errors (`isError`) as thrown messages, prefer `structuredContent`, else
  `JSON.parse` the text content. Empty arrays should reach callers as `[]`.
- Read the **snake_case** keys listed above; never invent camelCase aliases.
- A chat thread renders **oldest → newest** while `get_messages` returns
  newest-first — reverse client-side.
- Live updates: poll read-only tools (status/chats/messages) on an interval and
  re-render only when a signature (ids+text+timestamps) changed, preserving
  scroll position; sidebar connection chip + pending-approval badge derived
  from `status` and `list_pending_approvals`.
- Confirm destructive actions; toasts for feedback; empty states for zero rows.

## Versioning & release (CI-driven)

1. Bump `version` in `<plugin>/manifest.json` (semver, e.g. `0.2.1`).
2. Commit + push to `master`; CI builds/vets/tests the changed plugin on 3 OS,
   creates tag `<plugin>-v<version>`, a GitHub release, and auto-updates
   `versions.json` ([skip ci] commit by the bot).
3. A tag that already exists fails CI — bump before merging.
4. Update `CONTRACT.md` side-effect table whenever tool behavior changes so the
   host-rendered contract stays accurate.

## Battle-tested debug playbook

### FTS5 must not be external-content against a WITHOUT ROWID table

`telegram/mcp` stores messages with a composite TEXT primary key
`PRIMARY KEY (chat_id, id)`, which makes the table **WITHOUT ROWID**. A
`CREATE VIRTUAL TABLE … USING fts5(…, content='messages', content_rowid='rowid')`
external-content index then fails every read with
`no such column: T.message_id`. Use a **self-contained** FTS5 table
(`USING fts5(message_id, chat_id, text)`) plus triggers. If a legacy DB has the
broken index, `migrate()` must `DROP TABLE IF EXISTS fts_messages`, recreate,
and `INSERT INTO fts_messages … SELECT … FROM messages` — idempotent, with a
regression test (`TestMigrate_RepairsBrokenFTSIndex`).

### Allowlist matching must accept id / @username / display name

With privacy mode on, inbound messages are dropped silently before ingestion.
Entries can be numeric ids, `@username` (with or without `@`), or display
names; compare case-insensitively. The event must carry `sender_username`
alongside `sender_id`/`sender_name`, and a pure `allowlistMatch(entry,
senderID, senderUsername, senderName)` helper keeps the rule testable. A user
whose `/start` never appears in the UI is almost always a privacy/allowlist
mismatch — check `status` (`privacy_mode`, `allowlist`) before anything else.

### Outbound messages should appear instantly

After a successful send, mirror the message into the store
(`InsertMessage` + `UpsertChat`) so the UI shows it without waiting for the
poll loop to echo the bot's own message; `INSERT OR IGNORE` on `(chat_id, id)`
prevents duplicates when the poll echo arrives. Reading a chat
(`get_messages`) should reset its local unread count.

### Long polling must self-heal

Reconnect the `getUpdates` stream in a loop with exponential backoff and flip
`Connected=false` while down, so a network blip never leaves the bot stuck in a
permanent fake "connected" state.

### markdown→Telegram-HTML order matters

Extract code first, then blockquotes into placeholders **before** HTML
escaping (a raw `>` would survive as `&gt;`), then escape prose, then restore
tags. Link URLs are already `&`-escaped by then — only escape `"`.