import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { NoteService } from "../mcp/note-service.js";
import { callNotesTool, NOTES_TOOLS } from "../mcp/tools.js";
import { NOTES_TOOL_NAMES } from "../mcp/tool-catalog.js";

let tmpDir;
let dataFile;
let origEnv;
let service;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "notes-tools-test-"));
  dataFile = path.join(tmpDir, "notes.json");
  origEnv = process.env.NUSASHELL_NOTES_DATA_FILE;
  process.env.NUSASHELL_NOTES_DATA_FILE = dataFile;
  service = new NoteService();
  await service.load();
});

afterEach(async () => {
  if (origEnv === undefined) delete process.env.NUSASHELL_NOTES_DATA_FILE;
  else process.env.NUSASHELL_NOTES_DATA_FILE = origEnv;
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("NOTES_TOOLS", () => {
  it("has exactly the canonical tool names", () => {
    expect(NOTES_TOOLS.map((t) => t.name)).toEqual(NOTES_TOOL_NAMES);
  });

  it("every tool has name, description, inputSchema, and annotations", () => {
    for (const tool of NOTES_TOOLS) {
      expect(tool.name).toBeTruthy();
      expect(tool.description).toBeTruthy();
      expect(tool.inputSchema).toBeDefined();
      expect(tool.inputSchema.type).toBe("object");
      expect(tool.annotations).toBeDefined();
    }
  });
});

describe("callNotesTool - create", () => {
  it("creates a note with text and tags", async () => {
    const result = await callNotesTool(service, "create", {
      text: "hello world",
      tags: ["work"],
    }, { persist: false });
    expect(result.note.id).toBe(1);
    expect(result.note.text).toBe("hello world");
    expect(result.note.tags).toEqual(["work"]);
    expect(result.totalNotes).toBe(1);
  });

  it("creates a note without tags (default)", async () => {
    const result = await callNotesTool(service, "create", {
      text: "no tags",
    }, { persist: false });
    expect(result.note.tags).toEqual([]);
  });

  it("rejects missing text", async () => {
    await expect(callNotesTool(service, "create", {}, { persist: false })).rejects.toThrow();
  });

  it("rejects extra fields", async () => {
    await expect(
      callNotesTool(service, "create", { text: "x", extra: true }, { persist: false }),
    ).rejects.toThrow();
  });
});

describe("callNotesTool - list", () => {
  it("lists all notes", async () => {
    await callNotesTool(service, "create", { text: "n1" }, { persist: false });
    await callNotesTool(service, "create", { text: "n2" }, { persist: false });
    const result = await callNotesTool(service, "list", {}, { persist: false });
    expect(result.notes).toHaveLength(2);
    expect(result.total).toBe(2);
  });

  it("filters by tag", async () => {
    await callNotesTool(service, "create", { text: "n1", tags: ["work"] }, { persist: false });
    await callNotesTool(service, "create", { text: "n2", tags: ["personal"] }, { persist: false });
    const result = await callNotesTool(service, "list", { tag: "work" }, { persist: false });
    expect(result.notes).toHaveLength(1);
    expect(result.tag).toBe("work");
  });

  it("sorts by updated by default", async () => {
    const r1 = await callNotesTool(service, "create", { text: "first" }, { persist: false });
    const r2 = await callNotesTool(service, "create", { text: "second" }, { persist: false });
    await callNotesTool(service, "update", { id: r1.note.id, text: "updated first" }, { persist: false });
    const result = await callNotesTool(service, "list", {}, { persist: false });
    expect(result.notes[0].id).toBe(r1.note.id);
    expect(result.sort).toBe("updated");
  });

  it("sorts by created when specified", async () => {
    const r1 = await callNotesTool(service, "create", { text: "first" }, { persist: false });
    const r2 = await callNotesTool(service, "create", { text: "second" }, { persist: false });
    await callNotesTool(service, "update", { id: r1.note.id, text: "updated first" }, { persist: false });
    const result = await callNotesTool(service, "list", { sort: "created" }, { persist: false });
    expect(result.sort).toBe("created");
    expect(result.notes).toHaveLength(2);
    expect(result.notes.map((n) => n.id)).toContain(r1.note.id);
    expect(result.notes.map((n) => n.id)).toContain(r2.note.id);
  });
});

describe("callNotesTool - get", () => {
  it("returns a note by id", async () => {
    const created = await callNotesTool(service, "create", { text: "find me" }, { persist: false });
    const result = await callNotesTool(service, "get", { id: created.note.id }, { persist: false });
    expect(result.note.text).toBe("find me");
  });

  it("rejects missing id", async () => {
    await expect(callNotesTool(service, "get", {}, { persist: false })).rejects.toThrow();
  });

  it("rejects non-existent id (service throws)", async () => {
    await expect(callNotesTool(service, "get", { id: 999 }, { persist: false })).rejects.toThrow("Note not found");
  });
});

describe("callNotesTool - update", () => {
  it("updates text", async () => {
    const created = await callNotesTool(service, "create", { text: "original", tags: ["t1"] }, { persist: false });
    const result = await callNotesTool(service, "update", {
      id: created.note.id,
      text: "updated",
    }, { persist: false });
    expect(result.note.text).toBe("updated");
    expect(result.note.tags).toEqual(["t1"]);
  });

  it("updates tags", async () => {
    const created = await callNotesTool(service, "create", { text: "original", tags: ["t1"] }, { persist: false });
    const result = await callNotesTool(service, "update", {
      id: created.note.id,
      tags: ["new"],
    }, { persist: false });
    expect(result.note.tags).toEqual(["new"]);
    expect(result.note.text).toBe("original");
  });
});

describe("callNotesTool - delete", () => {
  it("deletes a note", async () => {
    const created = await callNotesTool(service, "create", { text: "delete me" }, { persist: false });
    const result = await callNotesTool(service, "delete", { id: created.note.id }, { persist: false });
    expect(result.deleted.id).toBe(created.note.id);
    expect(result.totalNotes).toBe(0);
  });
});

describe("callNotesTool - search", () => {
  it("finds matching notes", async () => {
    await callNotesTool(service, "create", { text: "# Project meeting" }, { persist: false });
    await callNotesTool(service, "create", { text: "Shopping list" }, { persist: false });
    const result = await callNotesTool(service, "search", { query: "project" }, { persist: false });
    expect(result.results).toHaveLength(1);
    expect(result.total).toBe(1);
  });

  it("rejects missing query", async () => {
    await expect(callNotesTool(service, "search", {}, { persist: false })).rejects.toThrow();
  });
});

describe("callNotesTool - unknown tool", () => {
  it("throws for unknown tool name", async () => {
    await expect(callNotesTool(service, "unknown_tool", {}, { persist: false })).rejects.toThrow();
  });
});
