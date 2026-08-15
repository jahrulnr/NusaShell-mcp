import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { NoteService } from "../mcp/note-service.js";

let tmpDir;
let dataFile;
let origEnv;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "notes-service-test-"));
  dataFile = path.join(tmpDir, "notes.json");
  origEnv = process.env.NUSASHELL_NOTES_DATA_FILE;
  process.env.NUSASHELL_NOTES_DATA_FILE = dataFile;
});

afterEach(async () => {
  if (origEnv === undefined) delete process.env.NUSASHELL_NOTES_DATA_FILE;
  else process.env.NUSASHELL_NOTES_DATA_FILE = origEnv;
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("NoteService.load", () => {
  it("starts empty when no data file exists", async () => {
    const svc = new NoteService();
    await svc.load();
    expect(svc.notes).toEqual([]);
    expect(svc.nextId).toBe(1);
  });

  it("loads existing notes", async () => {
    await fs.writeFile(dataFile, JSON.stringify({
      notes: [
        { id: 1, text: "first", createdAt: "2026-01-01T00:00:00Z" },
        { id: 3, text: "third", createdAt: "2026-01-03T00:00:00Z" },
      ],
    }));
    const svc = new NoteService();
    await svc.load();
    expect(svc.notes).toHaveLength(2);
    expect(svc.nextId).toBe(4);
  });

  it("migrates old notes without tags/updatedAt", async () => {
    await fs.writeFile(dataFile, JSON.stringify({
      notes: [
        { id: 1, text: "old note", createdAt: "2026-01-01T00:00:00Z" },
      ],
    }));
    const svc = new NoteService();
    await svc.load();
    expect(svc.notes[0].tags).toEqual([]);
    expect(svc.notes[0].updatedAt).toBe("2026-01-01T00:00:00Z");
  });
});

describe("NoteService.create", () => {
  it("creates a note with text and tags", async () => {
    const svc = new NoteService();
    await svc.load();
    const note = svc.create("hello world", ["work", "idea"]);
    expect(note.id).toBe(1);
    expect(note.text).toBe("hello world");
    expect(note.tags).toEqual(["work", "idea"]);
    expect(note.createdAt).toBeTruthy();
    expect(note.updatedAt).toBe(note.createdAt);
  });

  it("creates a note without tags", async () => {
    const svc = new NoteService();
    await svc.load();
    const note = svc.create("no tags");
    expect(note.tags).toEqual([]);
  });

  it("rejects empty text", () => {
    const svc = new NoteService();
    expect(() => svc.create("")).toThrow();
  });

  it("rejects non-string text", () => {
    const svc = new NoteService();
    expect(() => svc.create(123)).toThrow();
  });

  it("rejects too many tags", () => {
    const svc = new NoteService();
    const tags = Array(21).fill("tag");
    expect(() => svc.create("text", tags)).toThrow();
  });
});

describe("NoteService.list", () => {
  it("lists all notes", async () => {
    const svc = new NoteService();
    await svc.load();
    svc.create("note 1", ["work"]);
    svc.create("note 2", ["personal"]);
    const all = svc.list();
    expect(all).toHaveLength(2);
  });

  it("filters by tag", async () => {
    const svc = new NoteService();
    await svc.load();
    svc.create("note 1", ["work"]);
    svc.create("note 2", ["personal"]);
    svc.create("note 3", ["work"]);
    const workNotes = svc.list("work");
    expect(workNotes).toHaveLength(2);
  });
});

describe("NoteService.get", () => {
  it("returns note by id", async () => {
    const svc = new NoteService();
    await svc.load();
    const created = svc.create("find me");
    const found = svc.get(created.id);
    expect(found.text).toBe("find me");
  });

  it("throws for non-existent id", async () => {
    const svc = new NoteService();
    await svc.load();
    expect(() => svc.get(999)).toThrow("Note not found");
  });
});

describe("NoteService.update", () => {
  it("updates text only", async () => {
    const svc = new NoteService();
    await svc.load();
    const note = svc.create("original", ["tag1"]);
    const updated = svc.update(note.id, { text: "updated text" });
    expect(updated.text).toBe("updated text");
    expect(updated.tags).toEqual(["tag1"]);
    expect(updated.updatedAt >= note.createdAt).toBe(true);
  });

  it("updates tags only", async () => {
    const svc = new NoteService();
    await svc.load();
    const note = svc.create("original", ["tag1"]);
    const updated = svc.update(note.id, { tags: ["newtag"] });
    expect(updated.text).toBe("original");
    expect(updated.tags).toEqual(["newtag"]);
  });

  it("throws for non-existent id", async () => {
    const svc = new NoteService();
    await svc.load();
    expect(() => svc.update(999, { text: "x" })).toThrow("Note not found");
  });
});

describe("NoteService.delete", () => {
  it("deletes a note", async () => {
    const svc = new NoteService();
    await svc.load();
    const note = svc.create("delete me");
    const deleted = svc.delete(note.id);
    expect(deleted.id).toBe(note.id);
    expect(svc.notes).toHaveLength(0);
  });

  it("throws for non-existent id", async () => {
    const svc = new NoteService();
    await svc.load();
    expect(() => svc.delete(999)).toThrow("Note not found");
  });
});

describe("NoteService.search", () => {
  it("finds notes matching regex", async () => {
    const svc = new NoteService();
    await svc.load();
    svc.create("# Meeting notes\n\nDiscuss project");
    svc.create("Shopping list");
    svc.create("Project plan");
    const results = svc.search("project");
    expect(results).toHaveLength(2);
  });

  it("is case-insensitive", async () => {
    const svc = new NoteService();
    await svc.load();
    svc.create("Hello World");
    const results = svc.search("hello");
    expect(results).toHaveLength(1);
  });

  it("handles invalid regex gracefully", async () => {
    const svc = new NoteService();
    await svc.load();
    svc.create("test [bracket]");
    const results = svc.search("[bracket");
    expect(results).toHaveLength(1);
  });
});

describe("NoteService.allTags", () => {
  it("returns tag counts sorted by frequency", async () => {
    const svc = new NoteService();
    await svc.load();
    svc.create("n1", ["work", "idea"]);
    svc.create("n2", ["work"]);
    svc.create("n3", ["personal"]);
    const tags = svc.allTags();
    expect(tags).toEqual([
      { tag: "work", count: 2 },
      { tag: "idea", count: 1 },
      { tag: "personal", count: 1 },
    ]);
  });
});

describe("NoteService.save + load roundtrip", () => {
  it("persists and reloads notes", async () => {
    const svc1 = new NoteService();
    await svc1.load();
    svc1.create("persist me", ["tag1"]);
    await svc1.save();

    const svc2 = new NoteService();
    await svc2.load();
    expect(svc2.notes).toHaveLength(1);
    expect(svc2.notes[0].text).toBe("persist me");
    expect(svc2.notes[0].tags).toEqual(["tag1"]);
  });
});
