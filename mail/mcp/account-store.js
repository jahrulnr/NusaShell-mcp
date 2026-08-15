import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { AccountSchema, publicAccount, resolveAccount } from "./config.js";

const STORE_DIR_NAME = "plugins-data/nusashell.mail";
const STORE_FILE_NAME = "accounts.dat";
const MAX_STORE_BYTES = 256 * 1024;

function resolveStoreDir(environment = process.env) {
  const base = environment.NUSASHELL_USER_DATA
    || environment.NUSASHELL_DATA_DIR
    || path.join(os.homedir(), ".local", "share", "nusashell");
  return path.join(base, STORE_DIR_NAME);
}

function resolveStorePath(environment = process.env) {
  return path.join(resolveStoreDir(environment), STORE_FILE_NAME);
}

function encode(data) {
  return Buffer.from(JSON.stringify(data), "utf8").toString("base64");
}

function decode(raw) {
  return JSON.parse(Buffer.from(raw, "base64").toString("utf8"));
}

let cache = null;
let cachePath = null;

function readRaw(storePath) {
  if (cache && cachePath === storePath) return cache;
  if (!fs.existsSync(storePath)) {
    cache = [];
    cachePath = storePath;
    return cache;
  }
  const raw = fs.readFileSync(storePath, "utf8").trim();
  if (!raw) {
    cache = [];
    cachePath = storePath;
    return cache;
  }
  if (Buffer.byteLength(raw, "utf8") > MAX_STORE_BYTES) {
    throw new Error("Mail account store exceeds the 256 KiB limit");
  }
  let parsed;
  try {
    parsed = decode(raw);
  } catch {
    throw new Error("Mail account store is not valid");
  }
  if (!Array.isArray(parsed)) {
    throw new Error("Mail account store must contain an account array");
  }
  const accounts = parsed.map((value, index) => {
    try {
      return AccountSchema.parse(value);
    } catch (error) {
      throw new Error(`Mail account record ${index + 1} is invalid`, { cause: error });
    }
  });
  const seen = new Set();
  for (const account of accounts) {
    if (seen.has(account.id)) {
      throw new Error(`Duplicate account id: ${account.id}`);
    }
    seen.add(account.id);
  }
  cache = accounts;
  cachePath = storePath;
  return accounts;
}

function writeRaw(storePath, accounts) {
  const dir = path.dirname(storePath);
  fs.mkdirSync(dir, { recursive: true });
  const tmp = `${storePath}.tmp`;
  fs.writeFileSync(tmp, encode(accounts), { mode: 0o600 });
  fs.renameSync(tmp, storePath);
  cache = accounts;
  cachePath = storePath;
}

export function loadAccounts(environment = process.env) {
  return readRaw(resolveStorePath(environment));
}

export function listPublicAccounts(environment = process.env) {
  return loadAccounts(environment).map(publicAccount);
}

export function getPublicAccount(accountId, environment = process.env) {
  return publicAccount(resolveAccount(loadAccounts(environment), accountId));
}

export function saveAccount(input, environment = process.env) {
  const storePath = resolveStorePath(environment);
  const accounts = [...readRaw(storePath)];
  const parsed = AccountSchema.parse(input);
  const existing = accounts.find((account) => account.id === parsed.id);
  const next = existing
    ? accounts.map((account) => (account.id === parsed.id ? parsed : account))
    : [...accounts, parsed];
  writeRaw(storePath, next);
  return publicAccount(parsed);
}

export function deleteAccount(accountId, environment = process.env) {
  const storePath = resolveStorePath(environment);
  const accounts = readRaw(storePath);
  const next = accounts.filter((account) => account.id !== accountId);
  if (next.length === accounts.length) {
    throw new Error(`Mail account not found: ${accountId}`);
  }
  writeRaw(storePath, next);
  return next.map(publicAccount);
}

export function importAccounts(accounts, environment = process.env) {
  const storePath = resolveStorePath(environment);
  const validated = accounts.map((account) => AccountSchema.parse(account));
  const seen = new Set();
  for (const account of validated) {
    if (seen.has(account.id)) {
      throw new Error(`Duplicate account id: ${account.id}`);
    }
    seen.add(account.id);
  }
  writeRaw(storePath, validated);
  return validated.map(publicAccount);
}

export function resetCache() {
  cache = null;
  cachePath = null;
}
