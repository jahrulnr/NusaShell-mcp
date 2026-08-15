import { v4 as uuidv4 } from "uuid";
import { getStore, saveStore } from "./connection.js";
import { DEFAULT_COLUMNS } from "./schema.js";
import type {
  Priority,
  CreateTicketInput,
  UpdateTicketInput,
  CreateSessionInput,
  CreateDependencyInput,
  Project,
  Column,
  Session,
  Ticket,
  Attachment,
  Dependency,
} from "../shared/types.js";

// ============================================================
// Migrations — no-op for JSON store (schema is implicit)
// ============================================================

export function runMigrations(): void {
  // JSON store is schema-less; migrations are not needed.
}

// ============================================================
// Projects
// ============================================================

export function createProject(name: string): Project {
  const store = getStore();
  const id = uuidv4();
  const now = new Date().toISOString();
  const project: Project = { id, name, created_at: now };
  store.projects.push(project);

  for (let i = 0; i < DEFAULT_COLUMNS.length; i++) {
    store.columns.push({ id: uuidv4(), project_id: id, name: DEFAULT_COLUMNS[i]!, order: i });
  }

  saveStore();
  return getProject(id)!;
}

export function getProject(id: string): Project | null {
  return getStore().projects.find((p) => p.id === id) ?? null;
}

export function listProjects(): Project[] {
  return [...getStore().projects].sort((a, b) => a.created_at.localeCompare(b.created_at));
}

export function getOrCreateDefaultProject(): Project {
  const all = listProjects();
  if (all.length > 0) return all[0]!;
  return createProject("Default Project");
}

export function updateProject(id: string, updates: { name?: string }): Project | null {
  const store = getStore();
  const project = store.projects.find((p) => p.id === id);
  if (!project) return null;
  if (updates.name !== undefined) project.name = updates.name;
  saveStore();
  return getProject(id);
}

export function deleteProject(id: string): boolean {
  const store = getStore();
  const before = store.projects.length;
  store.projects = store.projects.filter((p) => p.id !== id);
  store.columns = store.columns.filter((c) => c.project_id !== id);
  store.tickets = store.tickets.filter((t) => t.project_id !== id);
  const ticketIds = new Set(store.tickets.map((t) => t.id));
  store.attachments = store.attachments.filter((a) => ticketIds.has(a.ticket_id));
  store.dependencies = store.dependencies.filter(
    (d) => ticketIds.has(d.ticket_id) && ticketIds.has(d.depends_on_ticket_id),
  );
  saveStore();
  return store.projects.length < before;
}

// ============================================================
// Columns
// ============================================================

export function getColumns(projectId: string): Column[] {
  return getStore()
    .columns.filter((c) => c.project_id === projectId)
    .sort((a, b) => a.order - b.order);
}

export function getColumn(id: string): Column | null {
  return getStore().columns.find((c) => c.id === id) ?? null;
}

export function createColumn(projectId: string, name: string, order: number): Column {
  const store = getStore();
  const id = uuidv4();
  const column: Column = { id, project_id: projectId, name, order };
  store.columns.push(column);
  saveStore();
  return column;
}

export function updateColumn(id: string, updates: { name?: string; order?: number }): Column | null {
  const store = getStore();
  const col = store.columns.find((c) => c.id === id);
  if (!col) return null;
  if (updates.name !== undefined) col.name = updates.name;
  if (updates.order !== undefined) col.order = updates.order;
  saveStore();
  return getColumn(id);
}

export function deleteColumn(id: string): boolean {
  const store = getStore();
  const before = store.columns.length;
  store.columns = store.columns.filter((c) => c.id !== id);
  store.tickets = store.tickets.filter((t) => t.column_id !== id);
  saveStore();
  return store.columns.length < before;
}

export function getBacklogColumn(projectId: string): Column | null {
  return getStore().columns.find((c) => c.project_id === projectId && c.name === "Backlog") ?? null;
}

