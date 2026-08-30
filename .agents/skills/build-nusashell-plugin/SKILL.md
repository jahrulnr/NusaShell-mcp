---
name: build-nusashell-plugin
description: Build, debug, or release a first-party NusaShell MCP plugin in this repository (kanban, notes, whatsapp, telegram). Use when creating a new plugin, fixing agent-facing bugs, improving a plugin UI, or preparing a version bump/release. Covers the manifest+CONTRACT+Go-server+UI anatomy, host contract rules, the battle-tested debug playbook, verification (unit + MOCK harness + visual), and CI release flow.
---

# Build NusaShell MCP plugins

Plugin work in this repo must satisfy both consumers: the NusaShell **host**
(reads `manifest.json`, serves the UI with a `window.shell.callTool` bridge,
renders `CONTRACT.md`, enforces `plugin_contract_mode`) and the **agent**
(discover via `mcp_search`, never call a tool before `tool_schema` +
`contract_read`). The single source of truth for repo-wide rules is
`AGENTS.md` at the repo root — read it first.

## Plugin anatomy

```
<plugin>/
├── manifest.json    id, version, icon("file://icon.png"), ui.entry, mcp.command("mcp/server"),
│                    contract.entry("CONTRACT.md"), dependencies.shell, category
├── CONTRACT.md      state + side-effect table per tool + best practices (host-rendered!)
├── mcp/             independent Go module (replace => ../../mcpkit), builds to `server`
│   ├── main.go      resolve data dir, open store, connect client, start ingester, serve stdio
│   ├── tools.go     tool registration + handler factories (jsonResult / errorResult)
│   ├── helpers.go   arg extraction, safe errors, json/error results
│   ├── store.go     SQLite (WAL + FTS5) — the app DB
│   ├── tgclient/wa… client + mock (MOCK_ENABLED=1) behind one interface
│   └── *_test.go    unit/integration tests, no network
└── ui/              plain HTML/CSS/JS, no build step (optional for headed plugins)
```

Tool names must **not** start with the plugin's `domain` (last segment of the
plugin id) and must **not** equal `domain`. `manifest.json` `version` is the
only version source: bump it, then CI tags `<plugin>-v<version>`.

## Workflow

1. **Read** `AGENTS.md`, the plugin's `CONTRACT.md`, and `mcp/` + `ui/` before
   changing anything. Note the declared data dir and `mcpkit` usage.
2. **Backend change:** keep handler factory + validation + honest errors.
   Commit-relevant proof: `cd <plugin>/mcp && go build -o server . && go vet
   ./... && go test ./...`.
3. **UI change:** map JSON keys exactly (snake_case; see AGENTS.md), reverse
   message pages to oldest→newest, poll read-only tools with signature-based
   re-render, escape user content, no mock fallback.
4. **Test** with `MOCK_ENABLED=1` before touching the real bot. Run the plugin
   server over stdio JSON-RPC, proxy `tools/call` to an HTTP endpoint, serve
   `ui/` with a shim that routes `window.shell.callTool` to that endpoint, then
   screenshot with Playwright and *inspect the pixels* (layout must be clean,
   not just "tests green").
5. **Bump + release:** update `manifest.json` version and `CONTRACT.md` when
   tool behavior changed. Commit, push; CI builds on 3 OS and releases
   `<plugin>-v<version>`.

## Verification checklist (before claiming done)

- [ ] `go build -o server . && go vet ./... && go test ./...` green
- [ ] `search_messages`-style FTS works on a **fresh** DB and on a **legacy**
      DB (migration repaired it)
- [ ] Privacy/allowlist matching accepts id, @username, display name
- [ ] Empty lists serialize as `[]`, not `null`
- [ ] UI renders real backend data with correct field names; thread order
      oldest→newest; sent messages appear immediately
- [ ] `contract_read` output matches `CONTRACT.md` on disk
- [ ] `mcp_register` + `mcp_enable` succeed; `mcp_list` shows running tools
- [ ] Binary hash in the installed plugin dir matches the repo build

## Debug playbook (battle-tested)

- **FTS "no such column: T.message_id"** — FTS5 external content against a
  WITHOUT ROWID table. Rebuild FTS as self-contained
  (`USING fts5(message_id, chat_id, text)`), drop+recreate+repopulate in
  `migrate()`, add `TestMigrate_RepairsBrokenFTSIndex`.
- **Messages never appear / bot silent** — check `status`: if
  `privacy_mode=true` and the sender isn't allowlisted (numeric id, @username,
  or display name match), the message is dropped by design. Fix the allowlist
  or the matching rule.
- **UI shows empty chat** — field mismatch (UI reads camelCase, backend sends
  snake_case) or missing outbound mirror after send.
- **UI shows newest-first with stale badges** — reverse pages client-side and
  reset unread on read.
- **Polling dies silently** — wrap getUpdates in a reconnect loop with backoff;
  flip Connected=false while down.

## Safety

- Never commit the compiled `server` binary, `*-data/` dirs, or `*.test*`.
- Never log or return tokens; store them mode 0600 in the plugin data dir.
- Register/replace plugins only with the owner's knowledge, backup the plugin's
  `*-data/` dir first, restore it after registering, and re-enable — then
  verify live discovery and a real tool call.
- `send_*` tools deliver real messages to real people: confirm target and
  content before executing, and prefer non-destructive tools in tests.