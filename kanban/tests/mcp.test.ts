import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

describe("Kanban MCP boundary", () => {
  const source = readFileSync(resolve(import.meta.dirname, "../mcp/server.js"), "utf8");
  it("contains the planned tools and prompt without board host controls", () => {
    for (const name of ["list_projects", "create_project", "create_ticket", "update_ticket", "move_ticket", "delete_ticket", "create_subtask", "complete_subtask", "list_tickets", "get_ticket", "list_columns", "create_session", "delete_session", "list_sessions", "howto"]) expect(source).toContain(`\"${name}\"`);
    expect(source).not.toContain("open_board");
    expect(source).not.toContain("stop_board");
    expect(source).not.toContain("child_process");
  });
});
