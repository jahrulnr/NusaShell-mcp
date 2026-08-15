import path from "node:path";
import { fileURLToPath } from "node:url";

let _dirname;
try {
  const _filename = fileURLToPath(import.meta.url);
  _dirname = path.dirname(_filename);
} catch {
  _dirname = typeof __dirname !== "undefined" ? __dirname : process.cwd();
}

/**
 * Resolve the durable Notes JSON path.
 *
 * Precedence:
 * 1. NUSASHELL_NOTES_DATA_FILE — tests and explicit overrides
 * 2. NUSASHELL_USER_DATA/plugins-data/nusashell.notes/notes.json — desktop shell
 *    (keeps user notes out of the install/plugin bundle so `make install` cannot
 *    overwrite production data with packaged or local-dev runtime state)
 * 3. Plugin-adjacent notes.json — standalone MCP / legacy fallback only
 */
export function notesDataFile() {
  const envFile = process.env.NUSASHELL_NOTES_DATA_FILE;
  if (envFile) return path.resolve(envFile);

  const userData = process.env.NUSASHELL_USER_DATA || process.env.NUSASHELL_DATA_DIR;
  if (userData) {
    return path.join(path.resolve(userData), "plugins-data", "nusashell.notes", "notes.json");
  }

  return path.join(_dirname, "..", "notes.json");
}

/** Legacy plugin-adjacent file (pre-userData isolation). Used for one-time migrate. */
export function legacyNotesDataFile() {
  return path.join(_dirname, "..", "notes.json");
}
