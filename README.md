# NusaShell MCP Plugins (v2 — Go)

First-party MCP plugins for [NusaShell](https://github.com/jahrulnr/NusaShell).

> **v2 branch:** All MCP servers are written in Go. The legacy Node.js/TypeScript
> implementation lives on the `master` branch.

Each plugin is a self-contained folder:

```
manifest.json + mcp/            # headless MCP-only plugin
manifest.json + ui/ + mcp/      # plugin with a window (UI + MCP)
```

## Plugins

| Plugin | Description |
| --- | --- |
| `files/` | File browser and filesystem MCP tools |
| `kanban/` | Kanban board with project/column/ticket management |
| `notes/` | Simple notes app |
| `terminal/` | Terminal emulator with shell execution MCP tools |

## Architecture

```
mcpkit/              # shared Go module (config, stdio transport, data-file resolution)
notes/mcp/           # notes MCP server (Go)
kanban/mcp/          # kanban MCP server (Go)
files/mcp/           # files MCP server (Go)
terminal/mcp/        # terminal MCP server (Go)
```

Each plugin's `mcp/` folder is an independent Go module that depends on
`mcpkit` via a `replace` directive. The MCP protocol is implemented with
[`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go).

## Usage

These plugins are consumed by NusaShell as a git submodule at `plugins/`.
The shell passes `NUSASHELL_USER_DATA` to each plugin process; plugins manage
their own durable data under `{NUSASHELL_USER_DATA}/plugins-data/{pluginId}/`.

## Build

Each plugin server is a standalone Go binary. Build from the plugin's `mcp/`
folder:

```bash
cd notes/mcp && go build -o server .
cd kanban/mcp && go build -o server .
cd files/mcp && go build -o server .
cd terminal/mcp && go build -o server .
```

The resulting `mcp/server` binary is what `manifest.json` launches
(`"command": "mcp/server", "args": []`).

### Build all

```bash
for p in notes kanban files terminal; do
  (cd "$p/mcp" && go build -o server .)
done
```

## Test

```bash
cd notes/mcp && go test ./...
cd kanban/mcp && go test ./...
cd files/mcp && go test ./...
cd terminal/mcp && go test ./...
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
