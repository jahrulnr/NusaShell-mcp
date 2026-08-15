import {
  unwrapToolPayload,
  extractText,
  parseToolJson,
  friendlyError,
  parseHostToolResult,
} from "./terminal-bridge.js";

const pluginId = new URLSearchParams(location.search).get("pluginId") || "nusashell.terminal";

// Version marker so we can tell in devtools whether the new UI bundle loaded.
window.__TERMINAL_UI_VERSION__ = "2026-07-31-colors-1";
console.info("[terminal-ui] loaded", window.__TERMINAL_UI_VERSION__);

const els = {
  container: document.getElementById("terminal-container"),
  overlay: document.getElementById("terminal-overlay"),
  overlayTitle: document.getElementById("overlay-title"),
  overlayMessage: document.getElementById("overlay-message"),
  statusCwd: document.getElementById("status-cwd"),
  statusShell: document.getElementById("status-shell"),
  statusSession: document.getElementById("status-session"),
  statusState: document.getElementById("status-state"),
  newSession: document.getElementById("new-session-button"),
  clear: document.getElementById("clear-button"),
  retry: document.getElementById("retry-button"),
};

const POLL_MS = 60;

let term;
let fitAddon;
let sessionId = null;
let pollTimer = null;
let stopped = false;
let pollInFlight = false;

function setStatus(state, text) {
  els.statusState.dataset.state = state;
  els.statusState.textContent = text;
}

function showOverlay(title, message) {
  if (els.overlayTitle) els.overlayTitle.textContent = title || "Terminal unavailable";
  if (message) els.overlayMessage.textContent = message;
  els.overlay.classList.add("is-visible");
  els.overlay.hidden = false;
  els.overlay.removeAttribute("hidden");
  setStatus("error", "offline");
}

function hideOverlay() {
  els.overlay.classList.remove("is-visible");
  els.overlay.hidden = true;
  els.overlay.setAttribute("hidden", "");
}

async function callTool(name, args = {}) {
  if (!window.shell || typeof window.shell.callTool !== "function") {
    throw new Error("NusaShell bridge unavailable. Open Terminal from the NusaShell launcher.");
  }
  const raw = await window.shell.callTool(pluginId, name, args);
  if (raw == null) throw new Error(`No response from terminal tool ${name}.`);

  const payload = unwrapToolPayload(raw);
  if (payload == null) throw new Error(`Empty response from terminal tool ${name}.`);
  if (payload.isError || payload.ok === false || raw.isError || raw.ok === false) {
    const message = extractText(payload) || extractText(raw) || `Tool ${name} failed.`;
    throw new Error(message);
  }
  return payload;
}

function ensureTerminal() {
  if (term) return;
  if (typeof window.Terminal !== "function") {
    throw new Error("xterm.js failed to load. Rebuild the Terminal plugin UI assets.");
  }
  term = new window.Terminal({
    cursorBlink: true,
    cursorStyle: "block",
    cursorWidth: 1,
    convertEol: true,
    allowProposedApi: true,
    fontSize: 13,
    lineHeight: 1.2,
    fontFamily: "SF Mono, Cascadia Code, JetBrains Mono, Menlo, Consolas, monospace",
    theme: {
      background: "#0b0f14",
      foreground: "#e5e9f0",
      cursor: "#4cc2ff",
      cursorAccent: "#0b0f14",
      selectionBackground: "rgba(76,194,255,0.28)",
      selectionForeground: "#e5e9f0",
      // Standard ANSI 16 — needed for ls/git/prompt colors to render.
      black: "#1b222c",
      red: "#ff7b72",
      green: "#7ee787",
      yellow: "#e3b341",
      blue: "#79c0ff",
      magenta: "#d2a8ff",
      cyan: "#4cc2ff",
      white: "#e5e9f0",
      brightBlack: "#6e7681",
      brightRed: "#ffa198",
      brightGreen: "#56d364",
      brightYellow: "#e3b341",
      brightBlue: "#a5d6ff",
      brightMagenta: "#d2a8ff",
      brightCyan: "#79e4ff",
      brightWhite: "#ffffff",
    },
  });
  const FitCtor = window.FitAddon?.FitAddon || window.FitAddon;
  if (typeof FitCtor === "function") {
    fitAddon = new FitCtor();
    term.loadAddon(fitAddon);
  }
  els.container.tabIndex = 0;
  term.open(els.container);
  if (fitAddon) fitAddon.fit();
  term.focus();

  els.container.addEventListener("mousedown", () => {
    term.focus();
  });

  term.onData((data) => {
    if (!sessionId) return;
    callTool("write", { sessionId, data }).catch(() => {});
  });
}