export function getDoneColumn(projectId: string): Column | null {
  return getStore().columns.find((c) => c.project_id === projectId && c.name === "Done") ?? null;
}

// ============================================================
// Sessions
// ============================================================

const SESSION_COLORS = [
  "#3B82F6", "#10B981", "#8B5CF6", "#F59E0B", "#EF4444",
  "#EC4899", "#06B6D4", "#84CC16", "#F97316", "#6366F1",
];

export function createSession(input: CreateSessionInput): Session {
  const store = getStore();
  const id = uuidv4();
  const all = listSessions();
  const color = input.color ?? SESSION_COLORS[all.length % SESSION_COLORS.length]!;
  const now = new Date().toISOString();
  const session: Session = {
    id,
    name: input.name,
    color,
    branch: input.branch ?? null,
    is_worktree: input.is_worktree ?? false,
    created_at: now,
  };
  store.sessions.push(session);
  saveStore();
  return getSession(id)!;
}

export function getSession(id: string): Session | null {
  return getStore().sessions.find((s) => s.id === id) ?? null;
}

export function listSessions(): Session[] {
  return [...getStore().sessions].sort((a, b) => a.created_at.localeCompare(b.created_at));
}

export function findSessionByBranch(branch: string): Session | null {
  return getStore().sessions.find((s) => s.branch === branch) ?? null;
}

export function deleteSession(id: string): boolean {
  const store = getStore();
  const session = getSession(id);
  if (!session) return false;

  const now = new Date().toISOString();
  for (const ticket of store.tickets) {
    if (ticket.session_id === id) {
      ticket.session_id = null;
      ticket.updated_at = now;
    }
  }

  const before = store.sessions.length;
  store.sessions = store.sessions.filter((s) => s.id !== id);
  saveStore();
  return store.sessions.length < before;
}

// ============================================================
// Tickets
// ============================================================

function getNextTicketNumber(projectId: string): number {
  const tickets = getStore().tickets.filter((t) => t.project_id === projectId);
  return (tickets.reduce((max, t) => Math.max(max, t.ticket_number), 0) + 1);
}

function getMaxOrderInColumn(columnId: string): number {
  const tickets = getStore().tickets.filter((t) => t.column_id === columnId);
  return (tickets.reduce((max, t) => Math.max(max, t.order), -1) + 1);
}

export function createTicket(input: CreateTicketInput): Ticket {
  const store = getStore();
  const id = uuidv4();
  const ticketNumber = getNextTicketNumber(input.project_id);

  let columnId = input.column_id;
  if (!columnId) {
    const backlog = getBacklogColumn(input.project_id);
    if (!backlog) throw new Error("No Backlog column found");
    columnId = backlog.id;
  }

  const order = getMaxOrderInColumn(columnId);
  const now = new Date().toISOString();
  const ticket: Ticket = {
    id,
    project_id: input.project_id,
    ticket_number: ticketNumber,
    title: input.title,
    description: input.description ?? null,
    priority: input.priority ?? null,
    column_id: columnId,
    session_id: input.session_id ?? null,
    parent_ticket_id: null,
    order,
    created_at: now,
    updated_at: now,
  };
  store.tickets.push(ticket);
  saveStore();
  return getTicket(id)!;
}

export function createSubtask(
  parentTicketId: string,
  title: string,
  description?: string,
  priority?: Priority,
): Ticket {
  const parent = getTicket(parentTicketId);
  if (!parent) throw new Error("Parent ticket not found");

  const store = getStore();
  const id = uuidv4();
  const ticketNumber = getNextTicketNumber(parent.project_id);
  const order = getMaxOrderInColumn(parent.column_id);
  const now = new Date().toISOString();
  const ticket: Ticket = {
    id,
    project_id: parent.project_id,
    ticket_number: ticketNumber,
    title,
    description: description ?? null,
    priority: priority ?? null,
    column_id: parent.column_id,
    session_id: parent.session_id,
    parent_ticket_id: parentTicketId,
    order,
    created_at: now,
    updated_at: now,
  };
  store.tickets.push(ticket);
  saveStore();
  return getTicket(id)!;
}

