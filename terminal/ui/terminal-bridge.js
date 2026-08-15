/**
 * Pure helpers for Terminal plugin UI ↔ host tool bridge.
 * Kept free of DOM so vitest can cover host payload shapes.
 */

export function unwrapToolPayload(raw) {
  if (raw == null) return null;
  return raw.structuredContent ?? raw.data ?? raw.result ?? raw;
}

export function isContentParts(value) {
  return Array.isArray(value) && value.some((part) => part && typeof part === "object" && part.type === "text");
}

export function extractText(payload) {
  if (typeof payload === "string") return payload;
  if (isContentParts(payload)) {
    return payload.map((c) => (typeof c?.text === "string" ? c.text : "")).join("");
  }
  if (isContentParts(payload?.content)) {
    return payload.content.map((c) => (typeof c?.text === "string" ? c.text : "")).join("");
  }
  return "";
}

export function parseJsonText(text, fallback = null) {
  if (!text) return fallback;
  try {
    return JSON.parse(text);
  } catch {
    return fallback;
  }
}

export function parseToolJson(payload, fallback = null) {
  const text = extractText(payload);
  if (text) return parseJsonText(text, fallback);
  if (payload && typeof payload === "object" && !Array.isArray(payload) && !payload.content) {
    return payload;
  }
  return fallback;
}

/**
 * Map host/MCP errors to short UI copy. Never surface raw JSON or MCP content arrays.
 */
export function friendlyError(err, fallback) {
  const message = String(err?.message || err || "").trim();
  if (!message) return fallback;
  if (/bridge unavailable/i.test(message)) {
    return "Open Terminal from the NusaShell launcher.";
  }
  if (/not running|no active MCP|Backend not ready|PLUGIN_NOT_RUNNING|MCP_CONNECTION/i.test(message)) {
    return "Start the Terminal plugin from the launcher, then retry.";
  }
  if (/node-pty|pty/i.test(message)) {
    return "Terminal PTY is unavailable. Rebuild the Terminal plugin dependencies.";
  }
  if (/session not found/i.test(message)) {
    return "That terminal session ended. Start a new session.";
  }
  if (/^\s*[\[{]/.test(message) || /"type"\s*:\s*"text"/.test(message)) {
    return fallback;
  }
  if (message.length > 180) return fallback;
  return message.replace(/^Error:\s*/i, "");
}

/**
 * Resolve a successful open / read payload from any host shape.
 */
export function parseHostToolResult(raw, fallback = null) {
  const payload = unwrapToolPayload(raw);
  if (payload == null) return fallback;
  if (payload.isError || payload.ok === false || raw?.isError || raw?.ok === false) {
    return fallback;
  }
  return parseToolJson(payload, fallback);
}
