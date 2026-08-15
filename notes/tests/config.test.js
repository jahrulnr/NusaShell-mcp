import { afterEach, describe, expect, it } from "vitest";
import path from "node:path";
import { notesDataFile, legacyNotesDataFile } from "../mcp/config.js";

const KEYS = ["NUSASHELL_NOTES_DATA_FILE", "NUSASHELL_USER_DATA", "NUSASHELL_DATA_DIR"];
const saved = {};

afterEach(() => {
  for (const key of KEYS) {
    if (saved[key] === undefined) delete process.env[key];
    else process.env[key] = saved[key];
    delete saved[key];
  }
});

function setEnv(key, value) {
  saved[key] = process.env[key];
  if (value === undefined) delete process.env[key];
  else process.env[key] = value;
}

describe("notesDataFile", () => {
  it("prefer NUSASHELL_NOTES_DATA_FILE over user data and plugin path", () => {
    setEnv("NUSASHELL_NOTES_DATA_FILE", "/tmp/override-notes.json");
    setEnv("NUSASHELL_USER_DATA", "/home/user/.config/nusashell");
    expect(notesDataFile()).toBe(path.resolve("/tmp/override-notes.json"));
  });

  it("stores notes under userData plugins-data, never the install plugin tree", () => {
    setEnv("NUSASHELL_NOTES_DATA_FILE", undefined);
    setEnv("NUSASHELL_USER_DATA", "/home/user/.config/nusashell");
    setEnv("NUSASHELL_DATA_DIR", undefined);
    expect(notesDataFile()).toBe(
      path.resolve("/home/user/.config/nusashell", "plugins-data", "nusashell.notes", "notes.json"),
    );
  });

  it("falls back to plugin-adjacent notes.json without user data env", () => {
    setEnv("NUSASHELL_NOTES_DATA_FILE", undefined);
    setEnv("NUSASHELL_USER_DATA", undefined);
    setEnv("NUSASHELL_DATA_DIR", undefined);
    const file = notesDataFile();
    expect(file).toBe(legacyNotesDataFile());
    expect(path.basename(file)).toBe("notes.json");
    expect(file).toContain(`${path.sep}notes${path.sep}`);
  });
});
