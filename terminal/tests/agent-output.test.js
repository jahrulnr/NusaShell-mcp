import { describe, expect, it } from "vitest";
import {
  stripAnsi,
  formatExecText,
  formatPtyReadText,
  formatShellsText,
  formatOkText,
  formatSessionOpenText,
  mcpToolResult,
} from "../mcp/agent-output.js";

describe("stripAnsi", () => {
  it("removes CSI and OSC sequences", () => {
    expect(stripAnsi("\u001b[32mgreen\u001b[0m")).toBe("green");
    expect(stripAnsi("\u001b]0;title\u0007prompt$ ")).toBe("prompt$ ");
    expect(stripAnsi("plain")).toBe("plain");
  });
});

describe("formatExecText", () => {
  it("keeps stdout/stderr bodies verbatim under labeled sections", () => {
    const text = formatExecText({
      ok: true,
      exitCode: 0,
      shellKind: "bash",
      shell: "/bin/bash",
      cwd: "/tmp",
      timedOut: false,
      truncated: false,
      stdout: "hello\nworld\n",
      stderr: "warn\n",
    });
    expect(text).toContain("ok=true");
    expect(text).toContain("exit_code=0");
    expect(text).toContain("shell=bash");
    expect(text).toContain("shell_path=/bin/bash");
    expect(text).toContain("=== stdout ===\nhello\nworld");
    expect(text).toContain("=== stderr ===\nwarn");
    expect(text).not.toContain("\\n");
  });

  it("marks failures and timeouts in the header", () => {
    const text = formatExecText({
      ok: false,
      exitCode: 1,
      shellKind: "cmd",
      shell: "cmd.exe",
      cwd: "C:\\\\Users\\\\a",
      timedOut: true,
      truncated: true,
      stdout: "",
      stderr: "boom",
    });
    expect(text).toContain("ok=false");
    expect(text).toContain("timed_out=true");
    expect(text).toContain("truncated=true");
    expect(text).toContain("exit_code=1");
  });
});

describe("formatPtyReadText", () => {
  it("strips ANSI in the agent text while reporting the flag", () => {
    const text = formatPtyReadText({
      sessionId: "s1",
      exited: false,
      exitCode: null,
      truncated: false,
      stdout: "\u001b[31mERR\u001b[0m\n",
      ansiStripped: true,
    });
    expect(text).toContain("ansi_stripped=true");
    expect(text).toContain("=== output ===\nERR");
    expect(text).not.toContain("\u001b[");
  });
});

describe("formatShellsText", () => {
  it("lists shells as a compact table", () => {
    const text = formatShellsText({
      defaultKind: "bash",
      platform: "linux",
      shells: [
        { kind: "bash", path: "/bin/bash", available: true, source: "env" },
        { kind: "zsh", path: "/bin/zsh", available: true, source: "which" },
      ],
    });
    expect(text).toContain("default=bash");
    expect(text).toContain("platform=linux");
    expect(text).toContain("bash\t/bin/bash\tenv");
  });
});

describe("mcpToolResult", () => {
  it("returns dual text + structuredContent", () => {
    const result = mcpToolResult(
      formatOkText({ ok: true, sessionId: "abc" }),
      { sessionId: "abc", ok: true },
    );
    expect(result.content[0].type).toBe("text");
    expect(result.content[0].text).toContain("sessionId=abc");
    expect(result.structuredContent).toEqual({ sessionId: "abc", ok: true });
    expect(result.isError).toBeUndefined();
  });
});

describe("formatSessionOpenText", () => {
  it("summarizes a new session", () => {
    const text = formatSessionOpenText({
      sessionId: "s1",
      shellKind: "pwsh",
      shell: "pwsh.exe",
      cwd: "C:\\\\work",
      cols: 120,
      rows: 30,
    });
    expect(text).toContain("session_id=s1");
    expect(text).toContain("shell=pwsh");
  });
});
