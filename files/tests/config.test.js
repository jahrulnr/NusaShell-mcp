import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import {
  loadRootFromEnvironment,
  resolvePath,
  validateRoot,
  toPosixPath,
  relativePosix,
  splitLines,
} from "../mcp/config.js";

let tmpDir;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-config-test-"));
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("loadRootFromEnvironment", () => {
  it("uses NUSASHELL_FILES_ROOT when set", async () => {
    const root = await loadRootFromEnvironment({ NUSASHELL_FILES_ROOT: tmpDir });
    expect(root).toBe(path.resolve(tmpDir));
  });

  it("falls back to home directory when not set", async () => {
    const root = await loadRootFromEnvironment({});
    expect(root).toBe(os.homedir());
  });

  it("throws when root does not exist", async () => {
    await expect(
      loadRootFromEnvironment({ NUSASHELL_FILES_ROOT: "/nonexistent/path/xyz" }),
    ).rejects.toThrow(/does not exist/);
  });

  it("throws when root is not a directory", async () => {
    const filePath = path.join(tmpDir, "file.txt");
    await fs.writeFile(filePath, "x");
    await expect(
      loadRootFromEnvironment({ NUSASHELL_FILES_ROOT: filePath }),
    ).rejects.toThrow(/not a directory/);
  });

  it("falls back to NUSASHELL_WORKSPACE when NUSASHELL_FILES_ROOT is unset", async () => {
    const root = await loadRootFromEnvironment({ NUSASHELL_WORKSPACE: tmpDir });
    expect(root).toBe(path.resolve(tmpDir));
  });

  it("prefers NUSASHELL_FILES_ROOT over NUSASHELL_WORKSPACE", async () => {
    const root = await loadRootFromEnvironment({
      NUSASHELL_FILES_ROOT: tmpDir,
      NUSASHELL_WORKSPACE: "/nonexistent/should-not-be-used",
    });
    expect(root).toBe(path.resolve(tmpDir));
  });
});

describe("validateRoot", () => {
  it("resolves and validates an existing directory", async () => {
    const root = await validateRoot(tmpDir);
    expect(root).toBe(path.resolve(tmpDir));
  });

  it("throws when the path does not exist", async () => {
    await expect(validateRoot("/nonexistent/path/xyz")).rejects.toThrow(/does not exist/);
  });

  it("throws when the path is not a directory", async () => {
    const filePath = path.join(tmpDir, "file.txt");
    await fs.writeFile(filePath, "x");
    await expect(validateRoot(filePath)).rejects.toThrow(/not a directory/);
  });
});

describe("resolvePath", () => {
  it("returns root for empty input", () => {
    expect(resolvePath(tmpDir, "")).toBe(tmpDir);
  });

  it("resolves / to OS root (not the files root)", () => {
    expect(resolvePath(tmpDir, "/")).toBe(path.resolve("/"));
  });

  it("resolves relative paths against root", () => {
    expect(resolvePath(tmpDir, "sub/file.txt")).toBe(path.resolve(tmpDir, "sub/file.txt"));
  });

  it("resolves absolute paths to OS-absolute (agent is a trusted actor)", () => {
    expect(resolvePath(tmpDir, "/absolute/path")).toBe(path.resolve("/absolute/path"));
    const deepPath = "/some/deep/workspace/tmp/plan/foo.md";
    expect(resolvePath(tmpDir, deepPath)).toBe(path.resolve(deepPath));
  });

  it("allows ../ traversal to escape root (no containment)", () => {
    expect(resolvePath(tmpDir, "../../etc/passwd")).toBe(path.resolve(tmpDir, "../../etc/passwd"));
    expect(resolvePath(tmpDir, "../../../")).toBe(path.resolve(tmpDir, "../../../"));
  });

  it("allows nested paths inside root", () => {
    expect(resolvePath(tmpDir, "sub/dir/file.txt")).toBe(path.resolve(tmpDir, "sub/dir/file.txt"));
  });
});

describe("toPosixPath / relativePosix", () => {
  it("normalizes Windows backslashes and leaves posix paths", () => {
    expect(toPosixPath("src\\app\\file.ts")).toBe("src/app/file.ts");
    expect(toPosixPath("src/app/file.ts")).toBe("src/app/file.ts");
    expect(toPosixPath("")).toBe("");
  });

  it("returns nested relative paths with forward slashes", () => {
    const abs = path.join(tmpDir, "nested", "dir", "file.txt");
    expect(relativePosix(tmpDir, abs)).toBe("nested/dir/file.txt");
    expect(relativePosix(tmpDir, abs)).not.toMatch(/\\/);
  });

  it("uses fallback when the absolute path is the root", () => {
    expect(relativePosix(tmpDir, tmpDir, ".")).toBe(".");
  });
});

describe("splitLines", () => {
  it("splits LF and CRLF without leaving CR on line bodies", () => {
    expect(splitLines("a\nb\n")).toEqual(["a", "b", ""]);
    expect(splitLines("a\r\nb\r\n")).toEqual(["a", "b", ""]);
    expect(splitLines("solo")).toEqual(["solo"]);
  });
});
