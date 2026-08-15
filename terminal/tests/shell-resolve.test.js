import { describe, expect, it } from "vitest";
import path from "node:path";
import {
  detectShellKind,
  execArgsForShell,
  ptyArgsForShell,
  resolveShell,
  listAvailableShells,
  SHELL_KINDS,
} from "../mcp/shell-resolve.js";

const win = (...parts) => path.win32.join(...parts);

describe("detectShellKind", () => {
  it("maps common executables", () => {
    expect(detectShellKind("/bin/bash")).toBe("bash");
    expect(detectShellKind(win("C:\\Program Files", "Git", "bin", "bash.exe"))).toBe("bash");
    expect(detectShellKind("powershell.exe")).toBe("powershell");
    expect(detectShellKind("pwsh")).toBe("pwsh");
    expect(detectShellKind("cmd.exe")).toBe("cmd");
    expect(detectShellKind("wsl.exe")).toBe("wsl");
    expect(detectShellKind("zsh")).toBe("zsh");
  });
});

describe("execArgsForShell", () => {
  it("uses login-shell form for bash/zsh", () => {
    expect(execArgsForShell("bash", "echo hi")).toEqual(["-lc", "echo hi"]);
    expect(execArgsForShell("zsh", "echo hi")).toEqual(["-lc", "echo hi"]);
  });

  it("uses NonInteractive -Command for PowerShell flavors", () => {
    expect(execArgsForShell("powershell", "Get-Location")).toEqual([
      "-NoLogo",
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      "Get-Location",
    ]);
    expect(execArgsForShell("pwsh", "1+1")).toEqual([
      "-NoLogo",
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      "1+1",
    ]);
  });

  it("uses cmd /d /s /c", () => {
    expect(execArgsForShell("cmd", "echo hi")).toEqual(["/d", "/s", "/c", "echo hi"]);
  });

  it("routes wsl through bash -lc", () => {
    expect(execArgsForShell("wsl", "pwd")).toEqual(["-e", "bash", "-lc", "pwd"]);
  });
});

describe("resolveShell", () => {
  it("resolves unix auto from SHELL env", () => {
    const resolved = resolveShell("auto", {
      platform: "linux",
      env: { SHELL: "/bin/zsh" },
      exists: () => true,
      which: () => null,
    });
    expect(resolved).toEqual({
      kind: "zsh",
      path: "/bin/zsh",
      available: true,
      source: "env",
    });
  });

  it("prefers pwsh over powershell over bash over cmd on Windows auto", () => {
    const pwsh = win("C:\\Program Files", "PowerShell", "7", "pwsh.exe");
    const powershell = win("C:\\Windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe");
    const bash = win("C:\\Program Files", "Git", "bin", "bash.exe");
    const cmd = win("C:\\Windows", "System32", "cmd.exe");
    const available = new Set([pwsh, powershell, bash, cmd]);

    const resolved = resolveShell("auto", {
      platform: "win32",
      env: {
        SystemRoot: "C:\\Windows",
        ProgramFiles: "C:\\Program Files",
        ComSpec: cmd,
      },
      exists: (candidate) => available.has(candidate),
      which: () => null,
    });
    expect(resolved.kind).toBe("pwsh");
    expect(resolved.path).toBe(pwsh);
  });

  it("falls back to cmd when richer Windows shells are missing", () => {
    const cmd = win("C:\\Windows", "System32", "cmd.exe");
    const resolved = resolveShell("auto", {
      platform: "win32",
      env: { ComSpec: cmd, SystemRoot: "C:\\Windows" },
      exists: (candidate) => candidate === cmd,
      which: () => null,
    });
    expect(resolved).toMatchObject({ kind: "cmd", available: true });
  });

  it("resolves explicit kind bash via common Git Bash paths", () => {
    const bash = win("C:\\Program Files", "Git", "bin", "bash.exe");
    const resolved = resolveShell("bash", {
      platform: "win32",
      env: { ProgramFiles: "C:\\Program Files" },
      exists: (candidate) => candidate === bash,
      which: () => null,
    });
    expect(resolved).toMatchObject({
      kind: "bash",
      path: bash,
      available: true,
      source: "discovery",
    });
  });

  it("accepts an absolute executable path override", () => {
    const resolved = resolveShell("/usr/local/bin/fish", {
      platform: "linux",
      env: {},
      exists: (candidate) => candidate === "/usr/local/bin/fish",
      which: () => null,
    });
    expect(resolved).toMatchObject({
      kind: "unknown",
      path: "/usr/local/bin/fish",
      available: true,
      source: "path",
    });
  });

  it("marks missing kinds unavailable", () => {
    const resolved = resolveShell("wsl", {
      platform: "win32",
      env: { SystemRoot: "C:\\Windows" },
      exists: () => false,
      which: () => null,
    });
    expect(resolved.available).toBe(false);
    expect(resolved.kind).toBe("wsl");
  });
});

describe("listAvailableShells", () => {
  it("lists only available shells and includes auto default", () => {
    const listed = listAvailableShells({
      platform: "linux",
      env: { SHELL: "/bin/bash" },
      exists: (candidate) => candidate === "/bin/bash" || candidate === "/bin/zsh",
      which: (name) => (name === "bash" ? "/bin/bash" : name === "zsh" ? "/bin/zsh" : null),
    });
    expect(listed.defaultKind).toBe("bash");
    expect(listed.shells.map((item) => item.kind)).toEqual(
      expect.arrayContaining(["bash", "zsh"]),
    );
    expect(listed.shells.every((item) => item.available)).toBe(true);
  });

  it("exposes the supported kind vocabulary", () => {
    expect(SHELL_KINDS).toEqual([
      "auto",
      "bash",
      "zsh",
      "pwsh",
      "powershell",
      "cmd",
      "wsl",
    ]);
  });
});

describe("ptyArgsForShell", () => {
  it("returns empty argv for cmd/powershell/wsl (interactive default)", () => {
    expect(ptyArgsForShell("cmd")).toEqual([]);
    expect(ptyArgsForShell("powershell")).toEqual([]);
    expect(ptyArgsForShell("pwsh")).toEqual([]);
    expect(ptyArgsForShell("wsl")).toEqual([]);
  });
});
