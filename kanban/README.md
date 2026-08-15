# NusaShell Kanban

A headed, local-first Kanban plugin for NusaShell. The board UI runs in the
plugin window and reaches the SQLite-backed MCP server only through
`window.shell.callTool`; it does not start an HTTP or WebSocket host.

## Build

From the repository root:

```bash
pnpm --filter @nusashell/kanban install
pnpm --filter @nusashell/kanban test
pnpm --filter @nusashell/kanban typecheck
pnpm --filter @nusashell/kanban build
```

The manifest points at `mcp/server.cjs` and `ui/index.html`. The MCP server
keeps its database under the host-provided `NUSASHELL_USER_DATA` directory,
inside `plugins-data/nusashell.kanban/`.

## MCP surface

The plugin exposes project, column, ticket, subtask, and session operations:
`list_projects`, `create_project`, `list_columns`, `list_tickets`,
`get_ticket`, `create_ticket`, `update_ticket`, `move_ticket`,
`delete_ticket`, `create_subtask`, `complete_subtask`, `list_sessions`,
`create_session`, and `delete_session`.

Use the `howto` MCP prompt before planning work. The prompt describes the
safe workflow: discover the project and columns, create tickets with useful
acceptance criteria, move work through the board, and avoid guessing IDs.

## Attribution

The DAL/schema and board UI are adapted from `gablabelle/mcp-kanban`, released
under the MIT License. See [`NOTICE`](./NOTICE).
