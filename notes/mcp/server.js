#!/usr/bin/env node
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  GetPromptRequestSchema,
  ListPromptsRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { safeNotesError } from "./errors.js";
import { getNotesPrompt, NOTES_PROMPTS } from "./prompts.js";
import { NoteService } from "./note-service.js";
import { callNotesTool, NOTES_TOOLS } from "./tools.js";

async function main() {
  const service = new NoteService();
  await service.load();

  const server = new Server(
    { name: "nusashell-notes", version: "1.0.0" },
    { capabilities: { tools: {}, prompts: {} } },
  );

  server.setRequestHandler(ListPromptsRequestSchema, async () => ({
    prompts: NOTES_PROMPTS,
  }));

  server.setRequestHandler(GetPromptRequestSchema, async (request) =>
    getNotesPrompt(request.params.name));

  server.setRequestHandler(ListToolsRequestSchema, async () => ({
    tools: NOTES_TOOLS,
  }));

  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    try {
      const result = await callNotesTool(
        service,
        request.params.name,
        request.params.arguments ?? {},
      );
      return {
        content: [{ type: "text", text: JSON.stringify(result) }],
        structuredContent: result,
      };
    } catch (error) {
      const safeError = safeNotesError(error);
      process.stderr.write(
        `[nusashell-notes] tool failed name=${request.params.name} error=${safeError}\n`,
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
  process.stderr.write(`[nusashell-notes] ready (${service.notes.length} notes loaded)\n`);
}

void main().catch((error) => {
  process.stderr.write(`[nusashell-notes] ${safeNotesError(error)}\n`);
  process.exitCode = 1;
});
