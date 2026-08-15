import { z } from "zod";
import { NOTES_TOOL_NAMES } from "./tool-catalog.js";

const noteText = z.string().trim().min(1).max(100 * 1024);
const tags = z.array(z.string().trim().min(1).max(60)).max(20).default([]);
const noteId = z.number().int().positive();
const searchQuery = z.string().trim().min(1).max(500);
const tagFilter = z.string().trim().min(1).max(60).optional();
const sortOption = z.enum(["updated", "created"]).default("updated");

const schemas = {
  create: z.object({ text: noteText, tags }).strict(),
  list: z.object({ tag: tagFilter, sort: sortOption }).strict(),
  get: z.object({ id: noteId }).strict(),
  update: z.object({
    id: noteId,
    text: noteText.optional(),
    tags: z.array(z.string().trim().min(1).max(60)).max(20).optional(),
  }).strict(),
  delete: z.object({ id: noteId }).strict(),
  search: z.object({ query: searchQuery }).strict(),
};

export const NOTES_TOOLS = Object.freeze([
  descriptor(
    "create",
    "Create a new note with optional tags. Text supports markdown.",
    {
      text: { type: "string", description: "Note content (markdown supported, max 100 KB)." },
      tags: { type: "array", items: { type: "string" }, description: "Optional tags (max 20, each max 60 chars).", default: [] },
    },
    ["text"],
    false,
  ),
  descriptor(
    "list",
    "List all notes, optionally filtered by tag. Results are sorted by updatedAt (default) or createdAt.",
    {
      tag: { type: "string", description: "Filter notes by tag name. Omit to list all." },
      sort: { type: "string", enum: ["updated", "created"], description: "Sort order: 'updated' (default) or 'created'.", default: "updated" },
    },
    [],
  ),
  descriptor(
    "get",
    "Get a single note by its ID.",
    {
      id: { type: "integer", description: "Note ID (positive integer).", minimum: 1 },
    },
    ["id"],
  ),
  descriptor(
    "update",
    "Update a note's text and/or tags. Only provided fields are changed.",
    {
      id: { type: "integer", description: "Note ID to update.", minimum: 1 },
      text: { type: "string", description: "New note text (markdown supported, max 100 KB). Omit to keep existing." },
      tags: { type: "array", items: { type: "string" }, description: "New tags array (replaces existing). Omit to keep existing." },
    },
    ["id"],
    false,
  ),
  descriptor(
    "delete",
    "Delete a note by its ID. This is permanent and cannot be undone.",
    {
      id: { type: "integer", description: "Note ID to delete.", minimum: 1 },
    },
    ["id"],
    false,
  ),
  descriptor(
    "search",
    "Search notes by text content using a regex pattern (case-insensitive).",
    {
      query: { type: "string", description: "Regex pattern to match against note text (case-insensitive)." },
    },
    ["query"],
  ),
]);

if (NOTES_TOOLS.map((t) => t.name).join(",") !== NOTES_TOOL_NAMES.join(",")) {
  throw new Error("Notes tool descriptors are out of sync with the canonical catalog");
}

export async function callNotesTool(service, name, rawArguments = {}, { persist = true } = {}) {
  const schema = schemas[name];
  if (!schema) throw new Error(`Unknown notes tool: ${name}`);
  const input = schema.parse(rawArguments ?? {});

  switch (name) {
    case "create": {
      const note = service.create(input.text, input.tags);
      if (persist) await service.save();
      return { note, totalNotes: service.notes.length };
    }
    case "list": {
      const notes = service.list(input.tag, input.sort);
      return { notes, total: notes.length, ...(input.tag ? { tag: input.tag } : {}), sort: input.sort };
    }
    case "get": {
      const note = service.get(input.id);
      return { note };
    }
    case "update": {
      const note = service.update(input.id, {
        ...(input.text !== undefined ? { text: input.text } : {}),
        ...(input.tags !== undefined ? { tags: input.tags } : {}),
      });
      if (persist) await service.save();
      return { note };
    }
    case "delete": {
      const deleted = service.delete(input.id);
      if (persist) await service.save();
      return { deleted, totalNotes: service.notes.length };
    }
    case "search": {
      const results = service.search(input.query);
      return { results, total: results.length, query: input.query };
    }
    default:
      throw new Error(`Unknown notes tool: ${name}`);
  }
}

function descriptor(name, description, properties, required = [], readOnly = true) {
  return {
    name,
    description,
    annotations: {
      title: name,
      readOnlyHint: readOnly,
      destructiveHint: name === "delete",
      idempotentHint: readOnly || name === "update",
      openWorldHint: false,
    },
    inputSchema: {
      type: "object",
      properties,
      required,
      additionalProperties: false,
    },
  };
}
