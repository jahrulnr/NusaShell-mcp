import { describe, expect, it } from "vitest";
import { MAIL_TOOL_NAMES } from "../mcp/tool-catalog.js";

describe("Mail MCP tool catalog", () => {
  it("exposes the expected tool set including account management", () => {
    expect(MAIL_TOOL_NAMES).toEqual([
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
  });

  it("does not expose direct-send tools", () => {
    expect(MAIL_TOOL_NAMES).not.toContain("mail_send");
    expect(MAIL_TOOL_NAMES.every((name) => !name.includes("password"))).toBe(true);
  });
});
