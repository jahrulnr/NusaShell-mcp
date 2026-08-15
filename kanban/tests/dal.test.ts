import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

let closeDb: typeof import("../db/connection").closeDb;
let db: typeof import("../db/dal");
let directory: string;

beforeEach(async () => {
  directory = mkdtempSync(join(tmpdir(), "nusashell-kanban-"));
  process.env.NUSASHELL_USER_DATA = directory;
  vi.resetModules();
  db = await import("../db/dal");
  ({ closeDb } = await import("../db/connection"));
  db.runMigrations();
});

afterEach(() => { closeDb(); rmSync(directory, { recursive: true, force: true }); });

describe("Kanban DAL", () => {
  it("creates a project with default columns and tickets", () => {
    const project = db.createProject("Release board");
    expect(db.listProjects()).toHaveLength(1);
    expect(db.getColumns(project.id).map((column) => column.name)).toEqual(["Backlog", "Todo", "In Progress", "Review", "Done"]);
    const ticket = db.createTicket({ project_id: project.id, title: "Ship plugin", description: "Acceptance criteria" });
    expect(db.listTickets({ project_id: project.id })[0].title).toBe("Ship plugin");
    const subtask = db.createSubtask(ticket.id, "Build MCP server");
    const done = db.getDoneColumn(project.id)!;
    db.moveTicket(subtask.id, done.id);
    expect(db.getStoryProgress(ticket.id)).toEqual({ total: 1, completed: 1 });
  });

  it("unlinks tickets when deleting a session", () => {
    const project = db.createProject("Sessions");
    const session = db.createSession({ name: "Agent" });
    const ticket = db.createTicket({ project_id: project.id, title: "Tracked", session_id: session.id });
    expect(db.deleteSession(session.id)).toBe(true);
    expect(db.getTicket(ticket.id)?.session_id).toBeNull();
  });
});