function fitAndResize() {
  if (!term) return;
  if (fitAddon) fitAddon.fit();
  if (sessionId) {
    callTool("resize", { sessionId, cols: term.cols, rows: term.rows }).catch(() => {});
  }
}

async function openSession() {
  ensureTerminal();
  hideOverlay();
  setStatus("starting", "starting…");

  const cols = term.cols || 120;
  const rows = term.rows || 30;
  let lastErr = null;
  for (let attempt = 0; attempt < 10; attempt++) {
    try {
      const payload = await callTool("open", { cols, rows });
      const info = parseToolJson(payload, null) || parseHostToolResult({ result: payload }, null);
      if (!info || !info.sessionId) {
        console.error("[terminal] unexpected open payload", payload);
        throw new Error("Failed to open terminal session.");
      }

      sessionId = info.sessionId;
      els.statusCwd.textContent = info.cwd || "Home";
      els.statusCwd.title = info.cwd || "";
      els.statusShell.textContent = info.shell || "shell";
      els.statusSession.textContent = info.sessionId.slice(0, 8);
      setStatus("running", "running");
      hideOverlay();
      startPolling();
      term.focus();
      return;
    } catch (err) {
      lastErr = err;
      if (!/not running|no active MCP|Backend not ready|PLUGIN_NOT_RUNNING|MCP_CONNECTION/i.test(String(err.message))) {
        throw err;
      }
      await new Promise((r) => setTimeout(r, 300));
    }
  }
  throw lastErr || new Error("Failed to open terminal session.");
}

function startPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollInFlight = false;
  stopped = false;
  pollTimer = setInterval(async () => {
    if (!sessionId || stopped || pollInFlight) return;
    pollInFlight = true;
    try {
      const payload = await callTool("read", { sessionId, clear: true });
      const data = parseToolJson(payload, null);
      if (!data) return;
      if (data.stdout) term.write(data.stdout);
      if (data.stderr) term.write(data.stderr);
      // Some profiles hide the cursor; keep it visible for interactive shell use.
      if (data.stdout || data.stderr) term.write("\x1b[?25h");
      term.focus();
      if (data.exited) {
        setStatus("error", `exited ${data.exitCode ?? ""}`.trim());
        stopPolling();
      }
    } catch (err) {
      console.error("[terminal] poll failed", err);
      setStatus("error", "offline");
      showOverlay(
        "Terminal disconnected",
        friendlyError(err, "The terminal session was interrupted. Retry to reconnect."),
      );
      stopPolling();
    } finally {
      pollInFlight = false;
    }
  }, POLL_MS);
}

function stopPolling() {
  stopped = true;
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = null;
  pollInFlight = false;
}

async function newSession() {
  stopPolling();
  stopped = false;
  if (sessionId) {
    callTool("close", { sessionId }).catch(() => {});
    sessionId = null;
  }
  if (term) term.reset();
  try {
    await openSession();
  } catch (err) {
    console.error("[terminal] new session failed", err);
    showOverlay(
      "Could not open session",
      friendlyError(err, "Could not start a terminal session. Retry or reopen from the launcher."),
    );
  }
}

async function boot() {
  try {
    await openSession();
  } catch (err) {
    console.error("[terminal] boot failed", err);
    showOverlay(
      "Terminal MCP is not ready",
      friendlyError(err, "Start the Terminal plugin from the launcher, then retry."),
    );
  }
}

els.newSession.addEventListener("click", newSession);
els.retry.addEventListener("click", () => {
  stopPolling();
  stopped = false;
  boot();
});
els.clear.addEventListener("click", () => {
  if (term) term.clear();
});
window.addEventListener("resize", fitAndResize);
window.addEventListener("beforeunload", () => {
  if (sessionId) {
    try {
      window.shell.callTool(pluginId, "close", { sessionId });
    } catch (_) {
      /* ignore */
    }
  }
});

hideOverlay();
boot();