export function getTicket(id: string): Ticket | null {
  return getStore().tickets.find((t) => t.id === id) ?? null;
}

export function getTicketWithSubtasks(id: string) {
  const ticket = getTicket(id);
  if (!ticket) return null;

  const subs = getSubtasks(id);
  const progress = getStoryProgress(id);

  return {
    ...ticket,
    subtasks: subs,
    subtask_total: progress.total,
    subtask_completed: progress.completed,
  };
}

export function listTickets(filters?: {
  project_id?: string;
  column_id?: string;
  session_id?: string;
  parent_ticket_id?: string | null;
}): Ticket[] {
  let tickets = [...getStore().tickets];

  if (filters?.project_id) {
    tickets = tickets.filter((t) => t.project_id === filters.project_id);
  }
  if (filters?.column_id) {
    tickets = tickets.filter((t) => t.column_id === filters.column_id);
  }
  if (filters?.session_id) {
    tickets = tickets.filter((t) => t.session_id === filters.session_id);
  }
  if (filters?.parent_ticket_id !== undefined) {
    if (filters.parent_ticket_id === null) {
      tickets = tickets.filter((t) => t.parent_ticket_id === null);
    } else {
      tickets = tickets.filter((t) => t.parent_ticket_id === filters.parent_ticket_id);
    }
  }

  return tickets.sort((a, b) => a.order - b.order);
}

export function updateTicket(id: string, updates: UpdateTicketInput): Ticket | null {
  const store = getStore();
  const ticket = store.tickets.find((t) => t.id === id);
  if (!ticket) return null;

  ticket.updated_at = new Date().toISOString();
  if (updates.title !== undefined) ticket.title = updates.title;
  if (updates.description !== undefined) ticket.description = updates.description;
  if (updates.priority !== undefined) ticket.priority = updates.priority;
  if (updates.session_id !== undefined) ticket.session_id = updates.session_id;

  saveStore();
  return getTicket(id);
}

export function moveTicket(id: string, columnId: string, order?: number): Ticket | null {
  const store = getStore();
  const ticket = store.tickets.find((t) => t.id === id);
  if (!ticket) return null;

  ticket.column_id = columnId;
  ticket.order = order ?? getMaxOrderInColumn(columnId);
  ticket.updated_at = new Date().toISOString();

  saveStore();
  return getTicket(id);
}

export function deleteTicket(id: string): boolean {
  const store = getStore();
  const before = store.tickets.length;
  const toDelete = new Set<string>([id]);
  // Cascade delete subtasks
  for (const t of store.tickets) {
    if (t.parent_ticket_id === id) toDelete.add(t.id);
  }
  store.tickets = store.tickets.filter((t) => !toDelete.has(t.id));
  store.attachments = store.attachments.filter((a) => !toDelete.has(a.ticket_id));
  store.dependencies = store.dependencies.filter(
    (d) => !toDelete.has(d.ticket_id) && !toDelete.has(d.depends_on_ticket_id),
  );
  saveStore();
  return store.tickets.length < before;
}

export function getSubtasks(parentTicketId: string): Ticket[] {
  return getStore()
    .tickets.filter((t) => t.parent_ticket_id === parentTicketId)
    .sort((a, b) => a.order - b.order);
}

export function getStoryProgress(ticketId: string): { total: number; completed: number } {
  const store = getStore();
  const subs = store.tickets.filter((t) => t.parent_ticket_id === ticketId);
  const doneColumnIds = new Set(
    store.columns.filter((c) => c.name === "Done").map((c) => c.id),
  );
  const completed = subs.filter((s) => doneColumnIds.has(s.column_id)).length;
  return { total: subs.length, completed };
}

// ============================================================
// Attachments
// ============================================================

