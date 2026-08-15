import { describe, expect, it } from "vitest";
import { getNotesPrompt, NOTES_PROMPTS } from "../mcp/prompts.js";

describe("Notes MCP prompts", () => {
  it("publishes a howto prompt with the Notes tool names", () => {
    expect(NOTES_PROMPTS).toEqual([
      expect.objectContaining({ name: "howto" }),
    ]);
    const prompt = getNotesPrompt("howto");
    const text = prompt.messages[0].content.text;
    expect(text).toContain("create");
    expect(text).toContain("search");
    expect(text).toContain("separate from the shell conversation history");
  });

  it("rejects unknown prompts", () => {
    expect(() => getNotesPrompt("missing")).toThrow("Unknown prompt");
  });
});
