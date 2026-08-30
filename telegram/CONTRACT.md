# nusashell.telegram — Usage Contract

## State

This plugin manages a **Telegram channel** connected via Bot API
(Bot account, not user account). Messages, chat metadata, and
approval state are persisted locally in SQLite.

- Data is local to the host; nothing is synced to Telegram servers
  beyond normal Bot API traffic.
- `chat_id` is a string (int64-as-string) for precision and
  consistency with the WhatsApp plugin's `chat_jid` convention.
- `message_id` is a number (Telegram message ID).
- There is no undo for sent messages or callback responses.
- History is **incremental only** — the bot cannot read messages
  sent before it was added to the chat.

## Side Effects

| Tool | Side Effect |
|------|-------------|
| `login` | Validates token via `getMe`, stores token (file mode 0600) |
| `logout` | Clears stored token, disconnects |
| `send_message` | Sends a real message to a Telegram chat; mirrors the message into the local store so the UI shows it instantly |
| `send_media` | Uploads and sends a file; mirrors the caption/label into the local store |
| `send_inline_buttons` | Sends a message with inline keyboard buttons; mirrors the text into the local store |
| `edit_message` | Edits an existing message (text or media) |
| `delete_message` | Deletes a message from a chat |
| `answer_callback` | Answers a callback query (approval/deny); resolves the matching pending approval in the local store (`approved`/`denied` derived from the notification text) |
| `send_chat_action` | Sends a typing/uploading indicator |
| `get_messages` | Reads messages from the local store; marks the chat read locally (clears the unread badge — no Telegram call) |
| `list_chats` | Lists tracked chats from the local store |
| `list_pending_approvals` | Lists pending approval requests |
| `get_chat` | Gets chat metadata |
| `get_chat_history` | Gets the full history of a chat |
| `request_sync` | Requests history sync (future; Bot API limitation) |
| `search_messages` | Full-text search across stored messages (FTS5) |
| `add_to_allowlist` | Adds a user/chat to the allowlist |
| `remove_from_allowlist` | Removes a user/chat from the allowlist |
| `set_privacy_mode` | Toggles local allowlist enforcement |
| `status` | Reports bot status, connection state, database counts, and the allowlist |

**All send tools deliver real messages to real people.** Confirm
the chat and content before sending.

## Best Practices

1. **Always login first.** Call `status` — if not connected, call
   `login` with a valid Bot API token before any other operation.

2. **Respect privacy mode.** In groups, the bot only sees messages
   mentioning it or sent as replies. To see all messages, disable
   privacy mode via BotFather (`/setprivacy`) and re-add the bot.

3. **Handle 429 rate limits gracefully.** The bot API limits
   1 msg/s per chat, 20/min per group, ~30/s global. On 429,
   respect the `retry_after` field and retry.

4. **No history before bot joined.** The store is built incrementally
   from incoming updates. Do not expect messages from before the
   bot was added to the chat.

5. **Use `chat_id` as string.** Telegram user/group IDs are int64.
   Pass as string (e.g. `"-1001234567890"`) to avoid precision loss
   in JSON/JS/LLM tool parameters.

6. **Confirm before send.** Send tools deliver real messages.
   Always show the target chat and message preview to the user
   before executing.

7. **Approval flow.** Use `send_inline_buttons` to create an
   approval prompt. The user responds via `answer_callback` with
   the callback_data. Pending approvals are tracked in the store
   and surfaced by `list_pending_approvals`.

8. **Streaming via `edit_message`.** For long AI responses, send a
   preview then `edit_message` repeatedly. The UI updates the
   existing message (by `message_id`), not inserting duplicates.

9. **Media limits.** Download limit 20 MB, upload limit 50 MB
   (cloud Bot API). Self-hosted Bot API server lifts download
   limit (2 GB upload).
