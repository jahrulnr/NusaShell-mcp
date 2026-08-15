#!/usr/bin/env node
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";
import * as db from "../db/index.js";

const priority = z.enum(["urgent", "high", "medium", "low"]);
const text = (value) => ({ content: [{ type: "text", text: JSON.stringify(value, null, 2) }], structuredContent: Array.isArray(value) ? { items: value } : value });
const missing = (message) => ({ isError: true, content: [{ type: "text", text: message }] });

export function createKanbanServer() {
  const server = new McpServer(
    { name: "nusashell-kanban", version: "1.0.0" },
    { instructions: "A local-first Kanban board. Discover projects and columns before creating or moving tickets. Keep ticket descriptions current, move work to In Progress before starting, and move it to Done only when complete." },
  );

  server.tool("list_projects", "List all Kanban projects.", {}, async () => text(db.listProjects()));
  server.tool("create_project", "Create a Kanban project with the default workflow columns.", { name: z.string().trim().min(1).max(100) }, async ({ name }) => text(db.createProject(name)));
  server.tool("list_columns", "List workflow columns for a project. Always use this before move_ticket.", { project_id: z.string().min(1) }, async ({ project_id }) => text(db.getColumns(project_id)));
  server.tool("create_ticket", "Create a story ticket. Provide acceptance criteria in the description.", {
    title: z.string().trim().min(1).max(200), description: z.string().max(20000).optional(), project_id: z.string().optional(), session_id: z.string().optional(), priority: priority.optional(), column_id: z.string().optional(),
  }, async ({ title, description, project_id, session_id, priority, column_id }) => {
    const projectId = project_id ?? db.getOrCreateDefaultProject().id;
    return text(db.createTicket({ title, description, project_id: projectId, session_id, priority, column_id }));
  });
  server.tool("update_ticket", "Update ticket title, description, priority, or session.", {
    ticket_id: z.string().min(1), title: z.string().trim().min(1).max(200).optional(), description: z.string().max(20000).optional(), priority: priority.optional(), session_id: z.string().nullable().optional(),
  }, async ({ ticket_id, ...updates }) => { const ticket = db.updateTicket(ticket_id, updates); return ticket ? text(ticket) : missing("Ticket not found"); });
  server.tool("move_ticket", "Move a ticket to a workflow column. Use list_columns first.", { ticket_id: z.string().min(1), column_id: z.string().min(1), order: z.number().int().min(0).max(100000).optional() }, async ({ ticket_id, column_id, order }) => { const ticket = db.moveTicket(ticket_id, column_id, order); return ticket ? text(ticket) : missing("Ticket not found"); });
  server.tool("delete_ticket", "Delete a ticket and its subtasks.", { ticket_id: z.string().min(1) }, async ({ ticket_id }) => text({ deleted: db.deleteTicket(ticket_id) }));
  server.tool("create_subtask", "Create an actionable subtask under a story.", { parent_ticket_id: z.string().min(1), title: z.string().trim().min(1).max(200), description: z.string().max(20000).optional(), priority: priority.optional() }, async ({ parent_ticket_id, title, description, priority }) => text(db.createSubtask(parent_ticket_id, title, description, priority)));
  server.tool("complete_subtask", "Move a subtask to its project's Done column and report parent progress.", { ticket_id: z.string().min(1) }, async ({ ticket_id }) => {
    const ticket = db.getTicket(ticket_id); if (!ticket) return missing("Ticket not found");
    const done = db.getDoneColumn(ticket.project_id); if (!done) return missing("Done column not found");
    const moved = db.moveTicket(ticket_id, done.id);
    return text({ ticket: moved, progress: ticket.parent_ticket_id ? db.getStoryProgress(ticket.parent_ticket_id) : null });
  });
  server.tool("list_tickets", "List tickets, optionally filtered by project, column, session, or parent.", { project_id: z.string().optional(), column_id: z.string().optional(), session_id: z.string().optional(), parent_ticket_id: z.string().nullable().optional() }, async (filters) => text(db.listTickets(filters)));
  server.tool("get_ticket", "Get a ticket with its subtasks and progress.", { ticket_id: z.string().min(1) }, async ({ ticket_id }) => { const ticket = db.getTicketWithSubtasks(ticket_id); return ticket ? text(ticket) : missing("Ticket not found"); });
  server.tool("create_session", "Create an agent session used to filter board work.", { name: z.string().trim().min(1).max(100), color: z.string().regex(/^#[0-9a-fA-F]{6}$/).optional() }, async ({ name, color }) => text(db.createSession({ name, color })));
  server.tool("delete_session", "Delete a session while preserving and unlinking its tickets.", { session_id: z.string().min(1) }, async ({ session_id }) => { const deleted = db.deleteSession(session_id); return deleted ? text({ deleted: true }) : missing("Session not found"); });
  server.tool("list_sessions", "List all agent sessions.", {}, async () => text(db.listSessions()));

  server.prompt("howto", "How to use the NusaShell Kanban board safely", {}, async () => ({
    messages: [{ role: "user", content: { type: "text", text: "Use this board as the shared work ledger. First call list_projects, then list_columns for the selected project. Create work with create_ticket and keep descriptions useful. Before starting, move_ticket to In Progress; use create_subtask for concrete steps and complete_subtask when each is done; update_ticket with decisions and blockers; move the parent to Done only after acceptance criteria are met. Use list_tickets for board state and get_ticket for details. Do not assume column IDs, do not invent ticket IDs, and do not delete tickets or sessions unless explicitly requested. Projects and all data are local to NusaShell's plugin data directory." } }],
  }));
  return server;
}

async function main() {
  db.runMigrations();
  db.getOrCreateDefaultProject();
  const server = createKanbanServer();
  const shutdown = () => void server.close().finally(() => db.closeDb());
  process.once("SIGINT", shutdown); process.once("SIGTERM", shutdown);
  await server.connect(new StdioServerTransport());
  process.stderr.write("[nusashell-kanban] ready\n");
}

if (process.argv[1]?.endsWith("server.js") || process.argv[1]?.endsWith("server.cjs")) void main().catch((error) => { process.stderr.write(`[nusashell-kanban] ${error instanceof Error ? error.message : "startup failed"}\n`); process.exitCode = 1; });
