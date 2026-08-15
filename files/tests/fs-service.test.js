import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { FileService, detectFileType, detectFileTypeByContent, formatFileSize } from "../mcp/fs-service.js";

let tmpDir;
let service;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-test-"));
  service = new FileService(tmpDir);
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("detectFileType", () => {
  it("detects text files", () => {
    expect(detectFileType("readme.md")).toBe("text");
    expect(detectFileType("app.js")).toBe("text");
    expect(detectFileType("data.json")).toBe("text");
  });

  it("detects image files", () => {
    expect(detectFileType("photo.png")).toBe("image");
    expect(detectFileType("photo.jpg")).toBe("image");
  });

  it("detects binary for unknown extensions", () => {
    expect(detectFileType("data.dat")).toBe("binary");
  });

  it("detects go.mod as text via basename", () => {
    expect(detectFileType("go.mod")).toBe("text");
    expect(detectFileType("go.sum")).toBe("text");
    expect(detectFileType("Makefile")).toBe("text");
    expect(detectFileType("Dockerfile")).toBe("text");
    expect(detectFileType("LICENSE")).toBe("text");
  });
});

describe("detectFileTypeByContent", () => {
  it("detects text files without standard extensions via magic bytes", async () => {
    const filePath = path.join(tmpDir, "go.mod");
    await fs.writeFile(filePath, "module github.com/example/foo\n\ngo 1.21\n");
    const result = await detectFileTypeByContent(filePath);
    expect(result.isText).toBe(true);
    expect(result.type).toBe("text");
  });

  it("detects Makefile as text via content", async () => {
    const filePath = path.join(tmpDir, "Makefile");
    await fs.writeFile(filePath, "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n");
    const result = await detectFileTypeByContent(filePath);
    expect(result.isText).toBe(true);
    expect(result.type).toBe("text");
  });

  it("detects actual binary files via NUL bytes", async () => {
    const filePath = path.join(tmpDir, "data.bin");
    const buf = Buffer.alloc(512, 0);
    buf[0] = 0x00; buf[1] = 0x01; buf[2] = 0x00; buf[3] = 0x02;
    await fs.writeFile(filePath, buf);
    const result = await detectFileTypeByContent(filePath);
    expect(result.isText).toBe(false);
  });

  it("detects PNG via magic bytes even with .txt extension", async () => {
    const filePath = path.join(tmpDir, "fake.txt");
    const pngHeader = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    const buf = Buffer.alloc(64, 0);
    pngHeader.copy(buf);
    await fs.writeFile(filePath, buf);
    const result = await detectFileTypeByContent(filePath);
    expect(result.isText).toBe(false);
    expect(result.type).toBe("image");
  });

  it("detects PDF via magic bytes", async () => {
    const filePath = path.join(tmpDir, "doc.pdf");
    const pdfHeader = Buffer.from("%PDF-1.4\n");
    await fs.writeFile(filePath, pdfHeader);
    const result = await detectFileTypeByContent(filePath);
    expect(result.isText).toBe(false);
    expect(result.type).toBe("pdf");
  });

  it("handles empty files as text", async () => {
    const filePath = path.join(tmpDir, "empty");
    await fs.writeFile(filePath, "");
    const result = await detectFileTypeByContent(filePath);
    expect(result.isText).toBe(true);
  });

  it("handles UTF-8 text with multibyte characters", async () => {
    const filePath = path.join(tmpDir, "unicode.txt");
    await fs.writeFile(filePath, "Hello 世界 🌍\nПривет мир\n");
    const result = await detectFileTypeByContent(filePath);
    expect(result.isText).toBe(true);
  });
});

describe("formatFileSize", () => {
  it("formats bytes", () => {
    expect(formatFileSize(0)).toBe("0 B");
    expect(formatFileSize(512)).toBe("512 B");
    expect(formatFileSize(1024)).toBe("1.0 KB");
    expect(formatFileSize(1024 * 1024)).toBe("1.0 MB");
  });
});

