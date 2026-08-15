#!/usr/bin/env node
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  GetPromptRequestSchema,
  ListPromptsRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { loadAccounts } from "./account-store.js";
import { MailConnectionManager } from "./connections.js";
import { safeMailError } from "./errors.js";
import { MailService } from "./mail-service.js";
import { callMailTool, MAIL_TOOLS } from "./tools.js";
import { getMailPrompt, MAIL_PROMPTS } from "./prompts.js";

const accounts = loadAccounts();
const connections = new MailConnectionManager(accounts);
const service = new MailService(accounts, connections);
const server = new Server(
  { name: "nusashell-mail", version: "0.1.0" },
  { capabilities: { tools: {}, prompts: {} } },
);

server.setRequestHandler(ListPromptsRequestSchema, async () => ({
  prompts: MAIL_PROMPTS,
}));

server.setRequestHandler(GetPromptRequestSchema, async (request) =>
  getMailPrompt(request.params.name));

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: MAIL_TOOLS,
}));

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  try {
    const result = await callMailTool(
      service,
      request.params.name,
      request.params.arguments ?? {},
    );
    return {
      content: [{ type: "text", text: JSON.stringify(result) }],
      structuredContent: result,
    };
  } catch (error) {
    const safeError = safeMailError(error);
    process.stderr.write(
      `[nusashell-mail] tool failed name=${request.params.name} error=${safeError}\n`,
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
  await connections.closeAll();
  await server.close();
}

process.once("SIGINT", () => void shutdown());
process.once("SIGTERM", () => void shutdown());

async function main() {
  const transport = new StdioServerTransport();
  await server.connect(transport);
}

void main().catch((error) => {
  process.stderr.write(`[nusashell-mail] ${safeMailError(error)}\n`);
  process.exitCode = 1;
});
