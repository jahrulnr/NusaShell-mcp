import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";

/** @typedef {"auto"|"bash"|"zsh"|"pwsh"|"powershell"|"cmd"|"wsl"|"unknown"} ShellKind */

export const SHELL_KINDS = Object.freeze([
  "auto",
  "bash",
  "zsh",
  "pwsh",
  "powershell",
  "cmd",
  "wsl",
]);

/**
 * Basename that understands both `/` and `\\` so Windows paths resolve on Linux CI.
 * @param {string} [executable]
 */
function exeBase(executable) {
  const normalized = String(executable || "").replace(/\\/g, "/");
  return path.posix.basename(normalized).replace(/\.exe$/i, "").toLowerCase();
}

/**
 * @param {string} [executable]
 * @returns {ShellKind}
 */
export function detectShellKind(executable) {
  const base = exeBase(executable);
  if (base === "bash") return "bash";
  if (base === "zsh") return "zsh";
  if (base === "pwsh") return "pwsh";
  if (base === "powershell") return "powershell";
  if (base === "cmd") return "cmd";
  if (base === "wsl") return "wsl";
  return "unknown";
}

/**
 * @param {ShellKind|string} kind
 * @param {string} command
 * @returns {string[]}
 */
export function execArgsForShell(kind, command) {
  switch (kind) {
    case "bash":
    case "zsh":
      return ["-lc", command];
    case "powershell":
    case "pwsh":
      return ["-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command];
    case "cmd":
      return ["/d", "/s", "/c", command];
    case "wsl":
      return ["-e", "bash", "-lc", command];
    default:
      return process.platform === "win32" ? ["/d", "/s", "/c", command] : ["-lc", command];
  }
}

/**
 * Interactive PTY argv extras (bootstrap handled separately for bash/zsh).
 * @param {ShellKind|string} kind
 * @returns {string[]}
 */
export function ptyArgsForShell(kind) {
  switch (kind) {
    case "bash":
    case "zsh":
    case "cmd":
    case "powershell":
    case "pwsh":
    case "wsl":
    default:
      return [];
  }
}

/**
 * @typedef {object} ShellResolveDeps
 * @property {NodeJS.Platform} [platform]
 * @property {NodeJS.ProcessEnv} [env]
 * @property {(candidate: string) => boolean} [exists]
 * @property {(name: string) => string|null} [which]
 */

/**
 * @typedef {object} ResolvedShell
 * @property {ShellKind} kind
 * @property {string} path
 * @property {boolean} available
 * @property {"env"|"which"|"discovery"|"path"|"fallback"} source
 */

/**
 * @param {ShellResolveDeps} [deps]
 */
function normalizeDeps(deps = {}) {
  return {
    platform: deps.platform ?? process.platform,
    env: deps.env ?? process.env,
    exists: deps.exists ?? ((candidate) => {
      try {
        return fs.existsSync(candidate);
      } catch {
        return false;
      }
    }),
    which: deps.which ?? defaultWhich,
  };
}

/**
 * @param {string} name
 * @returns {string|null}
 */
function defaultWhich(name) {
  try {
    if (process.platform === "win32") {
      const out = execFileSync("where.exe", [name], {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
        windowsHide: true,
      });
      const first = String(out).split(/\r?\n/).map((line) => line.trim()).find(Boolean);
      return first || null;
    }
    const out = execFileSync("which", [name], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    });
    const first = String(out).split(/\r?\n/).map((line) => line.trim()).find(Boolean);
    return first || null;
  } catch {
    return null;
  }
}

/**
 * @param {ShellResolveDeps} deps
 * @param {string[]} candidates
 * @returns {string|null}
 */
function firstExisting(deps, candidates) {
  for (const candidate of candidates) {
    if (candidate && deps.exists(candidate)) return candidate;
  }
  return null;
}

/**
 * @param {ShellResolveDeps} deps
 * @param {string} name
 * @param {string[]} [extra]
 * @returns {{ path: string, source: ResolvedShell["source"] }|null}
 */
function locateNamed(deps, name, extra = []) {
  const fromWhich = deps.which(name);
  if (fromWhich && deps.exists(fromWhich)) {
    return { path: fromWhich, source: "which" };
  }
  const discovered = firstExisting(deps, extra);
  if (discovered) return { path: discovered, source: "discovery" };
  return null;
}