describe("FileService.listDir", () => {
  it("lists files and directories sorted dirs-first", async () => {
    await fs.writeFile(path.join(tmpDir, "b.txt"), "b");
    await fs.mkdir(path.join(tmpDir, "afolder"));
    await fs.writeFile(path.join(tmpDir, "a.txt"), "a");

    const items = await service.listDir("");
    expect(items).toHaveLength(3);
    expect(items[0].name).toBe("afolder");
    expect(items[0].isDir).toBe(true);
    expect(items[1].name).toBe("a.txt");
    expect(items[2].name).toBe("b.txt");
  });

  it("includes file metadata", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const items = await service.listDir("");
    expect(items[0].size).toBe(5);
    expect(items[0].type).toBe("text");
    expect(items[0].modified).toBeTruthy();
  });
});

describe("FileService.tree", () => {
  it("builds a tree with default depth", async () => {
    await fs.mkdir(path.join(tmpDir, "dir1", "dir2"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "dir1", "file.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "dir1", "dir2", "deep.txt"), "y");

    const tree = await service.tree("");
    expect(tree).toHaveLength(1);
    expect(tree[0].name).toBe("dir1");
    expect(tree[0].children).toHaveLength(2);
    const dir2 = tree[0].children.find((c) => c.name === "dir2");
    expect(dir2).toBeDefined();
    // depth 3: dir1 > dir2 > deep.txt should be visible
    expect(dir2.children).toHaveLength(1);
  });

  it("respects depth limit", async () => {
    await fs.mkdir(path.join(tmpDir, "a", "b", "c"), { recursive: true });
    const tree = await service.tree("", 1);
    expect(tree).toHaveLength(1);
    expect(tree[0].name).toBe("a");
    expect(tree[0].children).toBeUndefined();
  });
});

describe("FileService.readFile", () => {
  it("reads full file content", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "line1\nline2\nline3");
    const result = await service.readFile("test.txt");
    expect(result.content).toBe("line1\nline2\nline3");
    expect(result.totalLines).toBe(3);
    expect(result.truncated).toBe(false);
  });

  it("truncates oversized text at maxBytes instead of failing", async () => {
    await fs.writeFile(path.join(tmpDir, "large.txt"), "first line\nsecond line\n");

    const result = await service.readFile("large.txt", { maxBytes: 8 });

    expect(result.content).toBe("first li");
    expect(Buffer.byteLength(result.content, "utf8")).toBeLessThanOrEqual(8);
    expect(result.totalLines).toBe(3);
    expect(result.totalBytes).toBe(23);
    expect(result.truncated).toBe(true);
    expect(result.truncatedReason).toBe("maxBytes");
  });

  it("does not split a UTF-8 character when truncating at maxBytes", async () => {
    await fs.writeFile(path.join(tmpDir, "unicode.txt"), "a😀b");

    const result = await service.readFile("unicode.txt", { maxBytes: 4 });

    expect(result.content).toBe("a");
    expect(Buffer.byteLength(result.content, "utf8")).toBeLessThanOrEqual(4);
    expect(result.truncated).toBe(true);
  });

  it("reads head lines", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "line1\nline2\nline3");
    const result = await service.readFile("test.txt", { head: 2 });
    expect(result.content).toBe("line1\nline2");
    expect(result.truncated).toBe(true);
    expect(result.truncatedReason).toBe("head");
  });

  it("reads tail lines", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "line1\nline2\nline3");
    const result = await service.readFile("test.txt", { tail: 1 });
    expect(result.content).toBe("line3");
    expect(result.truncated).toBe(true);
    expect(result.truncatedReason).toBe("tail");
  });

  it("reads a line range with start/end", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "l1\nl2\nl3\nl4\nl5");
    const result = await service.readFile("test.txt", { start: 2, end: 4 });
    expect(result.content).toBe("l2\nl3\nl4");
    expect(result.truncated).toBe(true);
    expect(result.truncatedReason).toBe("startEnd");
  });

  it("prefixes line numbers when lineNumbers is true", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "l1\nl2\nl3");
    const result = await service.readFile("test.txt", { start: 2, end: 3, lineNumbers: true });
    expect(result.content).toContain("     2|l2");
    expect(result.content).toContain("     3|l3");
  });

  it("rejects binary files with a helpful error", async () => {
    await fs.writeFile(path.join(tmpDir, "data.bin"), Buffer.from([0x00, 0x01, 0x02, 0x03]));
    await expect(service.readFile("data.bin")).rejects.toThrow(/binary/);
  });

  it("reads go.mod (text file without standard extension)", async () => {
    await fs.writeFile(path.join(tmpDir, "go.mod"), "module github.com/example/foo\n\ngo 1.21\n");
    const result = await service.readFile("go.mod");
    expect(result.content).toContain("module github.com/example/foo");
    expect(result.totalLines).toBe(4);
  });

  it("reads Makefile (extensionless text file)", async () => {
    await fs.writeFile(path.join(tmpDir, "Makefile"), "build:\n\tgo build ./...\n");
    const result = await service.readFile("Makefile");
    expect(result.content).toContain("go build");
  });

  it("rejects PNG file even with .txt extension (magic byte detection)", async () => {
    const pngHeader = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    const buf = Buffer.alloc(64, 0);
    pngHeader.copy(buf);
    await fs.writeFile(path.join(tmpDir, "fake.txt"), buf);
    await expect(service.readFile("fake.txt")).rejects.toThrow(/binary/);
  });

  it("reads CRLF files without embedding CR in line bodies", async () => {
    await fs.writeFile(path.join(tmpDir, "crlf.txt"), "one\r\ntwo\r\n");
    const numbered = await service.readFile("crlf.txt", { lineNumbers: true });
    expect(numbered.content).not.toContain("\r");
    expect(numbered.content).toContain("     1|one");
    expect(numbered.content).toContain("     2|two");
  });
});