export function createAttachment(ticketId: string, filePath: string, fileType: string): Attachment {
  const store = getStore();
  const id = uuidv4();
  const now = new Date().toISOString();
  const attachment: Attachment = { id, ticket_id: ticketId, file_path: filePath, file_type: fileType, created_at: now };
  store.attachments.push(attachment);
  saveStore();
  return getAttachment(id)!;
}

export function getAttachment(id: string): Attachment | null {
  return getStore().attachments.find((a) => a.id === id) ?? null;
}

export function listAttachments(ticketId: string): Attachment[] {
  return getStore()
    .attachments.filter((a) => a.ticket_id === ticketId)
    .sort((a, b) => a.created_at.localeCompare(b.created_at));
}

export function deleteAttachment(id: string): boolean {
  const store = getStore();
  const before = store.attachments.length;
  store.attachments = store.attachments.filter((a) => a.id !== id);
  saveStore();
  return store.attachments.length < before;
}

// ============================================================
// Dependencies
// ============================================================

export function createDependency(ticketId: string, input: CreateDependencyInput): Dependency {
  const store = getStore();
  const id = uuidv4();
  const dependency: Dependency = {
    id,
    ticket_id: ticketId,
    depends_on_ticket_id: input.depends_on_ticket_id,
    type: input.type,
  };
  store.dependencies.push(dependency);
  saveStore();
  return getDependency(id)!;
}

export function getDependency(id: string): Dependency | null {
  return getStore().dependencies.find((d) => d.id === id) ?? null;
}

export function listDependencies(ticketId: string): Dependency[] {
  return getStore().dependencies.filter(
    (d) => d.ticket_id === ticketId || d.depends_on_ticket_id === ticketId,
  );
}

export function deleteDependency(id: string): boolean {
  const store = getStore();
  const before = store.dependencies.length;
  store.dependencies = store.dependencies.filter((d) => d.id !== id);
  saveStore();
  return store.dependencies.length < before;
}

// ============================================================
// Merge Import
// ============================================================

export function mergeImportedData(importedDbPath: string): void {
  const fs = require("fs") as typeof import("fs");
  const raw = fs.readFileSync(importedDbPath, "utf-8");
  const imported = JSON.parse(raw) as Partial<{
    projects: Project[];
    columns: Column[];
    sessions: Session[];
    tickets: Ticket[];
    attachments: Attachment[];
    dependencies: Dependency[];
  }>;

  const store = getStore();
  const existingIds = {
    projects: new Set(store.projects.map((p) => p.id)),
    columns: new Set(store.columns.map((c) => c.id)),
    sessions: new Set(store.sessions.map((s) => s.id)),
    tickets: new Set(store.tickets.map((t) => t.id)),
    attachments: new Set(store.attachments.map((a) => a.id)),
    dependencies: new Set(store.dependencies.map((d) => d.id)),
  };

  for (const p of imported.projects ?? []) {
    if (!existingIds.projects.has(p.id)) store.projects.push(p);
  }
  for (const c of imported.columns ?? []) {
    if (!existingIds.columns.has(c.id)) store.columns.push(c);
  }
  for (const s of imported.sessions ?? []) {
    if (!existingIds.sessions.has(s.id)) store.sessions.push(s);
  }
  // Insert parent tickets first, then subtasks
  const allTickets = imported.tickets ?? [];
  for (const t of allTickets.filter((t) => t.parent_ticket_id === null)) {
    if (!existingIds.tickets.has(t.id)) store.tickets.push(t);
  }
  for (const t of allTickets.filter((t) => t.parent_ticket_id !== null)) {
    if (!existingIds.tickets.has(t.id)) store.tickets.push(t);
  }
  for (const a of imported.attachments ?? []) {
    if (!existingIds.attachments.has(a.id)) store.attachments.push(a);
  }
  for (const d of imported.dependencies ?? []) {
    if (!existingIds.dependencies.has(d.id)) store.dependencies.push(d);
  }

  saveStore();
}
