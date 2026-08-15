import { describe, expect, it } from "vitest";
import {
  formatListText,
  formatTreeText,
  formatReadText,
  formatGrepText,
  formatSearchText,
  formatExistsText,
  formatMutationText,
  formatGenericText,
  mcpToolResult,
} from "../mcp/agent-output.js";

describe("formatListText", () => {
  it("renders dirs first with compact d/f markers", () => {
    const text = formatListText({
      path: "",
      items: [
        { name: "src", path: "src", isDir: true, isFile: false, size: 0, type: "dir" },
        { name: "a.txt", path: "a.txt", isDir: false, isFile: true, size: 12, type: "text" },
      ],
    });
    expect(text).toContain("path=.");
    expect(text).toContain("count=2");
    expect(text).toContain("d  src/");
    expect(text).toContain("f  a.txt  12B  text");
  });
});

describe("formatTreeText", () => {
  it("renders an indented tree", () => {
    const text = formatTreeText({
      path: "",
      depth: 2,
      tree: [
        {
          name: "src",
          path: "src",
          isDir: true,
          children: [{ name: "a.ts", path: "src/a.ts", isDir: false, size: 10, type: "text" }],
        },
      ],
    });
    expect(text).toContain("depth=2");
    expect(text).toContain("src/");
    expect(text).toContain("  a.ts");
  });
});

describe("formatReadText", () => {
  it("keeps file body verbatim under a content section", () => {
    const text = formatReadText({
      path: "a.txt",
      content: "line1\nline2\n",
      totalLines: 2,
      totalBytes: 12,
      truncated: false,
    });
    expect(text).toContain("path=a.txt");
    expect(text).toContain("lines=2");
    expect(text).toContain("=== content ===\nline1\nline2");
    expect(text).not.toContain("\\n");
  });
});

describe("formatGrepText", () => {
  it("uses path:line:content rows", () => {
    const text = formatGrepText({
      path: "",
      pattern: "TODO",
      results: [
        { path: "a.ts", line: 4, content: "  // TODO fix" },
        { path: "b.ts", line: 1, content: "TODO" },
      ],
      meta: { truncated: false, count: 2, cap: 500 },
    });
    expect(text).toContain("count=2");
    expect(text).toContain("a.ts:4:  // TODO fix");
    expect(text).toContain("b.ts:1:TODO");
  });
});

describe("formatSearchText", () => {
  it("lists matching paths", () => {
    const text = formatSearchText({
      path: "",
      pattern: "*.ts",
      results: [{ path: "a.ts", isDir: false, type: "text" }, { path: "src", isDir: true, type: "dir" }],
      meta: { truncated: false, count: 2 },
    });
    expect(text).toContain("pattern=*.ts");
    expect(text).toContain("file  a.ts");
    expect(text).toContain("dir   src");
  });
});

describe("formatExistsText", () => {
  it("summarizes existence", () => {
    expect(formatExistsText({ path: "x", exists: false, isFile: false, isDir: false })).toContain("exists=false");
    expect(formatExistsText({ path: "x", exists: true, isFile: true, isDir: false })).toContain("is_file=true");
  });
});

describe("formatMutationText", () => {
  it("flattens simple mutation receipts", () => {
    const text = formatMutationText({ path: "a.txt", written: true });
    expect(text).toContain("ok=true");
    expect(text).toContain("written=true");
    expect(text).toContain("path=a.txt");
  });
});

describe("formatGenericText", () => {
  it("falls back to compact key lines for unknown shapes", () => {
    const text = formatGenericText({ hello: "world", n: 1 });
    expect(text).toContain("hello=world");
    expect(text).toContain("n=1");
  });
});

describe("mcpToolResult", () => {
  it("returns dual text + structuredContent", () => {
    const result = mcpToolResult("ok=true\n", { ok: true });
    expect(result.content[0].text).toBe("ok=true\n");
    expect(result.structuredContent).toEqual({ ok: true });
  });
});
