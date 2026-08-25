const pluginId = new URLSearchParams(location.search).get("pluginId") || "nusashell.kanban";

interface IpcCallResult {
  requestId: string;
  result: unknown;
}

interface PluginToolResult {
  content?: Array<{ type?: string; text?: string }>;
  structuredContent?: unknown;
  isError?: boolean;
}

export async function callTool<T>(name: string, args: Record<string, unknown> = {}): Promise<T> {
  if (!window.shell?.callTool) throw new Error("NusaShell bridge is unavailable");
  const envelope = await window.shell.callTool(pluginId, name, args) as
    | IpcCallResult
    | PluginToolResult
    | T;

  // The shim returns r.result from the HTTP response, which is the
  // pluginToolResultDTO: { content, structuredContent, isError }.
  // Older bridges returned { requestId, result } — unwrap that too.
  let payload: unknown = envelope;
  if (payload && typeof payload === "object" && "result" in (payload as Record<string, unknown>)) {
    payload = (payload as IpcCallResult).result;
  }

  // Surface tool-level errors (IsError=true) as exceptions so UI hooks
  // can render them via their catch paths.
  if (payload && typeof payload === "object" && "isError" in (payload as Record<string, unknown>)) {
    const result = payload as PluginToolResult;
    if (result.isError) {
      const text = result.content?.map((c) => c.text).filter(Boolean).join("\n");
      throw new Error(text || `Tool ${name} failed`);
    }
  }

  // Extract structuredContent — the actual tool data. The plugin handler
  // returns { content, structuredContent, isError }; without this unwrap
  // the whole envelope is returned as T, causing array access on an object.
  if (payload && typeof payload === "object" && "structuredContent" in (payload as Record<string, unknown>)) {
    const sc = (payload as PluginToolResult).structuredContent;
    if (sc !== null && sc !== undefined) {
      payload = sc;
    } else {
      // structuredContent was null (server returned a nil slice / nil value).
      // Fall back to parsing the text content so callers still get the real
      // value (e.g. [] for an empty list) instead of the whole envelope,
      // which would crash array consumers with "X is not iterable".
      const text = (payload as PluginToolResult).content
        ?.map((c) => c?.text)
        .filter(Boolean)
        .join("\n");
      if (typeof text === "string") {
        try { payload = JSON.parse(text); } catch { /* keep envelope */ }
      }
    }
  }

  // Some MCP servers wrap array results in { items: [...] } because the
  // MCP spec requires structuredContent to be an object.
  if (payload && typeof payload === "object" && "items" in (payload as Record<string, unknown>) && Array.isArray((payload as { items: unknown }).items)) {
    return (payload as { items: T }).items;
  }

  return payload as T;
}

export const api = {
  get: <T>(tool: string, args?: Record<string, unknown>) => callTool<T>(tool, args),
  post: <T>(tool: string, body: Record<string, unknown>) => callTool<T>(tool, body),
  put: <T>(tool: string, body: Record<string, unknown>) => callTool<T>(tool, body),
  del: <T>(tool: string, body: Record<string, unknown>) => callTool<T>(tool, body),
};
