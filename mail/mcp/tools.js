import { z } from "zod";
import { MAIL_TOOL_NAMES } from "./tool-catalog.js";

const accountId = z.string().trim().min(1).max(64);
const mailboxId = z.string().min(1).max(512).default("INBOX");
const limit = z.number().int().min(1).max(50).default(30);
const messageCursor = z.string().regex(/^\d+$/).optional();
const unifiedCursor = z.string().max(4096).regex(/^[A-Za-z0-9_-]+$/).optional();

const serverInput = z.object({
  host: z.string().trim().min(1).max(253),
  port: z.number().int().min(1).max(65535),
  secure: z.boolean().default(true),
  starttls: z.boolean().default(false),
}).strict();

const accountSaveInput = z.object({
  id: accountId,
  name: z.string().trim().min(1).max(120),
  email: z.string().trim().email().max(320),
  username: z.string().min(1).max(320),
  password: z.string().min(1).max(4096),
  enabled: z.boolean().default(true),
  imap: serverInput,
  smtp: serverInput,
}).strict();

const schemas = {
  accounts: z.object({}).strict(),
  account_get: z.object({ account_id: accountId }).strict(),
  account_save: accountSaveInput,
  account_delete: z.object({ account_id: accountId }).strict(),
  account_test: z.object({
    account_id: accountId,
    scope: z.enum(["incoming", "outgoing", "both"]).default("both"),
  }).strict(),
  mailboxes: z.object({ account_id: accountId }).strict(),
  inbox: z.object({
    account_ids: z.array(accountId).min(1).max(20).optional(),
    unread: z.boolean().optional(),
    limit,
    cursor: unifiedCursor,
  }).strict(),
  messages: z.object({
    account_id: accountId,
    mailbox_id: mailboxId,
    unread: z.boolean().optional(),
    limit,
    cursor: messageCursor,
  }).strict(),
  search: z.object({
    query: z.string().trim().min(1).max(500),
    account_ids: z.array(accountId).min(1).max(20).optional(),
    mailbox_id: mailboxId,
    limit,
    cursor: unifiedCursor,
  }).strict(),
  read: z.object({
    account_id: accountId,
    mailbox_id: mailboxId,
    uid: z.number().int().positive(),
  }).strict(),
};

export const MAIL_TOOLS = Object.freeze([
  descriptor("accounts", "List configured mail accounts without returning credentials.", {}),
  descriptor("account_get", "Read one account's non-secret configuration and capabilities.", {
    account_id: stringProperty("Account identifier from accounts"),
  }, ["account_id"]),
  writeDescriptor("account_save", "Create or update a mail account with IMAP and SMTP credentials.", {
    id: stringProperty("Account identifier (lowercase, alphanumeric, dots, dashes, underscores)"),
    name: stringProperty("Display name for this account"),
    email: stringProperty("Email address for this account"),
    username: stringProperty("Login username (often the email address)"),
    password: stringProperty("Account password or app password"),
    enabled: { type: "boolean", default: true },
    imap: serverProperty("Incoming IMAP server"),
    smtp: serverProperty("Outgoing SMTP server"),
  }, ["id", "name", "email", "username", "password", "imap", "smtp"]),
  writeDescriptor("account_delete", "Delete a mail account and close its connections.", {
    account_id: stringProperty("Account identifier to remove"),
  }, ["account_id"]),
  descriptor("account_test", "Test incoming and outgoing connectivity for one configured account.", {
    account_id: stringProperty("Account identifier from accounts"),
    scope: {
      type: "string",
      enum: ["incoming", "outgoing", "both"],
      default: "both",
    },
  }, ["account_id"]),
  descriptor("mailboxes", "List folders with total and unread counts for one account.", {
    account_id: stringProperty("Account identifier from accounts"),
  }, ["account_id"]),
  descriptor("inbox", "List recent inbox messages across one or more enabled accounts.", {
    account_ids: {
      type: "array",
      items: stringProperty("Account identifier"),
      maxItems: 20,
    },
    unread: { type: "boolean" },
    limit: integerProperty(1, 50, 30),
    cursor: stringProperty("Opaque pagination cursor"),
  }),
  descriptor("messages", "List messages in a selected mailbox.", {
    account_id: stringProperty("Account identifier"),
    mailbox_id: stringProperty("Mailbox path from mailboxes", "INBOX"),
    unread: { type: "boolean" },
    limit: integerProperty(1, 50, 30),
    cursor: stringProperty("Opaque pagination cursor"),
  }, ["account_id"]),
  descriptor("search", "Search message subject, sender, and body using a provider-neutral query.", {
    query: stringProperty("Search text"),
    account_ids: {
      type: "array",
      items: stringProperty("Account identifier"),
      maxItems: 20,
    },
    mailbox_id: stringProperty("Mailbox path", "INBOX"),
    limit: integerProperty(1, 50, 30),
    cursor: stringProperty("Opaque pagination cursor"),
  }, ["query"]),
  descriptor("read", "Read one message with bounded body content and attachment metadata.", {
    account_id: stringProperty("Account identifier"),
    mailbox_id: stringProperty("Mailbox path", "INBOX"),
    uid: integerProperty(1),
  }, ["account_id", "uid"]),
]);