describe("FileService.writeFile", () => {
  it("creates a new file", async () => {
    const result = await service.writeFile("new.txt", "content");
    expect(result.written).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "new.txt"), "utf8");
    expect(content).toBe("content");
  });

  it("returns nested paths with forward slashes", async () => {
    const result = await service.writeFile("sub/dir/file.txt", "nested");
    expect(result.path).toBe("sub/dir/file.txt");
    expect(result.path).not.toMatch(/\\/);
  });

  it("creates parent directories", async () => {
    await service.writeFile("sub/dir/file.txt", "nested");
    const content = await fs.readFile(path.join(tmpDir, "sub", "dir", "file.txt"), "utf8");
    expect(content).toBe("nested");
  });

  it("overwrites existing file", async () => {
    await service.writeFile("file.txt", "old");
    await service.writeFile("file.txt", "new");
    const content = await fs.readFile(path.join(tmpDir, "file.txt"), "utf8");
    expect(content).toBe("new");
  });

  it("does not leave temp files after atomic write", async () => {
    await service.writeFile("file.txt", "content");
    const entries = await fs.readdir(tmpDir);
    expect(entries.filter((e) => e.includes(".tmp-"))).toHaveLength(0);
  });
});

describe("FileService.makeDir", () => {
  it("creates an empty nested directory", async () => {
    const result = await service.makeDir("empty/nested");
    expect(result.created).toBe(true);
    expect((await fs.stat(path.join(tmpDir, "empty", "nested"))).isDirectory()).toBe(true);
  });
});

describe("FileService.moveFile", () => {
  it("moves a file", async () => {
    await fs.writeFile(path.join(tmpDir, "src.txt"), "data");
    const result = await service.moveFile("src.txt", "dst.txt");
    expect(result.moved).toBe(true);
    await expect(fs.stat(path.join(tmpDir, "src.txt"))).rejects.toThrow();
    const content = await fs.readFile(path.join(tmpDir, "dst.txt"), "utf8");
    expect(content).toBe("data");
  });

  it("moves into nested destination", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "data");
    await service.moveFile("file.txt", "nested/deep/file.txt");
    const content = await fs.readFile(path.join(tmpDir, "nested", "deep", "file.txt"), "utf8");
    expect(content).toBe("data");
  });
});

