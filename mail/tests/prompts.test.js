import { describe, expect, it } from "vitest";
import { getMailPrompt, MAIL_PROMPTS } from "../mcp/prompts.js";

describe("Mail MCP prompts", () => {
  it("publishes a howto prompt with the Mail tool names and credential boundary", () => {
    expect(MAIL_PROMPTS).toEqual([
      expect.objectContaining({ name: "howto" }),
    ]);
    const prompt = getMailPrompt("howto");
    const text = prompt.messages[0].content.text;
    expect(text).toContain("accounts");
    expect(text).toContain("read");
    expect(text).toContain("host-owned");
  });

  it("rejects unknown prompts", () => {
    expect(() => getMailPrompt("missing")).toThrow("Unknown prompt");
  });
});
