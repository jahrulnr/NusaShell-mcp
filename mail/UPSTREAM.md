# Upstream

The NusaShell Mail plugin was bootstrapped from the architecture and behavior
of:

- Project: `codefuturist/email-mcp`
- Source: https://github.com/codefuturist/email-mcp
- Pinned revision: `99ce431aa81dd4cafc2879bd35b6ee3acd0f2d74`
- Upstream license: LGPL-3.0-or-later

## Adaptation

NusaShell retained the upstream project's useful separation between account
configuration, connection management, IMAP services, and MCP tool
registration. The implementation was reduced to the first read-only milestone
and adapted to NusaShell's broker architecture:

- tool names use the explicit `mail_*` contract;
- account CRUD is owned by the plugin UI;
- credentials are encrypted by the Electron host and injected only when the
  MCP process starts;
- credentials never appear in MCP tool schemas or results;
- scheduler, calendar integration, AI triage, direct send, desktop
  notifications, and webhook behavior are intentionally excluded;
- the UI is an original NusaShell surface and does not copy another mail
  client's source.

When importing additional upstream code, keep this revision record current and
retain the upstream copyright and license notices.