describe("FileService.deleteFile", () => {
  it("deletes a file", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "x");
    const result = await service.deleteFile("file.txt", false);
    expect(result.deleted).toBe(true);
    await expect(fs.stat(path.join(tmpDir, "file.txt"))).rejects.toThrow();
  });

  it("refuses non-empty directory without recursive", async () => {
    await fs.mkdir(path.join(tmpDir, "dir"));
    await fs.writeFile(path.join(tmpDir, "dir", "file.txt"), "x");
    await expect(service.deleteFile("dir", false)).rejects.toThrow(/not empty/i);
  });

  it("deletes non-empty directory with recursive", async () => {
    await fs.mkdir(path.join(tmpDir, "dir"));
    await fs.writeFile(path.join(tmpDir, "dir", "file.txt"), "x");
    const result = await service.deleteFile("dir", true);
    expect(result.deleted).toBe(true);
  });

  it("allows absolute paths (agent is a trusted actor)", async () => {
    const absFile = path.join(tmpDir, "abs-file.txt");
    await fs.writeFile(absFile, "x");
    const result = await service.deleteFile(absFile, false);
    expect(result.deleted).toBe(true);
  });
});

describe("FileService.searchFiles", () => {
  it("finds files matching a glob pattern", async () => {
    await fs.writeFile(path.join(tmpDir, "a.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "b.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "c.md"), "x");
    await fs.mkdir(path.join(tmpDir, "sub"));
    await fs.writeFile(path.join(tmpDir, "sub", "d.txt"), "x");

    const { results, meta } = await service.searchFiles("", "*.txt");
    expect(results).toHaveLength(3);
    expect(results.map((r) => r.name).sort()).toEqual(["a.txt", "b.txt", "d.txt"]);
    expect(meta.truncated).toBe(false);
  });

  it("supports ? wildcard", async () => {
    await fs.writeFile(path.join(tmpDir, "ab.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "cd.txt"), "x");
    const { results } = await service.searchFiles("", "?b.txt");
    expect(results).toHaveLength(1);
    expect(results[0].name).toBe("ab.txt");
  });
});

describe("FileService.fileInfo", () => {
  it("returns detailed metadata for a file", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello");
    const info = await service.fileInfo("test.txt");
    expect(info.name).toBe("test.txt");
    expect(info.isFile).toBe(true);
    expect(info.isDir).toBe(false);
    expect(info.size).toBe(5);
    expect(info.type).toBe("text");
    expect(info.permissions).toBeTruthy();
  });

  it("returns metadata for a directory", async () => {
    await fs.mkdir(path.join(tmpDir, "mydir"));
    const info = await service.fileInfo("mydir");
    expect(info.isDir).toBe(true);
    expect(info.type).toBe("dir");
  });

  it("detects go.mod as text via content sniffing", async () => {
    await fs.writeFile(path.join(tmpDir, "go.mod"), "module github.com/example/foo\n\ngo 1.21\n");
    const info = await service.fileInfo("go.mod");
    expect(info.type).toBe("text");
  });

  it("detects PNG via magic bytes even with .txt extension", async () => {
    const pngHeader = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    const buf = Buffer.alloc(64, 0);
    pngHeader.copy(buf);
    await fs.writeFile(path.join(tmpDir, "fake.txt"), buf);
    const info = await service.fileInfo("fake.txt");
    expect(info.type).toBe("image");
  });
});

describe("FileService error hints", () => {
  it("includes root path hint on ENOENT for listDir", async () => {
    await expect(service.listDir("nonexistent")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for readFile", async () => {
    await expect(service.readFile("missing.txt")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for fileInfo", async () => {
    await expect(service.fileInfo("missing.txt")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for deleteFile", async () => {
    await expect(service.deleteFile("missing.txt", false)).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for tree", async () => {
    await expect(service.tree("nonexistent")).rejects.toThrow(/Files plugin root/);
  });

  it("includes root path hint on ENOENT for searchFiles", async () => {
    await expect(service.searchFiles("nonexistent", "*.txt")).rejects.toThrow(/Files plugin root/);
  });
});

describe("FileService.grepFiles", () => {
  it("finds matching lines in text files", async () => {
    await fs.writeFile(path.join(tmpDir, "a.js"), "function foo() {}\nconst x = 1;\nfunction bar() {}");
    await fs.writeFile(path.join(tmpDir, "b.js"), "const y = 2;\nfunction baz() {}");
    await fs.writeFile(path.join(tmpDir, "c.md"), "# Hello\nfunction notMatched() {}");

    const { results, meta } = await service.grepFiles("", "function\\s+\\w+");
    expect(results).toHaveLength(4);
    expect(results.every((r) => r.line > 0)).toBe(true);
    expect(results.every((r) => r.content.includes("function"))).toBe(true);
    expect(meta.truncated).toBe(false);
  });

  it("filters by glob pattern", async () => {
    await fs.writeFile(path.join(tmpDir, "a.js"), "function foo() {}");
    await fs.writeFile(path.join(tmpDir, "b.ts"), "function bar() {}");
    await fs.writeFile(path.join(tmpDir, "c.md"), "function baz() {}");

    const { results } = await service.grepFiles("", "function", { glob: "*.js" });
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("a.js");
  });

  it("skips non-text files", async () => {
    await fs.writeFile(path.join(tmpDir, "data.bin"), Buffer.from([0x00, 0x01, 0x02]));
    await fs.writeFile(path.join(tmpDir, "a.txt"), "hello world");

    const { results } = await service.grepFiles("", "hello");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("a.txt");
  });

  it("searches recursively", async () => {
    await fs.mkdir(path.join(tmpDir, "sub"));
    await fs.writeFile(path.join(tmpDir, "sub", "deep.js"), "TODO: fix this");

    const { results } = await service.grepFiles("", "TODO");
    expect(results).toHaveLength(1);
    // Agent-facing paths are always POSIX (stable across Windows/Unix).
    expect(results[0].path).toBe("sub/deep.js");
    expect(results[0].line).toBe(1);
  });

  it("greps a single file when path points at a file (not only directories)", async () => {
    await fs.mkdir(path.join(tmpDir, "sub"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "sub", "target.ts"), "export const scheduler = true;\nother\n");
    await fs.writeFile(path.join(tmpDir, "sub", "other.ts"), "scheduler should not match when path is one file\n");

    const { results, meta } = await service.grepFiles("sub/target.ts", "scheduler");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("sub/target.ts");
    expect(results[0].line).toBe(1);
    expect(results[0].content).toContain("scheduler");
    expect(meta.count).toBe(1);
  });

  it("includes root path hint on ENOENT", async () => {
    await expect(service.grepFiles("nonexistent", "pattern")).rejects.toThrow(/Files plugin root/);
  });

  it("returns context lines with before/after", async () => {
    await fs.writeFile(path.join(tmpDir, "a.txt"), "l1\nl2\nMATCH\nl4\nl5");
    const { results } = await service.grepFiles("", "MATCH", { before: 1, after: 1 });
    expect(results).toHaveLength(1);
    expect(results[0].before).toEqual(["l2"]);
    expect(results[0].after).toEqual(["l4"]);
  });

  it("ignoreCase matches case-insensitively", async () => {
    await fs.writeFile(path.join(tmpDir, "a.txt"), "Hello World\nHELLO AGAIN");
    const { results } = await service.grepFiles("", "hello", { ignoreCase: true });
    expect(results).toHaveLength(2);
  });

  it("exclude skips matching entries", async () => {
    await fs.mkdir(path.join(tmpDir, "node_modules"));
    await fs.writeFile(path.join(tmpDir, "node_modules", "skip.js"), "TODO: skip");
    await fs.writeFile(path.join(tmpDir, "keep.js"), "TODO: keep");
    const { results } = await service.grepFiles("", "TODO", { exclude: ["node_modules"] });
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("keep.js");
  });

  it("greps go.mod (text file without standard extension)", async () => {
    await fs.writeFile(path.join(tmpDir, "go.mod"), "module github.com/example/foo\nrequire (\n\tgithub.com/bar v1.0.0\n)\n");
    const { results } = await service.grepFiles("", "github.com/bar");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("go.mod");
    expect(results[0].line).toBe(3);
  });

  it("greps Makefile (extensionless text file)", async () => {
    await fs.writeFile(path.join(tmpDir, "Makefile"), "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n");
    const { results } = await service.grepFiles("", "go test");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("Makefile");
  });

  it("skips binary files even with text extension (magic byte detection)", async () => {
    const pngHeader = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    const buf = Buffer.alloc(64, 0);
    pngHeader.copy(buf);
    await fs.writeFile(path.join(tmpDir, "fake.txt"), buf);
    await fs.writeFile(path.join(tmpDir, "real.txt"), "TODO: find me");
    const { results } = await service.grepFiles("", "TODO");
    expect(results).toHaveLength(1);
    expect(results[0].path).toBe("real.txt");
  });
});

describe("FileService.patchFile", () => {
  it("replaces first occurrence of old_string", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello world\nfoo bar");
    const result = await service.patchFile("test.txt", [{ old_string: "foo bar", new_string: "baz qux" }]);
    expect(result.patched).toBe(true);
    expect(result.applied).toBe(1);
    expect(result.occurrences).toEqual([1]);
    const content = await fs.readFile(path.join(tmpDir, "test.txt"), "utf8");
    expect(content).toBe("hello world\nbaz qux");
  });

  it("only replaces first occurrence by default", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "aaa\naaa\naaa");
    await service.patchFile("test.txt", [{ old_string: "aaa", new_string: "bbb" }]);
    const content = await fs.readFile(path.join(tmpDir, "test.txt"), "utf8");
    expect(content).toBe("bbb\naaa\naaa");
  });

  it("replaces all occurrences with replace_all", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "aaa\naaa\naaa");
    const result = await service.patchFile("test.txt", [{ old_string: "aaa", new_string: "bbb", replace_all: true }]);
    expect(result.occurrences).toEqual([3]);
    const content = await fs.readFile(path.join(tmpDir, "test.txt"), "utf8");
    expect(content).toBe("bbb\nbbb\nbbb");
  });

  it("applies multiple edits in sequence", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "foo\nbar\nbaz");
    const result = await service.patchFile("test.txt", [
      { old_string: "foo", new_string: "FOO" },
      { old_string: "bar", new_string: "BAR" },
    ]);
    expect(result.applied).toBe(2);
    const content = await fs.readFile(path.join(tmpDir, "test.txt"), "utf8");
    expect(content).toBe("FOO\nBAR\nbaz");
  });

  it("preview mode returns content without writing", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello world");
    const result = await service.patchFile("test.txt", [{ old_string: "hello", new_string: "hi" }], true);
    expect(result.patched).toBe(false);
    expect(result.preview).toBe("hi world");
    const content = await fs.readFile(path.join(tmpDir, "test.txt"), "utf8");
    expect(content).toBe("hello world");
  });

  it("throws if old_string not found", async () => {
    await fs.writeFile(path.join(tmpDir, "test.txt"), "hello world");
    await expect(service.patchFile("test.txt", [{ old_string: "missing", new_string: "replacement" }])).rejects.toThrow(/old_string not found/);
  });

  it("throws on ENOENT with root hint", async () => {
    await expect(service.patchFile("missing.txt", [{ old_string: "a", new_string: "b" }])).rejects.toThrow(/Files plugin root/);
  });

  it("rejects binary files to prevent corruption", async () => {
    const pngHeader = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    const buf = Buffer.alloc(64, 0);
    pngHeader.copy(buf);
    await fs.writeFile(path.join(tmpDir, "image.png"), buf);
    await expect(service.patchFile("image.png", [{ old_string: "a", new_string: "b" }])).rejects.toThrow(/binary/);
  });

  it("patches go.mod (text file without standard extension)", async () => {
    await fs.writeFile(path.join(tmpDir, "go.mod"), "module github.com/example/foo\n\ngo 1.21\n");
    const result = await service.patchFile("go.mod", [{ old_string: "github.com/example/foo", new_string: "github.com/example/bar" }]);
    expect(result.applied).toBe(1);
    const content = await fs.readFile(path.join(tmpDir, "go.mod"), "utf8");
    expect(content).toContain("github.com/example/bar");
  });
});

