#!/usr/bin/env node
import os from "node:os";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { getTerminalPrompt, TERMINAL_PROMPTS } from "./prompts.js";
import {
  resolveShell,
  listAvailableShells,
  execArgsForShell,
  detectShellKind,
  SHELL_KINDS,
} from "./shell-resolve.js";
import {
  formatExecText,
  formatPtyReadText,
  formatShellsText,
  formatOkText,
  formatSessionOpenText,
  mcpToolResult,
  stripAnsi,
} from "./agent-output.js";
import {
  CallToolRequestSchema,
  GetPromptRequestSchema,
  ListPromptsRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

// Resolve from the launched entrypoint in both ESM dev mode and the bundled
// CJS package. This avoids import.meta/`__dirname` format differences in esbuild
// and keeps packaged resource lookup independent from the current working dir.
const moduleDir = process.argv[1]
  ? path.dirname(path.resolve(process.argv[1]))
  : path.dirname(process.execPath);

let pty;
try {
  pty = require("node-pty");
} catch (err) {
  console.error("[terminal-mcp] node-pty is required for terminal sessions:", err.message);
}

const HOME = os.homedir();
const MAX_BUFFER_CHARS = 200 * 1024;
// Keep shell bootstrap files with the host-owned runtime data. Falling back to
// the OS temp directory is only for running the plugin standalone outside the
// NusaShell broker.
const BOOTSTRAP_ROOT = process.env.NUSASHELL_USER_DATA
  ? path.join(path.resolve(process.env.NUSASHELL_USER_DATA), "runtime")
  : os.tmpdir();
const BOOTSTRAP_DIR = path.join(BOOTSTRAP_ROOT, "terminal-bootstrap");
const BASH_RC = path.join(BOOTSTRAP_DIR, "bashrc");
const ZSH_RC = path.join(BOOTSTRAP_DIR, ".zshrc");
const COLOR_BOOTSTRAP_SRC = path.join(moduleDir, "color-bootstrap.sh");

function ensureBootstrapFiles() {
  fs.mkdirSync(BOOTSTRAP_DIR, { recursive: true });
  const color = fs.readFileSync(COLOR_BOOTSTRAP_SRC, "utf8");
  fs.writeFileSync(
    BASH_RC,
    `# NusaShell bash bootstrap\n[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"\n${color}`,
  );
  fs.writeFileSync(
    ZSH_RC,
    `# NusaShell zsh bootstrap\n[ -f "$HOME/.zshrc" ] && . "$HOME/.zshrc"\n${color}`,
  );
}

function exeBase(shell) {
  return path.posix.basename(String(shell || "").replace(/\\/g, "/")).replace(/\.exe$/i, "").toLowerCase();
}

function shellSpawnArgs(shell) {
  const base = exeBase(shell);
  // Do not pass -i together with --rcfile: bash then errors with
  // "/bin/bash: --: invalid option" under node-pty.
  if (base === "bash") {
    return ["--rcfile", BASH_RC];
  }
  return [];
}

function shellSpawnEnv(shell, baseEnv) {
  const env = { ...baseEnv };
  const base = exeBase(shell);
  if (base === "zsh") {
    env.ZDOTDIR = BOOTSTRAP_DIR;
  }
  return env;
}

try {
  ensureBootstrapFiles();
} catch (err) {
  console.error("[terminal-mcp] failed to write bootstrap rc:", err.message);
}

function defaultCwd() {
  return HOME;
}

function resolveCwd(input) {
  const cwd = typeof input === "string" && input.trim() ? input.trim() : defaultCwd();
  if (!path.isAbsolute(cwd)) {
    throw new Error(`cwd must be an absolute path (got: ${cwd}). The conversation workspace is not applied automatically; pass the full path explicitly.`);
  }
  const stat = fs.statSync(cwd, { throwIfNoEntry: false });
  if (!stat || !stat.isDirectory()) {
    throw new Error(`cwd is not a directory: ${cwd}`);
  }
  return cwd;
}

function requireShell(shellInput) {
  const resolved = resolveShell(shellInput);
  if (!resolved.available) {
    throw new Error(
      `Shell "${shellInput || "auto"}" is not available on this host. Call the shells tool to list installed kinds (${SHELL_KINDS.filter((k) => k !== "auto").join(", ")}).`,
    );
  }
  return resolved;
}

function trimBuffer(text) {
  if (text.length > MAX_BUFFER_CHARS) {
    return { text: text.slice(text.length - MAX_BUFFER_CHARS), truncated: true };
  }
  return { text, truncated: false };
}

const server = new Server(
  { name: "nusashell-terminal", version: "1.0.0" },
  { capabilities: { tools: {}, prompts: {} } },
);

server.setRequestHandler(ListPromptsRequestSchema, async () => ({
  prompts: TERMINAL_PROMPTS,
}));

server.setRequestHandler(GetPromptRequestSchema, async (request) =>
  getTerminalPrompt(request.params.name));

const sessions = new Map();

function createSession(opts = {}) {
  if (!pty) throw new Error("node-pty is not available; rebuild the terminal plugin dependencies.");
  const resolved = requireShell(opts.shell);
  const shell = resolved.path;
  const cwd = resolveCwd(opts.cwd);
  const cols = Number.isFinite(opts.cols) ? Math.max(1, Math.floor(opts.cols)) : 120;
  const rows = Number.isFinite(opts.rows) ? Math.max(1, Math.floor(opts.rows)) : 30;
  const id = randomUUID();

  const baseEnv = {
    ...process.env,
    HOME,
    TERM: "xterm-256color",
    COLORTERM: "truecolor",
  };
  const term = pty.spawn(shell, shellSpawnArgs(shell), {
    name: "xterm-256color",
    cwd,
    cols,
    rows,
    env: shellSpawnEnv(shell, baseEnv),
  });

  const session = {
    id,
    term,
    shell,
    shellKind: resolved.kind === "unknown" ? detectShellKind(shell) : resolved.kind,
    cwd,
    cols,
    rows,
    buffer: "",
    truncated: false,
    createdAt: Date.now(),
    exited: false,
    exitCode: null,
  };

  term.onData((data) => {
    const next = trimBuffer(session.buffer + data);
    session.buffer = next.text;
    if (next.truncated) session.truncated = true;
  });
  term.onExit(({ exitCode }) => {
    session.exited = true;
    session.exitCode = exitCode;
  });

  sessions.set(id, session);
  return session;
}

function getSession(id) {
  const session = sessions.get(id);
  if (!session) throw new Error(`Session not found: ${id}`);
  return session;
}

function drainBuffer(session, clear = true) {
  const stdout = session.buffer;
  const truncated = session.truncated;
  if (clear) {
    session.buffer = "";
    session.truncated = false;
  }
  return { stdout, truncated };
}

function runExec({ command, cwd, timeoutMs, shell: shellInput }, extra) {
  return new Promise((resolve, reject) => {
    if (typeof command !== "string" || !command.trim()) {
      reject(new Error("command is required"));
      return;
    }
    const resolvedCwd = resolveCwd(cwd);
    const resolved = requireShell(shellInput);
    const shell = resolved.path;
    const kind = resolved.kind === "unknown" ? detectShellKind(shell) : resolved.kind;
    const args = execArgsForShell(kind, command);
    const startedAt = Date.now();
    const child = spawn(shell, args, {
      cwd: resolvedCwd,
      env: { ...process.env, HOME },
      windowsHide: true,
    });

    let stdout = "";
    let stderr = "";
    let truncated = false;
    let killed = false;
    const progressToken = extra?._meta?.progressToken;
    const signal = extra?.signal;
    let progressSeq = 0;

    const append = (current, chunk) => {
      const next = trimBuffer(current + chunk);
      if (next.truncated) truncated = true;
      return next.text;
    };

    const sendProgress = (text) => {
      if (progressToken === undefined) return;
      progressSeq++;
      const chunk = stripAnsi(text).slice(-2000);
      extra.sendNotification({
        method: "notifications/progress",
        params: { progressToken, progress: progressSeq, message: chunk },
      }).catch(() => {});
    };

    const timer = timeoutMs
      ? setTimeout(() => {
          killed = true;
          child.kill("SIGKILL");
        }, timeoutMs)
      : null;

    const onAbort = () => {
      killed = true;
      try { child.kill("SIGKILL"); } catch { /* ignore */ }
    };
    if (signal) {
      if (signal.aborted) { onAbort(); }
      else { signal.addEventListener("abort", onAbort, { once: true }); }
    }

    child.stdout.on("data", (chunk) => {
      const text = chunk.toString();
      stdout = append(stdout, text);
      sendProgress(text);
    });
    child.stderr.on("data", (chunk) => {
      const text = chunk.toString();
      stderr = append(stderr, text);
      sendProgress(text);
    });
    child.on("error", (err) => {
      if (timer) clearTimeout(timer);
      if (signal) signal.removeEventListener("abort", onAbort);
      reject(err);
    });
    child.on("close", (code, signalName) => {
      if (timer) clearTimeout(timer);
      if (signal) signal.removeEventListener("abort", onAbort);
      resolve({
        stdout,
        stderr,
        exitCode: code,
        signal: signalName,
        timedOut: killed,
        truncated,
        cwd: resolvedCwd,
        shell,
        shellKind: kind,
        durationMs: Date.now() - startedAt,
      });
    });
  });
}

const SHELL_DESC = `Shell kind or absolute executable path. Kinds: ${SHELL_KINDS.join(", ")}. On Windows, auto prefers pwsh → powershell → Git Bash → cmd.`;

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "exec",
      description:
        "Run a one-shot shell command and return an agent-readable receipt (stdout/stderr sections) plus structured fields. Prefer shells tool before picking a Windows shell. cwd defaults to the user's home directory; pass an absolute cwd for a specific folder.",
      inputSchema: {
        type: "object",
        required: ["command"],
        properties: {
          command: { type: "string", description: "Shell command to execute." },
          cwd: { type: "string", description: `Absolute working directory (default: ${HOME}).` },
          timeoutMs: { type: "number", description: "Optional timeout in milliseconds before the command is killed." },
          shell: { type: "string", description: SHELL_DESC },
        },
      },
    },
    {
      name: "shells",
      description:
        "List shells available on this host (bash, zsh, pwsh, powershell, cmd, wsl) with resolved paths and the auto default. Use before exec/open on Windows.",
      inputSchema: { type: "object", properties: {} },
    },
    {
      name: "open",
      description:
        "Open a new interactive terminal session (PTY). Prefer shells tool to pick a Windows shell kind. cwd defaults to the user's home directory.",
      inputSchema: {
        type: "object",
        properties: {
          shell: { type: "string", description: SHELL_DESC },
          cwd: { type: "string", description: `Absolute working directory (default: ${HOME}).` },
          cols: { type: "number", description: "Columns (default: 120)" },
          rows: { type: "number", description: "Rows (default: 30)" },
        },
      },
    },
    {
      name: "write",
      description: "Write input to a terminal session.",
      inputSchema: {
        type: "object",
        required: ["sessionId", "data"],
        properties: {
          sessionId: { type: "string" },
          data: { type: "string", description: "Text to send to the terminal (include \\n to run a command)." },
        },
      },
    },
    {
      name: "read",
      description:
        "Read buffered output from a terminal session. Agent text strips ANSI by default; structured stdout keeps raw PTY bytes for the UI.",
      inputSchema: {
        type: "object",
        required: ["sessionId"],
        properties: {
          sessionId: { type: "string" },
          clear: { type: "boolean", description: "Clear the buffer after reading (default: true)" },
          stripAnsi: { type: "boolean", description: "Strip ANSI/OSC sequences in the agent text receipt (default: true)." },
        },
      },
    },
    {
      name: "resize",
      description: "Resize a terminal session.",
      inputSchema: {
        type: "object",
        required: ["sessionId", "cols", "rows"],
        properties: {
          sessionId: { type: "string" },
          cols: { type: "number", minimum: 1 },
          rows: { type: "number", minimum: 1 },
        },
      },
    },
    {
      name: "close",
      description: "Close a terminal session.",
      inputSchema: {
        type: "object",
        required: ["sessionId"],
        properties: { sessionId: { type: "string" } },
      },
    },
    {
      name: "list",
      description: "List active terminal sessions.",
      inputSchema: { type: "object", properties: {} },
    },
  ],
}));

