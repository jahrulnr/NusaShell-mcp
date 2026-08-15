import { describe, expect, it, vi } from "vitest";
import { callMailTool, MAIL_TOOLS } from "../mcp/tools.js";

function service() {
  return {
    listAccounts: vi.fn(() => [
      { id: "work", name: "Work", email: "work@example.com", enabled: true },
      { id: "personal", name: "Personal", email: "me@example.com", enabled: true },
    ]),
    getAccount: vi.fn((id) => ({ id, name: id, email: `${id}@example.com` })),
    testAccount: vi.fn(async (id) => ({ accountId: id, incoming: { ok: true }, outgoing: { ok: true } })),
    listMailboxes: vi.fn(async () => []),
    listMessages: vi.fn(async ({ accountId, mailboxId }) => ({
      accountId,
      mailboxId,
      messages: [{ accountId, mailboxId, uid: 1, subject: `From ${accountId}` }],
      nextCursor: null,
      total: 1,
      availableTotal: 1,
      scannedCount: 1,
      scannedTailUid: 1,
      hasMore: false,
      messagePositions: { "1": 0 },
      truncated: false,
    })),
    readMessage: vi.fn(async ({ accountId, uid }) => ({ accountId, uid, subject: "Read" })),
  };
}

describe("Mail MCP tools", () => {
  it("publishes descriptors in the canonical catalog order", () => {
    expect(MAIL_TOOLS.map((tool) => tool.name)).toEqual([
      "accounts",
      "account_get",
      "account_save",
      "account_delete",
      "account_test",
      "mailboxes",
      "inbox",
      "messages",
      "search",
      "read",
    ]);
    expect(MAIL_TOOLS.every((tool) => !tool.name.startsWith("mail_"))).toBe(true);
  });

  it("builds a unified inbox across enabled accounts", async () => {
    const target = service();

    const result = await callMailTool(target, "inbox", { limit: 20 });

    expect(result.messages.map((message) => message.accountId)).toEqual(["work", "personal"]);
    expect(target.listMessages).toHaveBeenCalledTimes(2);
    expect(result.meta).toMatchObject({ data_is_untrusted: true, source: "email" });
  });

  it("validates tool input before opening a mail connection", async () => {
    const target = service();

    await expect(callMailTool(target, "read", {
      account_id: "work",
      mailbox_id: "INBOX",
      uid: -1,
    })).rejects.toThrow();
    expect(target.readMessage).not.toHaveBeenCalled();
  });

  it("tests only the requested connection scope", async () => {
    const target = service();

    await callMailTool(target, "account_test", {
      account_id: "work",
      scope: "incoming",
    });

    expect(target.testAccount).toHaveBeenCalledWith("work", "incoming");
  });

  it("returns a unified cursor that can be supplied to the next page", async () => {
    const target = service();
    target.listMessages.mockImplementation(async ({ accountId, mailboxId, cursor }) => {
      const allUids = accountId === "work" ? [10] : [8, 7];
      const beforeUid = cursor === undefined ? null : Number(cursor);
      const candidates = beforeUid === null
        ? allUids
        : beforeUid === 0
          ? []
          : allUids.filter((uid) => uid < beforeUid);
      const pageUids = candidates.slice(0, 1);
      const messages = pageUids.map((uid) => ({
            accountId,
            mailboxId,
            uid,
            subject: `From ${accountId}`,
            date: accountId === "work"
              ? "2026-07-30T10:00:00Z"
              : "2026-07-29T10:00:00Z",
          }));
      return {
        accountId,
        mailboxId,
        messages,
        total: allUids.length,
        availableTotal: candidates.length,
        scannedCount: pageUids.length,
        scannedTailUid: pageUids.at(-1) ?? null,
        hasMore: pageUids.length < candidates.length,
        messagePositions: Object.fromEntries(
          messages.map((message, index) => [String(message.uid), index]),
        ),
        truncated: false,
      };
    });

    const first = await callMailTool(target, "inbox", { limit: 1 });
    const second = await callMailTool(target, "inbox", {
      limit: 1,
      cursor: first.nextCursor,
    });
    const third = await callMailTool(target, "inbox", {
      limit: 1,
      cursor: second.nextCursor,
    });

    expect(first.messages[0].accountId).toBe("work");
    expect(second.messages[0]).toMatchObject({ accountId: "personal", uid: 8 });
    expect(third.messages[0]).toMatchObject({ accountId: "personal", uid: 7 });
    expect(third.nextCursor).toBeNull();
    expect(target.listMessages).toHaveBeenCalledWith(expect.objectContaining({
      accountId: "work",
      cursor: "10",
    }));
    expect(target.listMessages).toHaveBeenCalledWith(expect.objectContaining({
      accountId: "work",
      cursor: "0",
    }));
    expect(target.listMessages).toHaveBeenLastCalledWith(expect.objectContaining({
      accountId: "personal",
      cursor: "8",
    }));
  });

  it("rejects unknown tools", async () => {
    await expect(callMailTool(service(), "mail_raw_imap", {}))
      .rejects.toThrow(/unknown mail tool/i);
  });
});
