# nusashell.files — Usage Contract

## State

This plugin operates directly on the **host filesystem** with the permissions of the
process that launched it. It is shared across all conversations and agents — there
is no per-conversation sandbox or workspace isolation.

- Default root for empty `path` = user home directory, **not** the active conversation workspace.
- No containment jail: `../` traversal is permitted. The caller is responsible for
  staying within intended boundaries.
- Symbolic links are followed transparently.
- All mutating operations are atomic (write-to-temp-then-rename). A crash mid-call
  never leaves a partial file.
- `grep`/`search`/`tree` skip `.git`, hidden entries, common build directories
  (`node_modules`, `vendor`, `dist`, `build`, `target`, `__pycache__`, `coverage`,
  `venv`, `out`), and `.gitignore`/`.ignore` patterns by default.

## Side Effects

| Tool | Side Effect |
|------|-------------|
| `write` | Creates or **overwrites** a file; emits `files.modified` |
| `append` | Appends to a file (creates if missing); emits `files.modified` |
| `patch` | Replaces string occurrences in-place; emits `files.modified` |
| `mkdir` | Creates directory and parents |
| `touch` | Creates empty file or updates timestamps; emits `files.modified` |
| `move` | Renames or moves; **overwrites** destination if it exists; emits `files.moved` |
| `copy` | Copies recursively; overwrites destination |
| `delete` | **Irreversible.** Deletes file or directory; emits `files.deleted` |
| Read tools (`read`, `list`, `tree`, `search`, `grep`, `info`, `exists`, `context_map`, `search_relevant`, `list_symbols`, `detect_stack`) | No filesystem side effects |

## Best Practices

1. **Read before writing.** Call `read` (with `start`/`end`) or `exists` before
   patching a file you have not inspected. Never assume file content.

2. **Preview patches.** Call `patch` with `preview=true` first to verify the
   result, then apply with `preview=false`.

3. **Use absolute paths.** Relative paths are rejected. Empty `path` resolves to
   the Files default root (user home), which is often not the workspace — always
   pass an explicit absolute path.

4. **Confirm before destructive ops.** `delete` and `move` (over an existing
   destination) are irreversible. Verify the path with `exists` or `info` first
   and obtain user confirmation.

5. **grep ≠ exec.** `grep` takes a `pattern` (regex) and an absolute `path`.
   Never pass `command` or `cwd` — those belong to the Terminal plugin's `exec`.

6. **Scope search roots.** Pass an explicit directory as `path` to `grep`/`search`
   to avoid scanning the entire home tree.

7. **Pagination for large files.** Use `head`/`tail`/`start`/`end` on `read` and
   `maxBytes` when content may be large. Truncated results include `meta.truncated`.

8. **Disambiguation on patch.** If `old_string` matches multiple locations,
   `patch` fails and returns line numbers. Use `context_before`/`context_after`
   or `occurrence_index` to disambiguate; use `replace_all` only when intentional.
