# nusashell.notes — Usage Contract

## State

This plugin manages **persistent local notes** stored in NusaShell's plugin data
directory. Notes are independent of conversation history and survive restarts.

- `autostart: true` — the server starts automatically with NusaShell.
- Data is local to the host; nothing is synced to external services.
- Notes are identified by an opaque `id` returned at creation time. IDs must
  not be guessed or constructed — always capture them from `create` or `list`.
- There is no recycle bin: `delete` is **permanent**.

## Side Effects

| Tool | Side Effect |
|------|-------------|
| `create` | Adds a new persistent note; returns the assigned `id` |
| `update` | Overwrites title and/or body in-place |
| `delete` | **Irreversible.** Permanently removes the note |
| `list`, `get`, `search` | Read-only; no side effects |

## Best Practices

1. **List before acting.** Call `list` (or `search`) before `update`/`delete` to
   confirm the correct note `id`. Never hard-code or guess IDs.

2. **Confirm before delete.** `delete` is permanent. Show the note title to the
   user and get explicit confirmation before calling it.

3. **Use search for discovery.** `search` does full-text matching across titles
   and bodies — prefer it over scanning `list` output manually.

4. **Notes ≠ conversation memory.** Notes are user-visible, persistent documents.
   Use NusaShell's built-in `memory_save` / `memory_search` for agent-internal
   working knowledge; use Notes only for content the user explicitly wants saved
   as a note.