describe("FileService.appendFile", () => {
  it("appends to an existing file", async () => {
    await fs.writeFile(path.join(tmpDir, "log.txt"), "line1\n");
    const result = await service.appendFile("log.txt", "line2\n");
    expect(result.appended).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "log.txt"), "utf8");
    expect(content).toBe("line1\nline2\n");
  });

  it("creates a new file if it does not exist", async () => {
    const result = await service.appendFile("new.txt", "content");
    expect(result.appended).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "new.txt"), "utf8");
    expect(content).toBe("content");
  });

  it("creates parent directories", async () => {
    await service.appendFile("sub/dir/file.txt", "nested");
    const content = await fs.readFile(path.join(tmpDir, "sub", "dir", "file.txt"), "utf8");
    expect(content).toBe("nested");
  });
});

describe("FileService.copyFile", () => {
  it("copies a file", async () => {
    await fs.writeFile(path.join(tmpDir, "original.txt"), "hello world");
    const result = await service.copyFile("original.txt", "copy.txt");
    expect(result.copied).toBe(true);
    expect(result.from).toBe("original.txt");
    expect(result.to).toBe("copy.txt");
    const content = await fs.readFile(path.join(tmpDir, "copy.txt"), "utf8");
    expect(content).toBe("hello world");
    const original = await fs.readFile(path.join(tmpDir, "original.txt"), "utf8");
    expect(original).toBe("hello world");
  });

  it("copies a directory recursively", async () => {
    await fs.mkdir(path.join(tmpDir, "srcdir"));
    await fs.writeFile(path.join(tmpDir, "srcdir", "a.txt"), "aaa");
    await fs.writeFile(path.join(tmpDir, "srcdir", "b.txt"), "bbb");

    const result = await service.copyFile("srcdir", "dstdir");
    expect(result.copied).toBe(true);
    const aContent = await fs.readFile(path.join(tmpDir, "dstdir", "a.txt"), "utf8");
    const bContent = await fs.readFile(path.join(tmpDir, "dstdir", "b.txt"), "utf8");
    expect(aContent).toBe("aaa");
    expect(bContent).toBe("bbb");
  });

  it("creates parent directories for destination", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "content");
    await service.copyFile("file.txt", "sub/deep/copy.txt");
    const content = await fs.readFile(path.join(tmpDir, "sub", "deep", "copy.txt"), "utf8");
    expect(content).toBe("content");
  });

  it("throws on ENOENT with root hint", async () => {
    await expect(service.copyFile("missing.txt", "copy.txt")).rejects.toThrow(/Files plugin root/);
  });
});

