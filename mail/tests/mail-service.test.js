import { describe, expect, it, vi } from "vitest";
import { MailService } from "../mcp/mail-service.js";

const account = {
  id: "work",
  name: "Work",
  email: "me@example.com",
  username: "me@example.com",
  password: "app-password",
  enabled: true,
  imap: { host: "imap.example.com", port: 993, secure: true },
  smtp: { host: "smtp.example.com", port: 465, secure: true },
};

function fakeClient(messages) {
  return {
    list: vi.fn(async () => [
      { name: "Inbox", path: "INBOX", specialUse: "\\Inbox" },
      { name: "[Gmail]", path: "[Gmail]", flags: new Set(["\\Noselect", "\\HasChildren"]) },
      { name: "Archive", path: "Archive" },
    ]),
    status: vi.fn(async (path) => ({
      messages: path === "INBOX" ? 12 : 5,
      unseen: path === "INBOX" ? 3 : 0,
    })),
    search: vi.fn(async () => messages.map((message) => message.uid)),
    fetch: vi.fn(async function* (range) {
      const wanted = new Set(String(range).split(",").map(Number));
      for (const message of messages) {
        if (wanted.has(message.uid)) yield message;
      }
    }),
    fetchOne: vi.fn(async (uid) =>
      messages.find((message) => message.uid === Number(uid)) ?? false),
    getMailboxLock: vi.fn(async () => ({ release: vi.fn() })),
  };
}

function serviceFor(messages) {
  const client = fakeClient(messages);
  return {
    client,
    service: new MailService([account], {
      getImapClient: vi.fn(async () => client),
      testAccount: vi.fn(),
    }),
  };
}

describe("MailService", () => {
  it("lists mailboxes with message and unread counts", async () => {
    const { service } = serviceFor([]);

    await expect(service.listMailboxes("work")).resolves.toEqual({
      mailboxes: [
        {
          id: "INBOX",
          name: "Inbox",
          specialUse: "\\Inbox",
          total: 12,
          unread: 3,
        },
        {
          id: "Archive",
          name: "Archive",
          specialUse: null,
          total: 5,
          unread: 0,
        },
      ],
      truncated: false,
    });
  });

  it("returns newest messages first with a bounded page", async () => {
    const messages = [1, 2, 3].map((uid) => ({
      uid,
      envelope: {
        subject: `Message ${uid}`,
        from: [{ name: "Sender", address: "sender@example.com" }],
        to: [{ address: "me@example.com" }],
        date: new Date(`2026-07-${20 + uid}T10:00:00Z`),
        messageId: `<${uid}@example.com>`,
      },
      flags: uid === 3 ? new Set() : new Set(["\\Seen"]),
      bodyStructure: uid === 2
        ? { disposition: "attachment", dispositionParameters: { filename: "report.pdf" } }
        : null,
    }));
    const { service } = serviceFor(messages);

    const result = await service.listMessages({
      accountId: "work",
      mailboxId: "INBOX",
      limit: 2,
    });

    expect(result.messages.map((message) => message.uid)).toEqual([3, 2]);
    expect(result.messages[0]).toEqual(expect.objectContaining({
      subject: "Message 3",
      unread: true,
      from: { name: "Sender", address: "sender@example.com" },
    }));
    expect(result.messages[1].hasAttachments).toBe(true);
    expect(result.nextCursor).toBe("2");
    expect(result.availableTotal).toBe(3);
    expect(result.scannedCount).toBe(2);
  });

  it("parses a full MIME message without marking it as read", async () => {
    const source = Buffer.from([
      "From: Sender <sender@example.com>",
      "To: Me <me@example.com>",
      "Subject: A useful update",
      "Message-ID: <read@example.com>",
      "Content-Type: text/plain; charset=utf-8",
      "",
      "Hello from the message body.",
    ].join("\r\n"));
    const { service } = serviceFor([{
      uid: 42,
      envelope: {
        subject: "A useful update",
        from: [{ name: "Sender", address: "sender@example.com" }],
        to: [{ name: "Me", address: "me@example.com" }],
        date: new Date("2026-07-30T08:00:00Z"),
        messageId: "<read@example.com>",
      },
      flags: new Set(),
      bodyStructure: null,
      source,
    }]);

    const message = await service.readMessage({
      accountId: "work",
      mailboxId: "INBOX",
      uid: 42,
    });

    expect(message.bodyText).toContain("Hello from the message body.");
    expect(message.bodyHtml).toBeNull();
    expect(message.unread).toBe(true);
  });

  it("rejects mailbox names containing control characters", async () => {
    const { service } = serviceFor([]);

    await expect(service.listMessages({
      accountId: "work",
      mailboxId: "INBOX\r\nInjected",
      limit: 20,
    })).rejects.toThrow(/mailbox/i);
  });
});
