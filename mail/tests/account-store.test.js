import { afterEach, beforeEach, describe, expect, it } from "vitest";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import {
  deleteAccount,
  loadAccounts,
  resetCache,
  saveAccount,
} from "../mcp/account-store.js";

const account = {
  id: "work",
  name: "Work",
  email: "me@example.com",
  username: "me@example.com",
  password: "app-password",
  enabled: true,
  imap: { host: "imap.example.com", port: 993, secure: true },
  smtp: { host: "smtp.example.com", port: 465, secure: true },
};

let tempDir;

beforeEach(() => {
  tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "mail-store-"));
  resetCache();
});

afterEach(() => {
  fs.rmSync(tempDir, { recursive: true, force: true });
  resetCache();
});

describe("mail account store", () => {
  it("returns an empty array when no store file exists", () => {
    expect(loadAccounts({ NUSASHELL_USER_DATA: tempDir })).toEqual([]);
  });

  it("saves and loads accounts from the encoded store file", () => {
    const saved = saveAccount(account, { NUSASHELL_USER_DATA: tempDir });
    expect(saved.id).toBe("work");

    resetCache();
    const loaded = loadAccounts({ NUSASHELL_USER_DATA: tempDir });
    expect(loaded).toHaveLength(1);
    expect(loaded[0].id).toBe("work");
    expect(loaded[0].password).toBe("app-password");
  });

  it("store file is not human-readable (base64 encoded)", () => {
    saveAccount(account, { NUSASHELL_USER_DATA: tempDir });
    const storePath = path.join(tempDir, "plugins-data", "nusashell.mail", "accounts.dat");
    const raw = fs.readFileSync(storePath, "utf8");
    expect(raw).not.toContain("app-password");
    expect(raw).not.toContain("me@example.com");
    expect(() => Buffer.from(raw, "base64").toString("utf8")).not.toThrow();
  });

  it("updates an existing account by id", () => {
    saveAccount(account, { NUSASHELL_USER_DATA: tempDir });
    saveAccount({ ...account, name: "Work Updated" }, { NUSASHELL_USER_DATA: tempDir });
    resetCache();
    const loaded = loadAccounts({ NUSASHELL_USER_DATA: tempDir });
    expect(loaded).toHaveLength(1);
    expect(loaded[0].name).toBe("Work Updated");
  });

  it("deletes an account by id", () => {
    saveAccount(account, { NUSASHELL_USER_DATA: tempDir });
    saveAccount({ ...account, id: "personal", email: "personal@example.com" }, { NUSASHELL_USER_DATA: tempDir });

    const remaining = deleteAccount("work", { NUSASHELL_USER_DATA: tempDir });
    expect(remaining).toHaveLength(1);
    expect(remaining[0].id).toBe("personal");

    resetCache();
    const loaded = loadAccounts({ NUSASHELL_USER_DATA: tempDir });
    expect(loaded).toHaveLength(1);
    expect(loaded[0].id).toBe("personal");
  });

  it("throws when deleting a non-existent account", () => {
    expect(() => deleteAccount("missing", { NUSASHELL_USER_DATA: tempDir })).toThrow(/not found/i);
  });

  it("rejects invalid account input", () => {
    expect(() => saveAccount({ ...account, email: "not-an-email" }, { NUSASHELL_USER_DATA: tempDir })).toThrow();
  });
});
