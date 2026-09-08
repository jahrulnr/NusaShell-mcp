# NusaShell MCP Plugins

First-party MCP plugins for [NusaShell](https://github.com/jahrulnr/NusaShell).

Each plugin is a self-contained folder:

```
manifest.json + mcp/            # headless MCP-only plugin
manifest.json + ui/ + mcp/      # plugin with a window (UI + MCP)
```

## Plugins

| Plugin | Description |
| --- | --- |
| `kanban/` | Kanban board with project/column/ticket management |
| `notes/` | Simple notes app |
| `whatsapp/` | WhatsApp bridge — link your account, read/send messages via MCP |
| `telegram/` | Telegram bridge — link a bot (Bot API), read/send messages, approvals, FTS search |

## Architecture

```
mcpkit/              # shared Go module (config, stdio transport, data-file resolution)
notes/mcp/           # notes MCP server (Go)
kanban/mcp/          # kanban MCP server (Go)
whatsapp/mcp/        # whatsapp MCP server (Go, whatsmeow)
telegram/mcp/        # telegram MCP server (Go, mymrac/telego)
```

Each plugin's `mcp/` folder is an independent Go module that depends on
`mcpkit` via a `replace` directive. The MCP protocol is implemented with
[`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go).

## Business-event notifications

The message plugins use the shared, versioned NusaShell event notification
method `notifications/nusashell/event`. A publisher sends `schema_version: 1`,
a stable `event_id`, a domain `type`, and optional `occurred_at`, `subject`,
`attributes`, and JSON `data`. The host assigns `source` from the connected
plugin and deduplicates the normalized identity
`mcp:<server-id>:<event-id>`.

- Telegram emits `telegram.message`, with
  `message:<chat_id>:<message_id>` event IDs.
- WhatsApp emits `whatsapp.message_received`, with
  `message:<chat_jid>:<message_id>` event IDs.

Both notifications are emitted only after a newly received inbound message is
stored. Text in the event payload is bounded, while the complete message is
available through the plugin's read tools. The plugins do not use the
deprecated `notifications/message` compatibility bridge for these events.

## Build

Each plugin server is a standalone Go binary. Build from the plugin's `mcp/`
folder:

```bash
cd notes/mcp && go build -o server .
cd kanban/mcp && go build -o server .
cd whatsapp/mcp && go build -o server .
cd telegram/mcp && go build -o server .
```

The resulting `mcp/server` binary is what `manifest.json` launches
(`"command": "mcp/server", "args": []`).

### Build all

```bash
for p in notes kanban whatsapp telegram; do
  (cd "$p/mcp" && go build -o server .)
done
```

## Test

```bash
cd notes/mcp && go test ./...
cd kanban/mcp && go test ./...
cd whatsapp/mcp && go test ./...
cd telegram/mcp && go test ./...
```

## Smoke test

```bash
cd notes/mcp
printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1.0"}}}\n{"jsonrpc":"2.0","method":"notifications/initialized"}\n{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}\n' \
  | NUSASHELL_NOTES_DATA_FILE=/tmp/notes.json ./server 2>/dev/null
```

## License

MIT — see [LICENSE](LICENSE).

The `ui/` folders of the plugins remain plain HTML/JS (no Node build step);
only the MCP servers are Go.
