import { simpleParser } from "mailparser";
import { publicAccount, resolveAccount } from "./config.js";
import {
  deleteAccount as deleteAccountFromStore,
  loadAccounts,
  saveAccount as saveAccountToStore,
} from "./account-store.js";

const MAX_BODY_CHARS = 100_000;
const MAX_SOURCE_BYTES = 5 * 1024 * 1024;
const MAX_MAILBOXES = 200;
const MAILBOX_STATUS_CONCURRENCY = 10;
const MAX_MATCHED_UIDS = 10_000;
const MAX_ADDRESSES = 100;
const MAX_ATTACHMENTS = 100;
const MAX_MIME_NODES = 1_000;
const MAX_HEADER_CHARS = 2_000;

export class MailService {
  constructor(accounts, connections) {
    this.accounts = accounts;
    this.connections = connections;
  }

  listAccounts() {
    return this.accounts.map(publicAccount);
  }

  getAccount(accountId) {
    return publicAccount(resolveAccount(this.accounts, accountId));
  }

  saveAccount(input) {
    const saved = saveAccountToStore(input);
    this.accounts = loadAccounts();
    this.connections.reloadAccounts(this.accounts);
    return saved;
  }

  deleteAccount(accountId) {
    const remaining = deleteAccountFromStore(accountId);
    this.accounts = loadAccounts();
    this.connections.dropAccount(accountId);
    return remaining;
  }

  testAccount(accountId, scope = "both") {
    resolveAccount(this.accounts, accountId);
    return this.connections.testAccount(accountId, scope);
  }

  async listMailboxes(accountId) {
    resolveAccount(this.accounts, accountId);
    const client = await this.connections.getImapClient(accountId);
    const allMailboxes = await client.list();
    const selectable = allMailboxes.filter((mb) => !isNonSelectable(mb));
    const mailboxes = selectable.slice(0, MAX_MAILBOXES);
    const statuses = [];
    for (let offset = 0; offset < mailboxes.length; offset += MAILBOX_STATUS_CONCURRENCY) {
      const batch = mailboxes.slice(offset, offset + MAILBOX_STATUS_CONCURRENCY);
      statuses.push(...await Promise.allSettled(batch.map(async (mailbox) => {
        const status = await client.status(mailbox.path, {
          messages: true,
          unseen: true,
        });
        return {
          id: boundHeader(mailbox.path),
          name: boundHeader(mailbox.name || mailbox.path),
          specialUse: mailbox.specialUse ? boundHeader(mailbox.specialUse) : null,
          total: status.messages ?? 0,
          unread: status.unseen ?? 0,
        };
      })));
    }

    return {
      mailboxes: statuses.map((status, index) => status.status === "fulfilled"
        ? status.value
        : {
            id: boundHeader(mailboxes[index].path),
            name: boundHeader(mailboxes[index].name || mailboxes[index].path),
            specialUse: mailboxes[index].specialUse
              ? boundHeader(mailboxes[index].specialUse)
              : null,
            total: 0,
            unread: 0,
          }),
      truncated: allMailboxes.length > MAX_MAILBOXES,
    };
  }

  async listMessages(input) {
    const account = resolveAccount(this.accounts, input.accountId);
    const mailboxId = validateMailbox(input.mailboxId ?? "INBOX");
    const limit = normalizeLimit(input.limit);
    const beforeUid = parseCursor(input.cursor);
    const client = await this.connections.getImapClient(account.id);
    const lock = await client.getMailboxLock(mailboxId);
    try {
      const criteria = createSearchCriteria(input);
      const found = await client.search(criteria, { uid: true });
      const matchedUids = Array.isArray(found) ? found : [];
      const truncated = matchedUids.length > MAX_MATCHED_UIDS;
      const boundedUids = matchedUids.slice(-MAX_MATCHED_UIDS).sort((a, b) => b - a);
      const uids = beforeUid === null
        ? boundedUids
        : beforeUid === 0
          ? []
          : boundedUids.filter((uid) => uid < beforeUid);
      const pageUids = uids.slice(0, limit);
      if (pageUids.length === 0) {
        return {
          accountId: account.id,
          mailboxId,
          messages: [],
          nextCursor: null,
          total: matchedUids.length,
          availableTotal: uids.length,
          scannedCount: 0,
          scannedTailUid: null,
          hasMore: false,
          truncated,
        };
      }

      const messages = [];
      for await (const message of client.fetch(
        pageUids.join(","),
        {
          uid: true,
          envelope: true,
          flags: true,
          bodyStructure: true,
          source: { start: 0, maxLength: 1024 },
        },
        { uid: true },
      )) {
        messages.push(messageSummary(account.id, mailboxId, message));
      }
      messages.sort((left, right) => right.uid - left.uid);
      const messagePositions = Object.fromEntries(
        pageUids.map((uid, index) => [String(uid), index]),
      );

      return {
        accountId: account.id,
        mailboxId,
        messages,
        nextCursor: pageUids.length < uids.length
          ? String(pageUids[pageUids.length - 1])
          : null,
        total: matchedUids.length,
        availableTotal: uids.length,
        scannedCount: pageUids.length,
        scannedTailUid: pageUids[pageUids.length - 1] ?? null,
        hasMore: pageUids.length < uids.length,
        messagePositions,
        truncated,
      };
    } finally {
      lock.release();
    }
  }

