#!/usr/bin/env node
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  GetPromptRequestSchema,
  ListPromptsRequestSchema,
  ListResourcesRequestSchema,
  ListToolsRequestSchema,
  ReadResourceRequestSchema,
  RootsListChangedNotificationSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { fileURLToPath } from "node:url";
import { loadRootFromEnvironment } from "./config.js";
import { safeFilesError } from "./errors.js";
import { FileService } from "./fs-service.js";
import { ContextEngine } from "./context-engine.js";
import { callFilesTool, FILES_TOOLS } from "./tools.js";
import { getFilesPrompt, FILES_PROMPTS } from "./prompts.js";
import { RetrievalEngine } from "./search-relevant.js";
import { formatFilesToolText, mcpToolResult } from "./agent-output.js";

async function main() {
  const root = await loadRootFromEnvironment();
  const service = new FileService(root);
  const contextEngine = new ContextEngine(service.root);
  const retrievalEngine = new RetrievalEngine(service.root);
  const server = new Server(
    { name: "nusashell-files", version: "0.1.1" },
    { capabilities: { tools: {}, prompts: {}, resources: {} } },
  );

  server.setRequestHandler(ListPromptsRequestSchema, async () => ({
    prompts: FILES_PROMPTS,
  }));

  server.setRequestHandler(GetPromptRequestSchema, async (request) =>
    getFilesPrompt(request.params.name));

  server.setRequestHandler(ListResourcesRequestSchema, async () => {
    const instructions = await contextEngine.readWorkspaceInstructions();
    return {
      resources: instructions ? [{
        uri: instructions.uri,
        name: instructions.name,
        description: instructions.description,
        mimeType: instructions.mimeType,
      }] : [],
    };
  });

  server.setRequestHandler(ReadResourceRequestSchema, async (request) => {
    const instructions = await contextEngine.readWorkspaceInstructions();
    if (!instructions || request.params.uri !== instructions.uri) {
      throw new Error(`Unknown workspace resource: ${request.params.uri}`);
    }
    return {
      contents: [{
        uri: instructions.uri,
        mimeType: instructions.mimeType,
        text: instructions.text,
      }],
    };
  });

  // MCP Roots: the shell client advertises roots and notifies on change.
  // Fetch the workspace root on connect and re-fetch on roots/list_changed,
  // updating the in-process root without a process restart (Phase 2).
  async function refreshRoots() {
    try {
      const result = await server.listRoots();
      const fileRoot = result.roots.find((r) => r.uri.startsWith("file:"));
      if (fileRoot) {
        const fsPath = fileURLToPath(fileRoot.uri);
        await service.setRoot(fsPath);
        await contextEngine.setRoot(fsPath);
        retrievalEngine.setRoot(fsPath);
        process.stderr.write(`[nusashell-files] root=${service.root} (via roots)\n`);
      }
    } catch (error) {
      // Client does not support roots, or request failed — keep the env root.
      process.stderr.write(`[nusashell-files] roots refresh skipped: ${safeFilesError(error)}\n`);
    }
  }

  server.setRequestHandler(ListToolsRequestSchema, async () => ({
    tools: FILES_TOOLS,
  }));

  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    try {
      const result = await callFilesTool(
        service,
        request.params.name,
        request.params.arguments ?? {},
        contextEngine,
        retrievalEngine,
      );
      // Emit automation notifications for mutating operations (Watch→Agent demo).
      emitAutomationForTool(server, request.params.name, request.params.arguments ?? {});
      return mcpToolResult(formatFilesToolText(request.params.name, result), result);
    } catch (error) {
      const safeError = safeFilesError(error);
      process.stderr.write(
        `[nusashell-files] tool failed name=${request.params.name} error=${safeError}\n`,
      );
      return {
        isError: true,
        content: [{
          type: "text",
          text: safeError,
        }],
      };
    }
  });

  async function shutdown() {
    await server.close();
  }

  process.once("SIGINT", () => void shutdown());
  process.once("SIGTERM", () => void shutdown());

  const transport = new StdioServerTransport();
  await server.connect(transport);
  process.stderr.write(`[nusashell-files] root=${root}\n`);

  server.setNotificationHandler(RootsListChangedNotificationSchema, async () => {
    await refreshRoots();
  });
  await refreshRoots();
}

void main().catch((error) => {
  process.stderr.write(`[nusashell-files] ${safeFilesError(error)}\n`);
  process.exitCode = 1;
});

/**
 * Emit NusaShell automation notifications for mutating file operations.
 * This is the demo path for the Watch→Agent loop: a file write/delete/move
 * emits a typed event that the shell can match against event-triggered jobs.
 */
function emitAutomationForTool(server, toolName, args) {
  const notifications = {
    write: () => ({ type: "files.modified", payload: { path: args.path, action: "write" } }),
    patch: () => ({ type: "files.modified", payload: { path: args.path, action: "patch" } }),
    append: () => ({ type: "files.modified", payload: { path: args.path, action: "append" } }),
    mkdir: () => ({ type: "files.modified", payload: { path: args.path, action: "mkdir" } }),
    touch: () => ({ type: "files.modified", payload: { path: args.path, action: "touch" } }),
    delete: () => ({ type: "files.deleted", payload: { path: args.path, recursive: !!args.recursive } }),
    move: () => ({ type: "files.moved", payload: { source: args.source, destination: args.destination } }),
    copy: () => ({ type: "files.moved", payload: { source: args.source, destination: args.destination } }),
  };
  const builder = notifications[toolName];
  if (!builder) return;
  const { type, payload } = builder();
  server.notification({ method: "notifications/nusashell/automation", params: { type, payload } });
}
