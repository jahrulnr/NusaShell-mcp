# nusashell.terminal — Usage Contract

## State

This plugin manages **real OS processes and PTY sessions** with the permissions of
the user who launched NusaShell. Commands can read or modify any file, open network
connections, install software, or cause irreversible system changes.

- `exec` runs one-shot commands; stdout/stderr are returned when `wait=true` (default).
- `open`/`write`/`read` manage interactive PTY sessions (full terminal emulation).
- Long-running processes launched with `wait=false` persist until explicitly killed
  or the plugin is restarted.
- `keepAliveOnClose: true` in the manifest — the MCP server process survives UI
  panel close; sessions remain active.
- `cwd` defaults to user home, **not** the conversation workspace. Pass an explicit
  absolute `cwd` for every exec call that needs a specific directory.

## Side Effects

| Tool | Side Effect |
|------|-------------|
| `exec` | Runs arbitrary shell command; may modify files, network, system state |
| `open` | Spawns a PTY subprocess; consumes OS resources until `close` is called |
| `write` | Sends input bytes to a live PTY (may trigger commands) |
| `close` | Terminates a PTY session |
| `process_kill` | Sends SIGKILL/SIGTERM to a background process |
| `shells`, `list`, `read`, `resize`, `process_list`, `process_read`, `process_wait` | Read-only / non-destructive |

## Best Practices

1. **Confirm destructive commands.** Before running anything irreversible
   (`rm -rf`, `DROP TABLE`, `git push --force`, package installs, service restarts)
   show the exact command to the user and wait for confirmation.

2. **Always pass absolute `cwd`.** Never rely on the default home directory
   when a specific project directory is needed.

3. **Prefer bash/zsh over sh; prefer pwsh over cmd.exe.** Call `shells` first
   on Windows to confirm what is available before assuming `bash`.

4. **Close sessions when done.** PTY sessions consume file descriptors and
   pseudo-terminals. Call `close` after interactive work; don't leave orphan
   sessions.

5. **Read before assuming output.** For background processes (`wait=false`), call
   `process_read` to capture buffered output; don't assume success from exit.

6. **Don't use exec as a file tool.** Read/write files via the Files plugin
   (`nusashell.files`), not via `cat`/`echo` in exec. Use exec for operations
   that genuinely require shell execution (builds, git, package managers, etc.).

7. **Timeouts are call-wait limits, not process limits.** `timeoutMs` in `exec`
   controls how long the MCP call waits — it does not kill the process. Use
   `process_kill` to stop a runaway process explicitly.

8. **Sensitive env vars.** Values passed in `env` are merged with the parent
   environment. Never log or echo secret values; reference them by key name.
