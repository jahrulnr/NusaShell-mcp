import { describe, it, expect } from "vitest";
import {
  unwrapToolPayload,
  extractText,
  parseToolJson,
  parseHostToolResult,
  friendlyError,
} from "../ui/terminal-bridge.js";

const session = {
  sessionId: "2324a586-54dd-4b0b-ad15-1a6d7ba580ac",
  shell: "/bin/bash",
  cwd: "/home/jahrulnr",
  cols: 90,
  rows: 22,
};

const contentParts = [{ type: "text", text: JSON.stringify(session) }];

describe("unwrapToolPayload", () => {
  it("prefers structuredContent, then data, then result", () => {
    expect(unwrapToolPayload({ structuredContent: { a: 1 }, result: { b: 2 } })).toEqual({ a: 1 });
    expect(unwrapToolPayload({ data: { a: 1 }, result: { b: 2 } })).toEqual({ a: 1 });
    expect(unwrapToolPayload({ requestId: "r1", result: contentParts })).toEqual(contentParts);
  });
});

describe("extractText / parseToolJson", () => {
  it("parses bare MCP content arrays (host IPC shape that broke the UI)", () => {
    expect(extractText(contentParts)).toBe(JSON.stringify(session));
    expect(parseToolJson(contentParts)).toEqual(session);
  });

  it("parses { content: [...] } CallToolResult", () => {
    expect(parseToolJson({ content: contentParts })).toEqual(session);
  });

  it("parses host CallToolResult wrapper { requestId, result: content[] }", () => {
    const raw = { requestId: "abc", result: contentParts };
    expect(parseHostToolResult(raw)).toEqual(session);
  });

  it("parses host wrapper with nested { content }", () => {
    const raw = { requestId: "abc", result: { content: contentParts } };
    expect(parseHostToolResult(raw)).toEqual(session);
  });

  it("returns null for unexpected shapes instead of throwing", () => {
    expect(parseHostToolResult(null)).toBeNull();
    expect(parseHostToolResult({ result: { content: [{ type: "text", text: "not-json" }] } })).toBeNull();
  });
});

describe("friendlyError", () => {
  it("does not dump raw MCP content arrays to the UI", () => {
    const naked = `Failed to open terminal session: ${JSON.stringify(contentParts)}`;
    expect(friendlyError(new Error(naked), "Could not start a terminal session.")).toBe(
      "Could not start a terminal session.",
    );
  });

  it("maps not-running errors to a short retry hint", () => {
    expect(friendlyError(new Error("Plugin nusashell.terminal is not running"), "fallback")).toBe(
      "Start the Terminal plugin from the launcher, then retry.",
    );
  });
});