describe("FileService.setRoot", () => {
  it("updates the in-process root and subsequent operations use it", async () => {
    const newRoot = await fs.mkdtemp(path.join(os.tmpdir(), "files-setroot-"));
    try {
      await fs.writeFile(path.join(newRoot, "hello.txt"), "hi");
      await service.setRoot(newRoot);
      expect(service.root).toBe(path.resolve(newRoot));
      const result = await service.readFile("hello.txt");
      expect(result.content).toBe("hi");
    } finally {
      await fs.rm(newRoot, { recursive: true, force: true });
    }
  });

  it("rejects a non-existent root", async () => {
    await expect(service.setRoot("/nonexistent/path/xyz")).rejects.toThrow(/does not exist/);
  });

  it("rejects a root that is not a directory", async () => {
    const filePath = path.join(tmpDir, "not-a-dir.txt");
    await fs.writeFile(filePath, "x");
    await expect(service.setRoot(filePath)).rejects.toThrow(/not a directory/);
  });
});

describe("FileService.searchFiles exclude + type + maxDepth", () => {
  it("exclude skips matching directories", async () => {
    await fs.mkdir(path.join(tmpDir, "node_modules"));
    await fs.writeFile(path.join(tmpDir, "node_modules", "skip.txt"), "x");
    await fs.writeFile(path.join(tmpDir, "keep.txt"), "x");
    const { results } = await service.searchFiles("", "*.txt", { exclude: ["node_modules"] });
    expect(results).toHaveLength(1);
    expect(results[0].name).toBe("keep.txt");
  });

  it("type=dir returns only directories", async () => {
    await fs.mkdir(path.join(tmpDir, "subdir"));
    await fs.writeFile(path.join(tmpDir, "subdir.txt"), "x");
    const { results } = await service.searchFiles("", "*", { type: "dir" });
    expect(results.every((r) => r.isDir)).toBe(true);
    expect(results.find((r) => r.name === "subdir")).toBeDefined();
  });

  it("maxDepth limits recursion", async () => {
    await fs.mkdir(path.join(tmpDir, "a", "b", "c"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "a", "b", "c", "deep.txt"), "x");
    const { results } = await service.searchFiles("", "*.txt", { maxDepth: 2 });
    expect(results).toHaveLength(0);
    const { results: deep } = await service.searchFiles("", "*.txt", { maxDepth: 5 });
    expect(deep).toHaveLength(1);
  });
});

