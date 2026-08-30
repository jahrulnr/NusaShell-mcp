# nusashell.whatsapp

WhatsApp MCP plugin for [NusaShell](https://github.com/jahrulnr/NusaShell).

Links your personal WhatsApp account as a Web "linked device" via
[whatsmeow](https://github.com/tulir/whatsmeow) and exposes your messages,
contacts, and groups through MCP tools — letting NusaShell agents read,
search, and send WhatsApp messages on your behalf.

> **Unofficial.** This uses `whatsmeow`, an unofficial reimplementation of the
> WhatsApp Web multidevice protocol. Use at your own risk; WhatsApp may
> rate-limit or ban accounts that misuse automation. No bulk/spam/marketing.

## Install

In NusaShell, install via the plugin manager or manually:

```bash
openclaw plugins install clawhub:@openclaw/whatsapp  # TODO: NusaShell registry
```

Or build from source:

```bash
cd whatsapp/mcp && go build -o server .
```

## Quick start

1. Open the WhatsApp plugin window in NusaShell.
2. Click **Link WhatsApp** — a QR code appears.
3. Scan it with your phone (WhatsApp → Settings → Linked Devices → Link a Device).
4. Once linked, the chat list loads. Use the MCP tools or the UI to interact.

## Tools

| Tool | Purpose |
|------|---------|
| `status` | Connection state, linked JID, ingestion freshness, DB row counts |
| `login` | Start QR pairing flow; returns QR code string |
| `pair_with_code` | Start phone-number pairing flow; returns 8-char code (alternative to QR) |
| `logout` | Disconnect and clear WhatsApp auth state |
| `list_chats` | Recent chats, newest activity first; optional `kind` filter (`dm`/`group`) |
| `get_chat` | Chat metadata by JID (group participants for groups) |
| `list_contacts` | Substring search across push name, business name, phone |
| `list_groups` | Substring search over groups you're in |
| `send_message` | Send text to a chat (optional `reply_to_id`) |
| `send_media` | Send a file from disk (image/video/audio/document) |
| `react` | Add or remove a reaction on a message |
| `mark_read` | Mark a chat read up to a message ID |
| `get_messages` | Page through a chat, newest first; cursor pagination |
| `search_messages` | FTS5 full-text search across all message text and captions |
| `download_media` | Fetch + cache a media blob; return local path |
| `request_sync` | Per-chat history backfill from WhatsApp servers |

## Data

```
{NUSASHELL_USER_DATA}/plugins-data/nusashell.whatsapp/
  session.db    # whatsmeow session and device keys
  whatsapp.db   # application DB: messages, contacts, groups, media metadata (WAL + FTS5)
  media/        # downloaded media blobs, sha256-named
```

## Build

```bash
cd whatsapp/mcp && go build -o server .
```

## Test

```bash
cd whatsapp/mcp && go test ./...
```

## License

MIT — see [LICENSE](../LICENSE).
