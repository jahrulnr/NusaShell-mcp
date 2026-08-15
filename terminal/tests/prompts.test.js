import { describe, expect, it } from "vitest";
import { getTerminalPrompt, TERMINAL_PROMPTS } from "../mcp/prompts.js";

describe("Terminal MCP prompts", () => {
  it("publishes a howto prompt with the Terminal tool names and cwd boundary", () => {
    expect(TERMINAL_PROMPTS).toEqual([
      expect.objectContaining({ name: "howto" }),
    ]);
    const prompt = getTerminalPrompt("howto");
    const text = prompt.messages[0].content.text;
    expect(text).toContain("exec");
    expect(text).toContain("shells");
    expect(text).toContain("open");
    expect(text).toContain("absolute cwd");
    expect(text).toContain("AGENTS.md");
    expect(text).toContain("pwsh");
  });

  it("rejects unknown prompts", () => {
    expect(() => getTerminalPrompt("missing")).toThrow("Unknown prompt");
  });
});
