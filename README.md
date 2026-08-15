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
| `files/` | File browser and filesystem MCP tools |
| `kanban/` | Kanban board with project/column/ticket management |
| `mail/` | IMAP mail reader with account management |
| `notes/` | Simple notes app |
| `terminal/` | Terminal emulator with shell execution MCP tools |

## Usage

These plugins are consumed by NusaShell as a git submodule at `plugins/`.
The shell passes `NUSASHELL_USER_DATA` to each plugin process; plugins manage
their own durable data under `{NUSASHELL_USER_DATA}/plugins-data/{pluginId}/`.

## Development

Each plugin has its own `package.json` with build and test scripts. From any
plugin folder:

```bash
npm install
npm run build   # esbuild bundle
npm test        # vitest
```