/**
 * @param {ShellResolveDeps} deps
 * @returns {string[]}
 */
function winGitBashCandidates(deps) {
  const programFiles = deps.env.ProgramFiles || "C:\\Program Files";
  const programFilesX86 = deps.env["ProgramFiles(x86)"] || "C:\\Program Files (x86)";
  const localAppData = deps.env.LOCALAPPDATA || "";
  return [
    path.win32.join(programFiles, "Git", "bin", "bash.exe"),
    path.win32.join(programFiles, "Git", "usr", "bin", "bash.exe"),
    path.win32.join(programFilesX86, "Git", "bin", "bash.exe"),
    localAppData ? path.win32.join(localAppData, "Programs", "Git", "bin", "bash.exe") : "",
  ].filter(Boolean);
}

/**
 * @param {ShellResolveDeps} deps
 * @returns {string[]}
 */
function winPwshCandidates(deps) {
  const programFiles = deps.env.ProgramFiles || "C:\\Program Files";
  return [
    path.win32.join(programFiles, "PowerShell", "7", "pwsh.exe"),
    path.win32.join(programFiles, "PowerShell", "7-preview", "pwsh.exe"),
  ];
}

/**
 * @param {ShellResolveDeps} deps
 * @returns {string[]}
 */
function winPowershellCandidates(deps) {
  const root = deps.env.SystemRoot || deps.env.windir || "C:\\Windows";
  return [path.win32.join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")];
}

/**
 * @param {ShellResolveDeps} deps
 * @returns {string[]}
 */
function winCmdCandidates(deps) {
  const comSpec = deps.env.ComSpec;
  const root = deps.env.SystemRoot || deps.env.windir || "C:\\Windows";
  return [comSpec, path.win32.join(root, "System32", "cmd.exe")].filter(Boolean);
}

/**
 * @param {ShellResolveDeps} deps
 * @returns {string[]}
 */
function winWslCandidates(deps) {
  const root = deps.env.SystemRoot || deps.env.windir || "C:\\Windows";
  return [
    path.win32.join(root, "System32", "wsl.exe"),
    "wsl.exe",
  ];
}

/**
 * @param {string} kind
 * @param {ShellResolveDeps} deps
 * @returns {ResolvedShell}
 */
function resolveKind(kind, deps) {
  if (deps.platform === "win32") {
    if (kind === "pwsh") {
      const hit = locateNamed(deps, "pwsh.exe", winPwshCandidates(deps))
        || locateNamed(deps, "pwsh", winPwshCandidates(deps));
      return hit
        ? { kind: "pwsh", path: hit.path, available: true, source: hit.source }
        : { kind: "pwsh", path: "pwsh.exe", available: false, source: "fallback" };
    }
    if (kind === "powershell") {
      const hit = locateNamed(deps, "powershell.exe", winPowershellCandidates(deps));
      return hit
        ? { kind: "powershell", path: hit.path, available: true, source: hit.source }
        : { kind: "powershell", path: "powershell.exe", available: false, source: "fallback" };
    }
    if (kind === "bash") {
      const hit = locateNamed(deps, "bash.exe", winGitBashCandidates(deps))
        || locateNamed(deps, "bash", winGitBashCandidates(deps));
      return hit
        ? { kind: "bash", path: hit.path, available: true, source: hit.source }
        : { kind: "bash", path: "bash.exe", available: false, source: "fallback" };
    }
    if (kind === "zsh") {
      const hit = locateNamed(deps, "zsh.exe") || locateNamed(deps, "zsh");
      return hit
        ? { kind: "zsh", path: hit.path, available: true, source: hit.source }
        : { kind: "zsh", path: "zsh.exe", available: false, source: "fallback" };
    }
    if (kind === "cmd") {
      const hit = firstExisting(deps, winCmdCandidates(deps));
      return hit
        ? { kind: "cmd", path: hit, available: true, source: "discovery" }
        : { kind: "cmd", path: "cmd.exe", available: false, source: "fallback" };
    }
    if (kind === "wsl") {
      const hit = locateNamed(deps, "wsl.exe", winWslCandidates(deps));
      return hit
        ? { kind: "wsl", path: hit.path, available: true, source: hit.source }
        : { kind: "wsl", path: "wsl.exe", available: false, source: "fallback" };
    }
  }

  // Unix / non-Windows
  if (kind === "bash" || kind === "zsh" || kind === "pwsh" || kind === "powershell") {
    const names = kind === "powershell" ? ["powershell", "pwsh"] : [kind];
    for (const name of names) {
      const hit = locateNamed(deps, name, [`/bin/${name}`, `/usr/bin/${name}`, `/usr/local/bin/${name}`]);
      if (hit) {
        return { kind: /** @type {ShellKind} */ (kind === "powershell" && name === "pwsh" ? "pwsh" : kind), path: hit.path, available: true, source: hit.source };
      }
    }
    return { kind: /** @type {ShellKind} */ (kind), path: kind, available: false, source: "fallback" };
  }
  if (kind === "cmd" || kind === "wsl") {
    return { kind: /** @type {ShellKind} */ (kind), path: kind, available: false, source: "fallback" };
  }
  return { kind: "unknown", path: String(kind), available: false, source: "fallback" };
}

/**
 * @param {ShellResolveDeps} deps
 * @returns {ResolvedShell}
 */
function resolveAuto(deps) {
  if (deps.platform === "win32") {
    const configured = deps.env.SHELL;
    if (configured) {
      const base = path.posix.basename(configured).replace(/\.exe$/i, "").toLowerCase();
      if (base === "bash" || base === "zsh") {
        const mapped = resolveKind(base, deps);
        if (mapped.available) return { ...mapped, source: mapped.source === "fallback" ? "env" : mapped.source };
      }
    }
    for (const kind of /** @type {ShellKind[]} */ (["pwsh", "powershell", "bash", "cmd"])) {
      const resolved = resolveKind(kind, deps);
      if (resolved.available) return resolved;
    }
    return { kind: "cmd", path: deps.env.ComSpec || "cmd.exe", available: true, source: "fallback" };
  }

  const configured = deps.env.SHELL;
  if (configured) {
    return {
      kind: detectShellKind(configured),
      path: configured,
      available: true,
      source: "env",
    };
  }
  const bash = resolveKind("bash", deps);
  if (bash.available) return bash;
  return { kind: "bash", path: "/bin/bash", available: true, source: "fallback" };
}

/**
 * @param {string} [shell]
 * @param {ShellResolveDeps} [deps]
 * @returns {ResolvedShell}
 */
export function resolveShell(shell, deps) {
  const normalized = normalizeDeps(deps);
  const raw = typeof shell === "string" && shell.trim() ? shell.trim() : "auto";
  const lower = raw.toLowerCase();

  if (lower === "auto" || lower === "default") {
    return resolveAuto(normalized);
  }

  if (SHELL_KINDS.includes(/** @type {any} */ (lower)) && lower !== "auto") {
    return resolveKind(/** @type {ShellKind} */ (lower), normalized);
  }

  // Absolute / relative executable path override.
  const available = normalized.exists(raw);
  return {
    kind: detectShellKind(raw),
    path: raw,
    available,
    source: "path",
  };
}

/**
 * @param {ShellResolveDeps} [deps]
 */
export function listAvailableShells(deps) {
  const normalized = normalizeDeps(deps);
  const auto = resolveAuto(normalized);
  /** @type {ShellKind[]} */
  const kinds = normalized.platform === "win32"
    ? ["pwsh", "powershell", "bash", "zsh", "cmd", "wsl"]
    : ["bash", "zsh", "pwsh", "powershell"];

  const shells = [];
  const seenKinds = new Set();
  for (const kind of kinds) {
    const resolved = resolveKind(kind, normalized);
    if (!resolved.available) continue;
    if (seenKinds.has(resolved.kind)) continue;
    seenKinds.add(resolved.kind);
    shells.push(resolved);
  }

  // Ensure the auto default appears even if kind list missed it.
  if (auto.available && !seenKinds.has(auto.kind)) {
    shells.unshift(auto);
  } else if (auto.available) {
    // Prefer the auto-resolved path for the default kind.
    const idx = shells.findIndex((item) => item.kind === auto.kind);
    if (idx >= 0) shells[idx] = { ...shells[idx], path: auto.path, source: auto.source };
  }

  return {
    platform: normalized.platform,
    defaultKind: auto.kind === "unknown" ? "auto" : auto.kind,
    defaultPath: auto.path,
    shells,
  };
}