describe("FileService.tree exclude + includeFiles", () => {
  it("exclude skips directories", async () => {
    await fs.mkdir(path.join(tmpDir, "node_modules"));
    await fs.writeFile(path.join(tmpDir, "node_modules", "skip.js"), "x");
    await fs.writeFile(path.join(tmpDir, "keep.js"), "x");
    const tree = await service.tree("", 3, { exclude: ["node_modules"] });
    expect(tree.find((n) => n.name === "node_modules")).toBeUndefined();
    expect(tree.find((n) => n.name === "keep.js")).toBeDefined();
  });

  it("includeFiles=false returns dirs only", async () => {
    await fs.mkdir(path.join(tmpDir, "subdir"));
    await fs.writeFile(path.join(tmpDir, "file.txt"), "x");
    const tree = await service.tree("", 3, { includeFiles: false });
    expect(tree.every((n) => n.isDir)).toBe(true);
    expect(tree.find((n) => n.name === "subdir")).toBeDefined();
    expect(tree.find((n) => n.name === "file.txt")).toBeUndefined();
  });
});

describe("FileService.existsFile", () => {
  it("returns exists:true for an existing file", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "x");
    const result = await service.existsFile("file.txt");
    expect(result.exists).toBe(true);
    expect(result.isFile).toBe(true);
    expect(result.isDir).toBe(false);
  });

  it("returns exists:true for a directory", async () => {
    await fs.mkdir(path.join(tmpDir, "subdir"));
    const result = await service.existsFile("subdir");
    expect(result.exists).toBe(true);
    expect(result.isDir).toBe(true);
  });

  it("returns exists:false for a missing path (does NOT throw)", async () => {
    const result = await service.existsFile("missing.txt");
    expect(result.exists).toBe(false);
    expect(result.isFile).toBe(false);
    expect(result.isDir).toBe(false);
  });
});