  async readMessage(input) {
    const account = resolveAccount(this.accounts, input.accountId);
    const mailboxId = validateMailbox(input.mailboxId ?? "INBOX");
    const uid = Number(input.uid);
    if (!Number.isInteger(uid) || uid <= 0) throw new Error("Message uid must be a positive integer");

    const client = await this.connections.getImapClient(account.id);
    const lock = await client.getMailboxLock(mailboxId);
    try {
      const raw = await client.fetchOne(
        String(uid),
        {
          uid: true,
          envelope: true,
          flags: true,
          bodyStructure: true,
          source: { start: 0, maxLength: MAX_SOURCE_BYTES },
        },
        { uid: true },
      );
      if (!raw) throw new Error(`Message ${uid} was not found in ${mailboxId}`);

      const summary = messageSummary(account.id, mailboxId, raw);
      const parsed = raw.source ? await simpleParser(raw.source) : null;
      return {
        ...summary,
        to: addressList(parsed?.to?.value ?? raw.envelope?.to),
        cc: addressList(parsed?.cc?.value ?? raw.envelope?.cc),
        replyTo: addressList(parsed?.replyTo?.value),
        messageId: parsed?.messageId ?? raw.envelope?.messageId ?? null,
        inReplyTo: parsed?.inReplyTo ?? raw.envelope?.inReplyTo ?? null,
        bodyText: boundText(parsed?.text),
        bodyTextTruncated: textWasTruncated(parsed?.text),
        bodyHtml: boundText(typeof parsed?.html === "string" ? parsed.html : null),
        bodyHtmlTruncated: textWasTruncated(
          typeof parsed?.html === "string" ? parsed.html : null,
        ),
        attachments: (parsed?.attachments ?? []).slice(0, MAX_ATTACHMENTS).map((attachment) => ({
          filename: boundHeader(attachment.filename ?? "attachment"),
          contentType: boundHeader(attachment.contentType),
          size: attachment.size,
        })),
        attachmentsTruncated: (parsed?.attachments?.length ?? 0) > MAX_ATTACHMENTS,
      };
    } finally {
      lock.release();
    }
  }
}

function createSearchCriteria(input) {
  const criteria = {};
  if (input.unread === true) criteria.seen = false;
  if (input.from) criteria.from = boundedQuery(input.from);
  if (input.subject) criteria.subject = boundedQuery(input.subject);
  if (input.query) {
    const query = boundedQuery(input.query);
    criteria.or = [{ subject: query }, { from: query }, { body: query }];
  }
  return Object.keys(criteria).length > 0 ? criteria : { all: true };
}

function boundedQuery(value) {
  const normalized = String(value).trim();
  if (!normalized || normalized.length > 500) {
    throw new Error("Mail search query must contain between 1 and 500 characters");
  }
  return normalized;
}

function normalizeLimit(value) {
  const limit = value === undefined ? 30 : Number(value);
  if (!Number.isInteger(limit) || limit < 1 || limit > 50) {
    throw new Error("Message limit must be an integer between 1 and 50");
  }
  return limit;
}

function parseCursor(value) {
  if (value === undefined || value === null || value === "") return null;
  if (!/^\d+$/.test(String(value))) throw new Error("Message cursor is invalid");
  const uid = Number(value);
  if (!Number.isSafeInteger(uid)) throw new Error("Message cursor is invalid");
  return uid;
}

function validateMailbox(value) {
  const mailbox = String(value);
  if (!mailbox || mailbox.length > 512 || /[\u0000-\u001f\u007f]/.test(mailbox)) {
    throw new Error("Mailbox name is invalid");
  }
  return mailbox;
}

function messageSummary(accountId, mailboxId, message) {
  const envelope = message.envelope ?? {};
  const flags = message.flags instanceof Set ? message.flags : new Set(message.flags ?? []);
  return {
    accountId,
    mailboxId,
    uid: Number(message.uid),
    subject: boundHeader(envelope.subject || "(no subject)"),
    from: address(envelope.from?.[0]),
    to: addressList(envelope.to),
    date: safeDate(envelope.date),
    unread: !flags.has("\\Seen"),
    starred: flags.has("\\Flagged"),
    answered: flags.has("\\Answered"),
    hasAttachments: hasAttachment(message.bodyStructure),
    excerpt: sourceExcerpt(message.source),
  };
}

function address(value) {
  return {
    name: value?.name ? boundHeader(value.name) : null,
    address: boundHeader(value?.address || "unknown"),
  };
}

function addressList(values) {
  return Array.isArray(values) ? values.slice(0, MAX_ADDRESSES).map(address) : [];
}

function hasAttachment(node) {
  const pending = node && typeof node === "object" ? [node] : [];
  let visited = 0;
  while (pending.length > 0 && visited < MAX_MIME_NODES) {
    const current = pending.pop();
    visited += 1;
    if (String(current?.disposition ?? "").toLowerCase() === "attachment") return true;
    if (Array.isArray(current?.childNodes)) {
      pending.push(...current.childNodes.slice(0, MAX_MIME_NODES - visited));
    }
  }
  return false;
}

function sourceExcerpt(source) {
  if (!Buffer.isBuffer(source)) return null;
  const raw = source.toString("utf8");
  const bodyStart = raw.search(/\r?\n\r?\n/);
  if (bodyStart < 0) return null;
  return raw.slice(bodyStart).replace(/\s+/g, " ").trim().slice(0, 240) || null;
}

function boundText(value) {
  if (!value) return null;
  return String(value).slice(0, MAX_BODY_CHARS);
}

function textWasTruncated(value) {
  return Boolean(value && String(value).length > MAX_BODY_CHARS);
}

function boundHeader(value) {
  return String(value ?? "").slice(0, MAX_HEADER_CHARS);
}

function isNonSelectable(mailbox) {
  const flags = mailbox?.flags;
  if (!flags) return false;
  const set = flags instanceof Set ? flags : new Set(flags);
  return set.has("\\Noselect") || set.has("\\NonExistent");
}

function safeDate(value) {
  const date = value ? new Date(value) : new Date(0);
  return Number.isNaN(date.getTime()) ? new Date(0).toISOString() : date.toISOString();
}
