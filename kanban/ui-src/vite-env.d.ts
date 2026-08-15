/// <reference types="vite/client" />

declare global {
  interface Window {
    shell?: { callTool: (pluginId: string, toolName: string, args: Record<string, unknown>) => Promise<unknown> };
  }
}

export {};