server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
  const { name, arguments: args = {} } = request.params;
  try {
    switch (name) {
      case "shells": {
        const listed = listAvailableShells();
        return mcpToolResult(formatShellsText(listed), listed);
      }
      case "exec": {
        const timeoutMs = Number.isFinite(args.timeoutMs) ? Math.max(0, Math.floor(args.timeoutMs)) : null;
        const result = await runExec({
          command: args.command,
          cwd: args.cwd,
          timeoutMs,
          shell: typeof args.shell === "string" ? args.shell : undefined,
        }, extra);
        const stdoutText = stripAnsi(result.stdout);
        const stderrText = stripAnsi(result.stderr);
        const structured = {
          stdout: stdoutText,
          stderr: stderrText,
          exitCode: result.exitCode,
          signal: result.signal,
          timedOut: result.timedOut,
          truncated: result.truncated,
          cwd: result.cwd,
          shell: result.shell,
          shellKind: result.shellKind,
          durationMs: result.durationMs,
        };
        const text = formatExecText({
          ok: result.exitCode === 0 && !result.timedOut,
          exitCode: result.exitCode,
          signal: result.signal,
          shellKind: result.shellKind,
          shell: result.shell,
          cwd: result.cwd,
          timedOut: result.timedOut,
          truncated: result.truncated,
          stdout: stdoutText,
          stderr: stderrText,
          durationMs: result.durationMs,
        });
        return mcpToolResult(text, structured);
      }
      case "open": {
        const session = createSession({
          shell: typeof args.shell === "string" ? args.shell : undefined,
          cwd: args.cwd,
          cols: args.cols,
          rows: args.rows,
        });
        const structured = {
          sessionId: session.id,
          shell: session.shell,
          shellKind: session.shellKind,
          cwd: session.cwd,
          cols: session.cols,
          rows: session.rows,
        };
        return mcpToolResult(formatSessionOpenText(structured), structured);
      }
      case "write": {
        const session = getSession(args.sessionId);
        if (session.exited) throw new Error("Session has exited");
        session.term.write(String(args.data ?? ""));
        return mcpToolResult(
          formatOkText({ session_id: session.id, written: true }),
          { ok: true, sessionId: session.id },
        );
      }
      case "read": {
        const session = getSession(args.sessionId);
        const clear = args.clear === undefined ? true : Boolean(args.clear);
        const ansiStripped = args.stripAnsi === undefined ? true : Boolean(args.stripAnsi);
        const { stdout, truncated } = drainBuffer(session, clear);
        const structured = {
          stdout, // raw PTY (ANSI) for UI
          stderr: "",
          exited: session.exited,
          exitCode: session.exitCode,
          truncated,
          sessionId: session.id,
          ansiStripped,
        };
        const text = formatPtyReadText({
          sessionId: session.id,
          exited: session.exited,
          exitCode: session.exitCode,
          truncated,
          stdout,
          ansiStripped,
        });
        return mcpToolResult(text, structured);
      }
      case "resize": {
        const session = getSession(args.sessionId);
        const cols = Math.max(1, Math.floor(args.cols));
        const rows = Math.max(1, Math.floor(args.rows));
        session.cols = cols;
        session.rows = rows;
        if (!session.exited) session.term.resize(cols, rows);
        return mcpToolResult(
          formatOkText({ session_id: session.id, cols, rows, resized: true }),
          { ok: true, sessionId: session.id, cols, rows },
        );
      }
      case "close": {
        const session = getSession(args.sessionId);
        if (!session.exited) {
          try { session.term.kill(); } catch { /* ignore */ }
        }
        sessions.delete(args.sessionId);
        return mcpToolResult(
          formatOkText({ session_id: args.sessionId, closed: true }),
          { ok: true, sessionId: args.sessionId },
        );
      }
      case "list": {
        const list = Array.from(sessions.values()).map((session) => ({
          sessionId: session.id,
          shell: session.shell,
          shellKind: session.shellKind,
          cwd: session.cwd,
          cols: session.cols,
          rows: session.rows,
          createdAt: session.createdAt,
          exited: session.exited,
          exitCode: session.exitCode,
        }));
        const structured = { sessions: list, count: list.length };
        const text = [
          `ok=true`,
          `count=${list.length}`,
          "",
          "session_id\tshell\tcwd\texited",
          ...list.map((session) => `${session.sessionId}\t${session.shellKind}\t${session.cwd}\t${session.exited}`),
          "",
        ].join("\n");
        return mcpToolResult(text, structured);
      }
      default:
        throw new Error(`Unknown tool: ${name}`);
    }
  } catch (err) {
    return mcpToolResult(`Error: ${err.message}`, { ok: false, error: err.message }, { isError: true });
  }
});

async function main() {
  const transport = new StdioServerTransport();
  await server.connect(transport);
  console.error("[terminal-mcp] Server running on stdio");
}

main().catch((err) => {
  console.error("[terminal-mcp] fatal:", err);
  process.exit(1);
});
