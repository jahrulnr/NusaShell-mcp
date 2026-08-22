# nusashell.kanban — Usage Contract

## State

This plugin manages a **local Kanban board** stored in NusaShell's plugin data
directory. Projects, columns, tickets, and subtasks are persisted across restarts.

- Data is local to the host; nothing is synced to external services.
- Projects, columns, and tickets carry opaque IDs — never guess or construct them.
  Always obtain IDs from `list_projects`, `list_columns`, or `list_tickets`.
- There is no undo for `delete_ticket`, `delete_subtask`, or project deletion.
- Column names (e.g. "In Progress", "Done") vary per project — do not hard-code
  column IDs across projects.

## Side Effects

| Tool | Side Effect |
|------|-------------|
| `create_ticket` | Adds a new ticket to a column; returns its `id` |
| `update_ticket` | Overwrites ticket fields in-place |
| `move_ticket` | Changes the ticket's column |
| `delete_ticket` | **Irreversible.** Permanently removes the ticket |
| `create_subtask` | Adds a subtask under a ticket |
| `complete_subtask` | Marks a subtask done |
| `delete_subtask` | **Irreversible.** Permanently removes the subtask |
| `list_projects`, `list_columns`, `list_tickets`, `get_ticket` | Read-only; no side effects |

## Best Practices

1. **Always resolve IDs first.** Call `list_projects` → `list_columns` → `list_tickets`
   before any mutating call. Never reuse IDs from a previous session without
   re-fetching — the board state may have changed.

2. **Reflect work state in real time.** Before starting work on a ticket, call
   `move_ticket` to move it to "In Progress". Move to "Done" only after acceptance
   criteria are met — not just when coding is finished.

3. **Keep descriptions useful.** Write ticket descriptions for humans who will
   read them later; include context, acceptance criteria, and blockers, not just
   a one-line label.

4. **Use subtasks for concrete steps.** Break large tickets into subtasks via
   `create_subtask` and call `complete_subtask` as each step finishes.

5. **Confirm before delete.** Show the ticket title to the user and get explicit
   confirmation before calling `delete_ticket` or `delete_subtask`.

6. **Update on blockers.** When work is blocked, call `update_ticket` to record
   the blocker in the description before moving on to other work.
