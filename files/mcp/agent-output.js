/**
 * Agent-readable MCP text receipts for the Files plugin.
 * Structured payloads stay typed for UI; text is what the model sees.
 */

/**
 * @param {number} bytes
 */
function formatSize(bytes) {
  const n = Number(bytes) || 0;
  if (n < 1024) return `${n}B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)}K`;
  return `${(n / (1024 * 1024)).toFixed(1)}M`;
}

/**
 * @param {Record<string, string|number|boolean|null|undefined>} fields
 */
function headerLines(fields) {
  const lines = [];
  for (const [key, value] of Object.entries(fields)) {
    if (value === undefined) continue;
    lines.push(`${key}=${value === null ? "" : String(value)}`);
  }
  return lines.join("\n");
}

function displayPath(value) {
  if (value === undefined || value === null || value === "") return ".";
  return String(value);
}

/**
 * @param {object} result
 */
export function formatListText(result) {
  const items = Array.isArray(result.items) ? result.items : [];
  const lines = [
    headerLines({
      ok: true,
      path: displayPath(result.path),
      count: items.length,
    }),
    "",
  ];
  for (const item of items) {
    if (item.isDir) {
      lines.push(`d  ${item.name}/`);
    } else {
      lines.push(`f  ${item.name}  ${formatSize(item.size)}  ${item.type || "file"}`);
    }
  }
  lines.push("");
  return lines.join("\n");
}

/**
 * @param {object[]} nodes
 * @param {number} indent
 * @param {string[]} lines
 */
function appendTreeNodes(nodes, indent, lines) {
  for (const node of nodes ?? []) {
    const pad = "  ".repeat(indent);
    if (node.isDir) {
      lines.push(`${pad}${node.name}/`);
      if (Array.isArray(node.children) && node.children.length > 0) {
        appendTreeNodes(node.children, indent + 1, lines);
      }
    } else {
      lines.push(`${pad}${node.name}`);
    }
  }
}

/**
 * @param {object} result
 */
export function formatTreeText(result) {
  const tree = Array.isArray(result.tree) ? result.tree : [];
  const lines = [
    headerLines({
      ok: true,
      path: displayPath(result.path),
      depth: result.depth,
      count: tree.length,
    }),
    "",
  ];
  appendTreeNodes(tree, 0, lines);
  lines.push("");
  return lines.join("\n");
}

/**
 * @param {object} result
 */
export function formatReadText(result) {
  const header = headerLines({
    ok: true,
    path: result.path,
    lines: result.totalLines,
    bytes: result.totalBytes,
    truncated: Boolean(result.truncated),
  });
  return [
    header,
    "",
    "=== content ===",
    String(result.content ?? "").replace(/\s+$/u, ""),
    "",
  ].join("\n");
}

/**
 * @param {object} result
 */
export function formatGrepText(result) {
  const results = Array.isArray(result.results) ? result.results : [];
  const meta = result.meta ?? {};
  const lines = [
    headerLines({
      ok: true,
      path: displayPath(result.path),
      pattern: result.pattern,
      count: meta.count ?? results.length,
      truncated: Boolean(meta.truncated),
    }),
    "",
  ];
  for (const hit of results) {
    lines.push(`${hit.path}:${hit.line}:${hit.content ?? ""}`);
  }
  lines.push("");
  return lines.join("\n");
}

/**
 * @param {object} result
 */
export function formatSearchText(result) {
  const results = Array.isArray(result.results) ? result.results : [];
  const meta = result.meta ?? {};
  const lines = [
    headerLines({
      ok: true,
      path: displayPath(result.path),
      pattern: result.pattern,
      count: meta.count ?? results.length,
      truncated: Boolean(meta.truncated),
    }),
    "",
  ];
  for (const hit of results) {
    const kind = hit.isDir ? "dir " : "file";
    lines.push(`${kind}  ${hit.path}`);
  }
  lines.push("");
  return lines.join("\n");
}

/**
 * @param {object} result
 */
export function formatExistsText(result) {
  return `${headerLines({
    ok: true,
    path: result.path,
    exists: Boolean(result.exists),
    is_file: Boolean(result.isFile),
    is_dir: Boolean(result.isDir),
  })}\n`;
}

/**
 * @param {Record<string, unknown>} result
 */
export function formatMutationText(result) {
  const fields = { ok: true };
  for (const [key, value] of Object.entries(result ?? {})) {
    if (value === null || typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
      fields[key] = value;
    }
  }
  return `${headerLines(fields)}\n`;
}

/**
 * @param {unknown} result
 */
export function formatGenericText(result) {
  if (result === null || result === undefined) return "ok=true\n";
  if (typeof result !== "object") return `ok=true\nvalue=${String(result)}\n`;
  if (Array.isArray(result)) {
    return `${headerLines({ ok: true, count: result.length })}\n${JSON.stringify(result, null, 2)}\n`;
  }
  const fields = { ok: true };
  const complex = [];
  for (const [key, value] of Object.entries(result)) {
    if (value === null || typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
      fields[key] = value;
    } else {
      complex.push([key, value]);
    }
  }
  const lines = [headerLines(fields), ""];
  for (const [key, value] of complex) {
    lines.push(`=== ${key} ===`);
    lines.push(typeof value === "string" ? value : JSON.stringify(value, null, 2));
    lines.push("");
  }
  return lines.join("\n");
}

/**
 * Pick the best text formatter for a Files tool result.
 * @param {string} toolName
 * @param {object} result
 */
export function formatFilesToolText(toolName, result) {
  switch (toolName) {
    case "list":
      return formatListText(result);
    case "tree":
      return formatTreeText(result);
    case "read":
      return formatReadText(result);
    case "grep":
      return formatGrepText(result);
    case "search":
      return formatSearchText(result);
    case "exists":
      return formatExistsText(result);
    case "write":
    case "mkdir":
    case "move":
    case "copy":
    case "delete":
    case "append":
    case "touch":
    case "info":
      return formatMutationText(result);
    case "patch":
      return formatGenericText(result);
    default:
      return formatGenericText(result);
  }
}

/**
 * @param {string} text
 * @param {Record<string, unknown>} structured
 * @param {{ isError?: boolean }} [opts]
 */
export function mcpToolResult(text, structured, opts = {}) {
  const payload = {
    content: [{ type: "text", text: String(text) }],
    structuredContent: structured,
  };
  if (opts.isError) payload.isError = true;
  return payload;
}