describe("FileService.touchFile", () => {
  it("creates a new empty file", async () => {
    const result = await service.touchFile("new.txt");
    expect(result.created).toBe(true);
    expect(result.touched).toBe(false);
    const content = await fs.readFile(path.join(tmpDir, "new.txt"), "utf8");
    expect(content).toBe("");
  });

  it("creates parent directories by default", async () => {
    await service.touchFile("sub/dir/file.txt");
    const stat = await fs.stat(path.join(tmpDir, "sub", "dir", "file.txt"));
    expect(stat.isFile()).toBe(true);
  });

  it("updates timestamps of an existing file", async () => {
    await fs.writeFile(path.join(tmpDir, "file.txt"), "x");
    const before = (await fs.stat(path.join(tmpDir, "file.txt"))).mtime;
    await new Promise((r) => setTimeout(r, 20));
    const result = await service.touchFile("file.txt");
    expect(result.created).toBe(false);
    expect(result.touched).toBe(true);
    const after = (await fs.stat(path.join(tmpDir, "file.txt"))).mtime;
    expect(after.getTime()).toBeGreaterThan(before.getTime());
  });

  it("updateOnly throws on missing file", async () => {
    await expect(service.touchFile("missing.txt", { updateOnly: true })).rejects.toThrow(/does not exist/);
  });
});
describe("FileService.writeFile with encoding", () => {
  it("writes utf8 content by default", async () => {
    const result = await service.writeFile("hello.txt", "héllo 🌍");
    expect(result.written).toBe(true);
    const content = await fs.readFile(path.join(tmpDir, "hello.txt"), "utf8");
    expect(content).toBe("héllo 🌍");
  });

  it("writes base64 content as raw bytes", async () => {
    const bytes = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0xfe]);
    const b64 = Buffer.from(bytes).toString("base64");
    const result = await service.writeFile("img.bin", b64, { encoding: "base64" });
    expect(result.written).toBe(true);
    const written = await fs.readFile(path.join(tmpDir, "img.bin"));
    expect([...written]).toEqual([...bytes]);
  });

  it("keeps base64 writes byte-identical with NUL bytes", async () => {
    const bytes = Buffer.from([0x00, 0x01, 0x02, 0xff, 0x00, 0x7f]);
    await service.writeFile("zeros.bin", bytes.toString("base64"), { encoding: "base64" });
    const written = await fs.readFile(path.join(tmpDir, "zeros.bin"));
    expect(written.equals(bytes)).toBe(true);
  });
});