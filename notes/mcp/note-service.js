import path from "node:path";
import fsp from "node:fs/promises";
import { legacyNotesDataFile, notesDataFile } from "./config.js";

const MAX_TEXT_LENGTH = 100 * 1024;
const MAX_TAGS = 20;
const MAX_TAG_LENGTH = 60;

export class NoteService {
  constructor() {
    this.notes = [];
    this.nextId = 1;
  }

  async load() {
    const file = notesDataFile();
    await this.#migrateLegacyIfNeeded(file);
    try {
      const raw = await fsp.readFile(file, "utf8");
      const data = JSON.parse(raw);
      if (Array.isArray(data.notes)) {
        this.notes = data.notes.map((note) => this._migrate(note));
        const maxId = Math.max(0, ...this.notes.map((n) => Number(n.id) || 0));
        this.nextId = maxId + 1;
      }
    } catch (error) {
      if (error.code !== "ENOENT") {
        process.stderr.write(`[notes-mcp] failed to load notes: ${error.message}\n`);
      }
    }
  }

  async #migrateLegacyIfNeeded(targetFile) {
    // Explicit test/override paths must stay empty until the service creates them.
    if (process.env.NUSASHELL_NOTES_DATA_FILE) return;
    const legacy = legacyNotesDataFile();
    if (path.resolve(legacy) === path.resolve(targetFile)) return;
    try {
      await fsp.access(targetFile);
      return;
    } catch {
      // Target missing — try one-time copy from plugin-adjacent legacy file.
    }
    try {
      const raw = await fsp.readFile(legacy, "utf8");
      await fsp.mkdir(path.dirname(targetFile), { recursive: true });
      await fsp.writeFile(targetFile, raw, "utf8");
      process.stderr.write(`[notes-mcp] migrated notes from legacy path to ${targetFile}\n`);
    } catch (error) {
      if (error.code !== "ENOENT") {
        process.stderr.write(`[notes-mcp] legacy notes migration skipped: ${error.message}\n`);
      }
    }
  }

  async save() {
    const file = notesDataFile();
    await fsp.mkdir(path.dirname(file), { recursive: true });
    await fsp.writeFile(file, JSON.stringify({ notes: this.notes }, null, 2), "utf8");
  }

  _migrate(note) {
    return {
      id: Number(note.id) || 0,
      text: String(note.text ?? ""),
      tags: Array.isArray(note.tags) ? note.tags.map(String) : [],
      createdAt: note.createdAt || new Date().toISOString(),
      updatedAt: note.updatedAt || note.createdAt || new Date().toISOString(),
    };
  }

  _validateText(text) {
    if (typeof text !== "string") throw new Error("text must be a string");
    if (text.length === 0) throw new Error("text must not be empty");
    if (text.length > MAX_TEXT_LENGTH) {
      throw new Error(`text too long (${text.length} chars, max ${MAX_TEXT_LENGTH})`);
    }
  }

  _validateTags(tags) {
    if (!Array.isArray(tags)) throw new Error("tags must be an array");
    if (tags.length > MAX_TAGS) throw new Error(`too many tags (max ${MAX_TAGS})`);
    for (const tag of tags) {
      if (typeof tag !== "string" || tag.length === 0) {
        throw new Error("each tag must be a non-empty string");
      }
      if (tag.length > MAX_TAG_LENGTH) {
        throw new Error(`tag too long (max ${MAX_TAG_LENGTH} chars): ${tag.slice(0, 20)}...`);
      }
    }
  }

  create(text, tags = []) {
    this._validateText(text);
    this._validateTags(tags);
    const now = new Date().toISOString();
    const note = {
      id: this.nextId++,
      text,
      tags,
      createdAt: now,
      updatedAt: now,
    };
    this.notes.push(note);
    return note;
  }

  list(tag, sort = "updated") {
    let notes = tag ? this.notes.filter((n) => n.tags.includes(tag)) : [...this.notes];
    const sortKey = sort === "created" ? "createdAt" : "updatedAt";
    notes.sort((a, b) => {
      const aVal = a[sortKey] || a.createdAt;
      const bVal = b[sortKey] || b.createdAt;
      return new Date(bVal).getTime() - new Date(aVal).getTime();
    });
    return notes;
  }

  get(id) {
    const note = this.notes.find((n) => n.id === Number(id));
    if (!note) throw new Error(`Note not found: ${id}`);
    return note;
  }

  update(id, updates) {
    const note = this.get(id);
    if (updates.text !== undefined) {
      this._validateText(updates.text);
      note.text = updates.text;
    }
    if (updates.tags !== undefined) {
      this._validateTags(updates.tags);
      note.tags = updates.tags;
    }
    note.updatedAt = new Date().toISOString();
    return note;
  }

  delete(id) {
    const idx = this.notes.findIndex((n) => n.id === Number(id));
    if (idx === -1) throw new Error(`Note not found: ${id}`);
    const [deleted] = this.notes.splice(idx, 1);
    return deleted;
  }

  search(query) {
    let regex;
    try {
      regex = new RegExp(query, "i");
    } catch {
      regex = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "i");
    }
    return this.notes.filter((n) => regex.test(n.text));
  }

  allTags() {
    const counts = {};
    for (const note of this.notes) {
      for (const tag of note.tags) {
        counts[tag] = (counts[tag] || 0) + 1;
      }
    }
    return Object.entries(counts)
      .map(([tag, count]) => ({ tag, count }))
      .sort((a, b) => b.count - a.count);
  }
}