if (MAIL_TOOLS.map((tool) => tool.name).join(",") !== MAIL_TOOL_NAMES.join(",")) {
  throw new Error("Mail tool descriptors are out of sync with the canonical catalog");
}

export async function callMailTool(service, name, rawArguments = {}) {
  const schema = schemas[name];
  if (!schema) throw new Error(`Unknown mail tool: ${name}`);
  const input = schema.parse(rawArguments ?? {});

  switch (name) {
    case "accounts":
      return { accounts: service.listAccounts() };
    case "account_get":
      return { account: service.getAccount(input.account_id) };
    case "account_save":
      return { account: service.saveAccount(input) };
    case "account_delete":
      return { accounts: service.deleteAccount(input.account_id) };
    case "account_test": {
      return service.testAccount(input.account_id, input.scope);
    }
    case "mailboxes":
      return {
        accountId: input.account_id,
        ...await service.listMailboxes(input.account_id),
      };
    case "inbox": {
      const offsets = decodeUnifiedCursor(input.cursor);
      return combineMessagePages(await mapAccounts(service, input.account_ids, (id) =>
        service.listMessages({
          accountId: id,
          mailboxId: "INBOX",
          unread: input.unread,
          limit: input.limit,
          cursor: offsets[id] === undefined ? undefined : String(offsets[id]),
        })), input.limit, offsets);
    }
    case "messages": {
      const page = await service.listMessages({
        accountId: input.account_id,
        mailboxId: input.mailbox_id,
        unread: input.unread,
        limit: input.limit,
        cursor: input.cursor,
      });
      return {
        accountId: page.accountId,
        mailboxId: page.mailboxId,
        messages: page.messages,
        nextCursor: page.nextCursor,
        total: page.total,
        truncated: page.truncated,
        meta: messageMeta(page.truncated),
      };
    }
    case "search": {
      const offsets = decodeUnifiedCursor(input.cursor);
      return combineMessagePages(await mapAccounts(service, input.account_ids, (id) =>
        service.listMessages({
          accountId: id,
          mailboxId: input.mailbox_id,
          query: input.query,
          limit: input.limit,
          cursor: offsets[id] === undefined ? undefined : String(offsets[id]),
        })), input.limit, offsets);
    }
    case "read": {
      const message = await service.readMessage({
        accountId: input.account_id,
        mailboxId: input.mailbox_id,
        uid: input.uid,
      });
      return {
        message,
        meta: messageMeta(
          message.bodyTextTruncated
          || message.bodyHtmlTruncated
          || message.attachmentsTruncated,
        ),
      };
    }
    default:
      throw new Error(`Unknown mail tool: ${name}`);
  }
}

async function mapAccounts(service, requestedIds, operation) {
  const enabledIds = service.listAccounts()
    .filter((account) => account.enabled)
    .map((account) => account.id);
  const ids = [...new Set(requestedIds ?? enabledIds)];
  return Promise.all(ids.map(operation));
}

