const pluginId = new URLSearchParams(location.search).get("pluginId") || "nusashell.kanban";

interface IpcCallResult {
  requestId: string;
  result: unknown;
}

export async function callTool<T>(name: string, args: Record<string, unknown> = {}): Promise<T> {
  if (!window.shell?.callTool) throw new Error("NusaShell bridge is unavailable");
  const envelope = await window.shell.callTool(pluginId, name, args) as IpcCallResult | T;

  // The IPC handler returns { requestId, result } where `result` is the
  // unwrapped MCP structuredContent. The MCP SDK requires structuredContent
  // to be an object, so the server wraps array results in { items: [...] }.
  const payload = envelope && typeof envelope === "object" && "result" in (envelope as Record<string, unknown>)
    ? (envelope as IpcCallResult).result
    : envelope;

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
