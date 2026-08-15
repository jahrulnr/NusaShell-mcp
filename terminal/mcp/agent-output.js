/**
 * Agent-readable MCP text receipts for the Terminal plugin.
 * Structured payloads stay typed for the UI; text is what the model sees.
 */

const ANSI_CSI_RE = /\u001b\[[0-9;?]*[ -/]*[@-~]/g;
const ANSI_OSC_RE = /\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)/g;
const ANSI_MISC_RE = /\u001b[@-Z\\-_]/g;

/**
 * @param {string} text
 * @returns {string}
 */
export function stripAnsi(text) {
  return String(text ?? "")
    .replace(ANSI_OSC_RE, "")
    .replace(ANSI_CSI_RE, "")
    .replace(ANSI_MISC_RE, "");
}

/**
 * @param {Record<string, string|number|boolean|null|undefined>} fields
 * @returns {string}
 */
function headerLines(fields) {
  const lines = [];
  for (const [key, value] of Object.entries(fields)) {
    if (value === undefined) continue;
    lines.push(`${key}=${value === null ? "" : String(value)}`);
  }
  return lines.join("\n");
}

/**
 * @param {object} input
 * @param {boolean} input.ok
 * @param {number|null} input.exitCode
 * @param {string} input.shellKind
 * @param {string} input.shell
 * @param {string} input.cwd
 * @param {boolean} input.timedOut
 * @param {boolean} input.truncated
 * @param {string} input.stdout
 * @param {string} input.stderr
 * @param {string|null} [input.signal]
 * @param {number} [input.durationMs]
 */
export function formatExecText(input) {
  const header = headerLines({
    ok: input.ok,
    exit_code: input.exitCode,
    signal: input.signal ?? "",
    shell: input.shellKind,
    shell_path: input.shell,
    cwd: input.cwd,
    timed_out: Boolean(input.timedOut),
    truncated: Boolean(input.truncated),
    ...(input.durationMs !== undefined ? { duration_ms: input.durationMs } : {}),
  });
  return [
    header,
    "",
    "=== stdout ===",
    String(input.stdout ?? "").replace(/\s+$/u, "") ,
    "=== stderr ===",
    String(input.stderr ?? "").replace(/\s+$/u, ""),
    "",
  ].join("\n");
}

/**
 * @param {object} input
 */
export function formatPtyReadText(input) {
  const body = input.ansiStripped
    ? stripAnsi(input.stdout ?? "")
    : String(input.stdout ?? "");
  const header = headerLines({
    ok: true,
    session_id: input.sessionId,
    exited: Boolean(input.exited),
    exit_code: input.exitCode,
    truncated: Boolean(input.truncated),
    ansi_stripped: Boolean(input.ansiStripped),
  });
  return [
    header,
    "",
    "=== output ===",
    body.replace(/\s+$/u, ""),
    "",
  ].join("\n");
}

/**
 * @param {object} input
 */
export function formatShellsText(input) {
  const lines = [
    headerLines({
      ok: true,
      platform: input.platform,
      default: input.defaultKind,
      count: Array.isArray(input.shells) ? input.shells.length : 0,
    }),
    "",
    "kind\tpath\tsource",
  ];
  for (const shell of input.shells ?? []) {
    lines.push(`${shell.kind}\t${shell.path}\t${shell.source}`);
  }
  lines.push("");
  return lines.join("\n");
}

/**
 * @param {Record<string, string|number|boolean|null|undefined>} fields
 */
export function formatOkText(fields) {
  return `${headerLines({ ok: true, ...fields })}\n`;
}

/**
 * @param {object} input
 */
export function formatSessionOpenText(input) {
  return formatOkText({
    session_id: input.sessionId,
    shell: input.shellKind,
    shell_path: input.shell,
    cwd: input.cwd,
    cols: input.cols,
    rows: input.rows,
  });
}

/**
 * @param {string} text
 * @param {Record<string, unknown>} structured
 * @param {{ isError?: boolean }} [opts]
 */
export function mcpToolResult(text, structured, opts = {}) {
  const result = {
    content: [{ type: "text", text: String(text) }],
    structuredContent: structured,
  };
  if (opts.isError) result.isError = true;
  return result;
}