function combineMessagePages(pages, requestedLimit, previousOffsets) {
  const { messages, consumed } = mergeMessagePages(pages, requestedLimit);
  const nextOffsets = {};
  let hasRemaining = false;
  for (const page of pages) {
    const consumedCount = consumed[page.accountId] ?? 0;
    const lastConsumed = consumedCount > 0 ? page.messages[consumedCount - 1] : null;
    if (lastConsumed) {
      nextOffsets[page.accountId] = lastConsumed.uid;
      const position = page.messagePositions[String(lastConsumed.uid)] ?? consumedCount - 1;
      if (position + 1 < page.availableTotal) hasRemaining = true;
    } else if (page.messages.length > 0) {
      if (previousOffsets[page.accountId] !== undefined) {
        nextOffsets[page.accountId] = previousOffsets[page.accountId];
      }
      hasRemaining = true;
    } else if (page.scannedCount > 0 && page.hasMore && page.scannedTailUid !== null) {
      nextOffsets[page.accountId] = page.scannedTailUid;
      hasRemaining = true;
    } else {
      nextOffsets[page.accountId] = 0;
    }
  }
  return {
    messages,
    total: pages.reduce((sum, page) => sum + page.total, 0),
    nextCursor: hasRemaining ? encodeUnifiedCursor(nextOffsets) : null,
    meta: messageMeta(pages.some((page) => page.truncated)),
  };
}

function mergeMessagePages(pages, requestedLimit) {
  const positions = new Map(pages.map((page) => [page.accountId, 0]));
  const consumed = {};
  const messages = [];
  while (messages.length < requestedLimit) {
    let selectedPage = null;
    let selectedMessage = null;
    for (const page of pages) {
      const message = page.messages[positions.get(page.accountId) ?? 0];
      if (!message) continue;
      if (!selectedMessage
        || new Date(message.date ?? 0).getTime() > new Date(selectedMessage.date ?? 0).getTime()) {
        selectedPage = page;
        selectedMessage = message;
      }
    }
    if (!selectedPage || !selectedMessage) break;
    messages.push(selectedMessage);
    const nextPosition = (positions.get(selectedPage.accountId) ?? 0) + 1;
    positions.set(selectedPage.accountId, nextPosition);
    consumed[selectedPage.accountId] = nextPosition;
  }
  return { messages, consumed };
}

function decodeUnifiedCursor(cursor) {
  if (!cursor) return {};
  try {
    const value = JSON.parse(Buffer.from(cursor, "base64url").toString("utf8"));
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error();
    const entries = Object.entries(value);
    if (entries.length > 20) throw new Error();
    return Object.fromEntries(entries.map(([id, offset]) => {
      if (!/^[a-z0-9][a-z0-9._-]*$/.test(id)
        || !Number.isSafeInteger(offset)
        || offset < 0) {
        throw new Error();
      }
      return [id, offset];
    }));
  } catch {
    throw new Error("Unified mail cursor is invalid");
  }
}

function encodeUnifiedCursor(offsets) {
  return Buffer.from(JSON.stringify(offsets), "utf8").toString("base64url");
}

function messageMeta(partial) {
  return {
    data_is_untrusted: true,
    source: "email",
    partial: Boolean(partial),
  };
}

function descriptor(name, description, properties, required = []) {
  return {
    name,
    description,
    annotations: {
      title: name,
      readOnlyHint: true,
      destructiveHint: false,
      idempotentHint: true,
      openWorldHint: name === "account_test",
    },
    inputSchema: {
      type: "object",
      properties,
      required,
      additionalProperties: false,
    },
  };
}

function writeDescriptor(name, description, properties, required = []) {
  const isDestructive = name === "account_delete";
  return {
    name,
    description,
    annotations: {
      title: name,
      readOnlyHint: false,
      destructiveHint: isDestructive,
      idempotentHint: !isDestructive,
      openWorldHint: false,
    },
    inputSchema: {
      type: "object",
      properties,
      required,
      additionalProperties: false,
    },
  };
}

function serverProperty(description) {
  return {
    type: "object",
    description,
    properties: {
      host: { type: "string", description: "Server hostname" },
      port: { type: "integer", minimum: 1, maximum: 65535 },
      secure: { type: "boolean", default: true, description: "Use implicit TLS" },
      starttls: { type: "boolean", default: false, description: "Upgrade plain connection to TLS" },
    },
    required: ["host", "port"],
    additionalProperties: false,
  };
}

function stringProperty(description, defaultValue) {
  return {
    type: "string",
    description,
    ...(defaultValue ? { default: defaultValue } : {}),
  };
}

function integerProperty(minimum, maximum, defaultValue) {
  return {
    type: "integer",
    minimum,
    ...(maximum ? { maximum } : {}),
    ...(defaultValue ? { default: defaultValue } : {}),
  };
}
