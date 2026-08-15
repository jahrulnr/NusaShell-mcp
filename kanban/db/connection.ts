import fs from "fs";
import path from "path";
import os from "os";
import type { Project, Column, Session, Ticket, Attachment, Dependency } from "../shared/types.js";

export interface KanbanStore {
  projects: Project[];
  columns: Column[];
  sessions: Session[];
  tickets: Ticket[];
  attachments: Attachment[];
  dependencies: Dependency[];
}

const userData = process.env.NUSASHELL_USER_DATA || process.env.NUSASHELL_DATA_DIR || path.join(os.homedir(), ".local", "share", "nusashell");
const KANBAN_DIR = path.join(userData, "plugins-data", "nusashell.kanban");
const DB_PATH = path.join(KANBAN_DIR, "kanban.json");

let cache: KanbanStore | null = null;

function emptyStore(): KanbanStore {
  return { projects: [], columns: [], sessions: [], tickets: [], attachments: [], dependencies: [] };
}

export function getStore(): KanbanStore {
  if (cache) return cache;

  fs.mkdirSync(KANBAN_DIR, { recursive: true });

  if (fs.existsSync(DB_PATH)) {
    try {
      const raw = fs.readFileSync(DB_PATH, "utf-8");
      const parsed = JSON.parse(raw) as Partial<KanbanStore>;
      cache = {
        projects: parsed.projects ?? [],
        columns: parsed.columns ?? [],
        sessions: parsed.sessions ?? [],
        tickets: parsed.tickets ?? [],
        attachments: parsed.attachments ?? [],
        dependencies: parsed.dependencies ?? [],
      };
    } catch {
      cache = emptyStore();
    }
  } else {
    cache = emptyStore();
  }

  return cache;
}

export function saveStore(): void {
  if (!cache) return;
  fs.mkdirSync(KANBAN_DIR, { recursive: true });
  fs.writeFileSync(DB_PATH, JSON.stringify(cache, null, 2), "utf-8");
}

export function closeDb(): void {
  cache = null;
}
