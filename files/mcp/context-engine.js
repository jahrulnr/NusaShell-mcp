import fs from "node:fs/promises";
import path from "node:path";
import { resolvePath, validateRoot, toPosixPath, relativePosix, splitLines } from "./config.js";

/**
 * Workspace Context Engine - a deterministic, LLM-free repo map builder.
 *
 * Port of the design in "Riset Workspace Context Indexing" (Aider-style repo
 * map) from the Python reference implementation:
 *   1. Stack classification & manifest detection
 *   2. Directory walking with .gitignore/.ignore matching
 *   3. Regex fallback-lexer symbol extraction (definitions + references)
 *   4. Directed dependency graph + Personalized PageRank
 *   5. Scope-aware elision + token budget fitting (binary search)
 *   6. In-memory tag cache with (path, mtime, size) invalidation
 *
 * The persistent SQLite cache from the research doc becomes an in-memory Map:
 * the stdio plugin process is long-lived, and the node20 bundle target has no
 * dependency-free SQLite. Disk persistence is deferred.
 */

/** Maximum file size considered for symbol extraction. */
const MAX_EXTRACT_BYTES = 1024 * 1024;
/** Maximum definition signatures rendered per file in the repo map. */
const MAX_DEFS_PER_FILE = 30;
/** Absolute cap on files scanned in one pipeline run. */
const MAX_SCAN_FILES = 20000;
const MAX_WORKSPACE_INSTRUCTIONS_BYTES = 50 * 1024;
export const WORKSPACE_INSTRUCTIONS_URI = "nusashell://workspace/AGENTS.md";
/** Personalization multipliers (research doc: active 50x, mentioned 10x). */
const ACTIVE_FILE_BOOST = 50;
const QUERY_MATCH_BOOST = 10;
/** Role-match boost folded into Personalized PageRank personalization (like query 10x). */
const ROLE_MATCH_BOOST = 8;
/** Recency half-life for role-aware scores (matches experimental rcr_router.py). */
const RECENCY_HALF_LIFE_MS = 30 * 86400 * 1000;

/**
 * Descending rank comparator with a deterministic path tie-break, so files
 * with exactly equal scores keep a stable order across calls (Map insertion
 * order alone is not a reliable tie-break for byte-identical map output).
 * @param {[string, number]} a
 * @param {[string, number]} b
 */
function compareRankedDesc(a, b) {
  return b[1] - a[1] || a[0].localeCompare(b[0]);
}

/**
 * Role-specific token budget: B(role) = floor(base * multiplier + offset).
 * Absent/unknown role leaves the caller's budget unchanged (legacy).
 * Planner gets more room for docs; executor is near-base for code; reviewer mid for tests/conventions.
 */
export const ROLE_BUDGET_PARAMS = Object.freeze({
  planner: { multiplier: 1.2, offset: 256 },
  executor: { multiplier: 1.05, offset: 64 },
  reviewer: { multiplier: 1.12, offset: 128 },
});

export const CONTEXT_MAP_ROLES = Object.freeze(Object.keys(ROLE_BUDGET_PARAMS));

/**
 * @param {number} baseBudget caller token budget (unchanged semantics)
 * @param {string} [role] planner | executor | reviewer
 * @returns {number}
 */
export function allocateRoleBudget(baseBudget, role) {
  const params = role ? ROLE_BUDGET_PARAMS[role] : null;
  if (!params) return baseBudget;
  return Math.max(1, Math.floor(baseBudget * params.multiplier + params.offset));
}

/**
 * Exponential recency decay: 1 at now, ~0.5 at one half-life, etc.
 * @param {number} mtimeMs file mtime
 * @param {number} [nowMs]
 */
export function recencyDecay(mtimeMs, nowMs = Date.now()) {
  if (!Number.isFinite(mtimeMs) || mtimeMs <= 0) return 1;
  const age = Math.max(0, nowMs - mtimeMs);
  return Math.exp((-Math.LN2 * age) / RECENCY_HALF_LIFE_MS);
}

/**
 * Deterministic role × path affinity used as a PageRank personalization multiplier.
 * planner → docs / markdown / AGENTS / RULES; executor → non-test source; reviewer → tests + conventions.
 * @param {string} rel workspace-relative posix path
 * @param {string} [role]
 * @returns {number} >= 1
 */
export function roleMatchMultiplier(rel, role) {
  if (!role || !ROLE_BUDGET_PARAMS[role]) return 1;
  const p = toPosixPath(rel);
  const base = p.includes("/") ? p.slice(p.lastIndexOf("/") + 1) : p;
  const ext = (() => {
    const i = base.lastIndexOf(".");
    return i >= 0 ? base.slice(i).toLowerCase() : "";
  })();
  const isTest =
    /(?:^|\/)(?:tests?|__tests__)\//i.test(p) ||
    /\.(?:test|spec)\.[^.]+$/i.test(p);
  const isConvention =
    /^AGENTS\.md$/i.test(base) ||
    /^RULES(?:\.[^.]+)?$/i.test(base) ||
    /(?:^|\/)docs\//i.test(p);
  const isDoc = DOC_EXTS.has(ext) || isConvention;
  const isCode = Boolean(SUPPORTED_EXTS[ext]);

  switch (role) {
    case "planner":
      return isDoc || isConvention ? ROLE_MATCH_BOOST : 1;
    case "executor":
      return isCode && !isTest ? ROLE_MATCH_BOOST : 1;
    case "reviewer":
      if (isTest) return ROLE_MATCH_BOOST;
      if (isConvention || /^AGENTS\.md$/i.test(base)) return ROLE_MATCH_BOOST;
      if (isDoc) return Math.max(2, ROLE_MATCH_BOOST / 2);
      return 1;
    default:
      return 1;
  }
}

const MANIFESTS = {
  "package.json": "node",
  "pnpm-workspace.yaml": "node",
  "Cargo.toml": "rust",
  "pyproject.toml": "python",
  "requirements.txt": "python",
  "go.mod": "go",
  "composer.json": "php",
  "pom.xml": "java-maven",
  "build.gradle": "java-gradle",
  "mix.exs": "elixir",
  Gemfile: "ruby",
};

const SUPPORTED_EXTS = {
  ".ts": "typescript",
  ".tsx": "typescript",
  ".js": "javascript",
  ".jsx": "javascript",
  ".mjs": "javascript",
  ".cjs": "javascript",
  ".py": "python",
  ".go": "go",
  ".rs": "rust",
  ".rb": "ruby",
  ".php": "php",
  ".java": "java",
  ".kt": "kotlin",
  ".ex": "elixir",
  ".exs": "elixir",
  ".cs": "csharp",
  ".cpp": "cpp",
  ".cc": "cpp",
  ".cxx": "cpp",
  ".h": "cpp",
  ".hpp": "cpp",
  ".c": "c",
};

const DOC_EXTS = new Set([".md", ".mdx", ".txt", ".rst"]);

const KEY_DEP_PREFIXES = [
  "react", "next", "vue", "nuxt", "svelte", "express", "fastify",
  "electron", "vitest", "jest", "typescript", "zod", "fastapi",
  "django", "flask", "axum", "tokio",
];

const DEFAULT_IGNORE_DIRS = new Set([
  ".git", ".hg", ".svn", "node_modules", "target", "dist", "build",
  ".next", ".nuxt", ".cache", ".turbo", "coverage", ".vitest", "out",
  "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache",
  "vendor", ".venv", "venv", "env", ".idea", ".vscode",
]);

const IGNORE_FILE_NAMES = [".gitignore", ".ignore"];

/**
 * Definition patterns per language. Each entry captures a `name` group and
 * records the definition kind for map rendering.
 * @type {Record<string, Array<{ re: RegExp, kind: string }>>}
 */
const DEF_PATTERNS = {
  typescript: [
    { kind: "type", re: /^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(?:class|interface|type|enum)\s+(?<name>[A-Za-z_$][\w$]*)/ },
    { kind: "function", re: /^\s*(?:export\s+)?(?:async\s+)?function\s+(?<name>[A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*\(/ },
    { kind: "function", re: /^\s*(?:export\s+)?(?:async\s+)?(?<name>[A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*\([^)]*\)\s*[:{]/ },
    { kind: "const", re: /^\s*(?:export\s+)?(?:const|let|var)\s+(?<name>[A-Za-z_$][\w$]*)\s*=/ },
  ],
  javascript: [
    { kind: "type", re: /^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+(?<name>[A-Za-z_$][\w$]*)/ },
    { kind: "function", re: /^\s*(?:export\s+)?(?:async\s+)?function\s+(?<name>[A-Za-z_$][\w$]*)\s*\(/ },
    { kind: "const", re: /^\s*(?:export\s+)?(?:const|let|var)\s+(?<name>[A-Za-z_$][\w$]*)\s*=/ },
  ],
  python: [
    { kind: "type", re: /^\s*class\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "function", re: /^\s*(?:async\s+)?def\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "const", re: /^\s*(?<name>[A-Z_][A-Z0-9_]*)\s*=/ },
  ],
  go: [
    { kind: "function", re: /^\s*func\s+(?:\([^)]*\)\s+)?(?<name>[A-Za-z_]\w*)\s*\(/ },
    { kind: "type", re: /^\s*type\s+(?<name>[A-Za-z_]\w*)\s+/ },
    { kind: "const", re: /^\s*const\s+(?<name>[A-Za-z_]\w*)\s*=/ },
  ],
  rust: [
    { kind: "function", re: /^\s*(?:pub\s+)?(?:async\s+)?fn\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "type", re: /^\s*(?:pub\s+)?(?:struct|enum|trait)\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "const", re: /^\s*(?:pub\s+)?const\s+(?<name>[A-Za-z_]\w*)\s*:/ },
  ],
  php: [
    { kind: "type", re: /^\s*(?:final\s+|abstract\s+)?class\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "function", re: /^\s*function\s+(?<name>[A-Za-z_]\w*)\s*\(/ },
  ],
  ruby: [
    { kind: "type", re: /^\s*(?:class|module)\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "function", re: /^\s*def\s+(?<name>[A-Za-z_][\w]*)/ },
  ],
  java: [
    { kind: "type", re: /^\s*(?:public|private|protected)?\s*(?:abstract\s+|final\s+)*class\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "function", re: /^\s*(?:public|private|protected)\s+(?:static\s+)?(?:[\w<>[\]]+\s+)+(?<name>[A-Za-z_]\w*)\s*\(/ },
  ],
  csharp: [
    { kind: "type", re: /^\s*(?:public|private|protected|internal)?\s*(?:class|interface|struct|enum)\s+(?<name>[A-Za-z_]\w*)/ },
  ],
  cpp: [
    { kind: "type", re: /^\s*(?:class|struct|enum\s+class|enum)\s+(?<name>[A-Za-z_]\w*)/ },
  ],
  c: [
    { kind: "type", re: /^\s*(?:struct|enum|union)\s+(?<name>[A-Za-z_]\w*)/ },
  ],
  elixir: [
    { kind: "function", re: /^\s*defp?\s+(?<name>[a-z_]\w*)/ },
    { kind: "type", re: /^\s*defmodule\s+(?<name>[A-Za-z_.\w]*)/ },
  ],
  kotlin: [
    { kind: "function", re: /^\s*fun\s+(?<name>[A-Za-z_]\w*)/ },
    { kind: "type", re: /^\s*(?:data\s+)?(?:class|object)\s+(?<name>[A-Za-z_]\w*)/ },
  ],
};

/** Fallback-lexer identifier pattern for reference capture. */
const IDENT_RE = /\b[A-Za-z_$][A-Za-z0-9_$]*\b/g;

const KEYWORDS = new Set([
  "if", "else", "for", "while", "return", "break", "continue", "switch",
  "case", "default", "try", "catch", "finally", "throw", "new", "class",
  "interface", "type", "enum", "function", "def", "func", "fn", "const",
  "let", "var", "import", "from", "export", "async", "await",
  "public", "private", "protected", "static", "abstract", "final", "this",
  "self", "super", "true", "false", "null", "none", "undefined", "void",
  "int", "string", "bool", "boolean", "number", "any", "unknown", "never",
  "struct", "union", "module", "package", "namespace", "use", "pub", "mut",
  "impl", "trait", "match", "when", "do", "end", "then", "elif", "and",
  "or", "not", "is", "in", "as", "with", "yield", "lambda", "fun",
  "data", "object", "val", "by", "companion",
]);

/** Rough token estimate: ~4 chars per token (clamped to >= 1). */
export function estimateTokens(text) {
  return Math.max(1, Math.floor(text.length / 4));
}

/**
 * Personalized PageRank via power iteration.
 * @param {{ nodes: string[], outEdges: Map<string, Set<string>> }} graph
 * @param {Record<string, number>} [personalization] file -> multiplier
 */
export function personalizedPagerank(graph, personalization = {}, damping = 0.85, maxIter = 100, tol = 1.0e-6) {
  const nodes = [...graph.nodes];
  const n = nodes.length;
  if (n === 0) return {};
  const idx = new Map(nodes.map((f, i) => [f, i]));

  let p = nodes.map((f) => personalization[f] ?? 1);
  const psum = p.reduce((acc, v) => acc + v, 0);
  p = psum > 0 ? p.map((v) => v / psum) : nodes.map(() => 1 / n);

  let rank = nodes.map(() => 1 / n);
  const outDeg = nodes.map((f) => graph.outEdges.get(f)?.size ?? 0);

  for (let iter = 0; iter < maxIter; iter += 1) {
    const next = p.map((pi) => (1 - damping) * pi);
    let dangling = 0;
    for (let i = 0; i < n; i += 1) {
      if (outDeg[i] === 0) dangling += rank[i];
    }
    if (dangling > 0) {
      for (let i = 0; i < n; i += 1) next[i] += damping * dangling * p[i];
    }
    for (let i = 0; i < n; i += 1) {
      const deg = outDeg[i];
      if (deg === 0) continue;
      const share = (damping * rank[i]) / deg;
      for (const target of graph.outEdges.get(nodes[i])) {
        next[idx.get(target)] += share;
      }
    }
    let delta = 0;
    for (let i = 0; i < n; i += 1) delta += Math.abs(next[i] - rank[i]);
    rank = next;
    if (delta < tol) break;
  }
  return Object.fromEntries(nodes.map((f, i) => [f, rank[i]]));
}

async function pathExists(p) {
  try {
    await fs.access(p);
    return true;
  } catch {
    return false;
  }
}

function toPosix(rel) {
  return toPosixPath(rel);
}

function escapeRegExp(text) {
  return text.replace(/[.+^${}()|[\]\\]/g, "\\$&");
}

/** Minimal gitignore-style matcher over simple dir/file globs (posix rel paths). */
function matchesIgnore(relPosix, patterns) {
  const parts = relPosix.split("/");
  const name = parts[parts.length - 1];
  for (const raw of patterns) {
    let pat = raw;
    if (pat.endsWith("/")) pat = pat.slice(0, -1);
    if (pat.startsWith("/")) {
      pat = pat.slice(1);
      if (relPosix === pat || relPosix.startsWith(`${pat}/`)) return true;
      continue;
    }
    if (pat.includes("*")) {
      const re = new RegExp(`^${pat.split("*").map(escapeRegExp).join(".*")}$`);
      if (re.test(name) || re.test(relPosix)) return true;
    } else if (name === pat || parts.includes(pat)) {
      return true;
    }
  }
  return false;
}

async function readIgnorePatterns(dir) {
  const patterns = [];
  for (const name of IGNORE_FILE_NAMES) {
    let text;
    try {
      text = await fs.readFile(path.join(dir, name), "utf8");
    } catch {
      continue;
    }
    for (const line of text.split(/\r?\n/)) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      patterns.push(trimmed);
    }
  }
  return patterns;
}

/**
 * ContextEngine builds token-budgeted workspace repo maps for one root.
 * Shares root semantics with FileService; `setRoot` clears the tag cache.
 */
export class ContextEngine {
  /** @param {string} root absolute workspace root */
  constructor(root) {
    this.root = path.resolve(root);
    /** rel path -> { mtimeMs, size, lang, defs, refs } */
    this.cache = new Map();
  }

  /** @param {string} newRoot */
  async setRoot(newRoot) {
    this.root = await validateRoot(newRoot);
    this.cache.clear();
  }

  /**
   * Read only the workspace-root AGENTS.md as MCP resource context.
   * Nested instruction files are intentionally excluded: without a target
   * path they would mix unrelated package rules into one workspace context.
   * @returns {Promise<{uri: string, name: string, description: string, mimeType: string, text: string}|null>}
   */
  async readWorkspaceInstructions() {
    const filePath = path.join(this.root, "AGENTS.md");
    let stat;
    try {
      stat = await fs.stat(filePath);
    } catch (error) {
      if (error?.code === "ENOENT") return null;
      throw error;
    }
    if (!stat.isFile()) return null;

    const raw = await fs.readFile(filePath, "utf8");
    const clipped = Buffer.byteLength(raw, "utf8") > MAX_WORKSPACE_INSTRUCTIONS_BYTES
      ? `${Buffer.from(raw, "utf8").subarray(0, MAX_WORKSPACE_INSTRUCTIONS_BYTES).toString("utf8")}\n\n[AGENTS.md truncated by the Files MCP resource limit.]`
      : raw;
    return {
      uri: WORKSPACE_INSTRUCTIONS_URI,
      name: "Workspace instructions",
      description: "Workspace-root AGENTS.md project guidance.",
      mimeType: "text/markdown",
      text: clipped,
    };
  }

  /**
   * Phase 1: classify the workspace from manifests at root + one level deep.
   * @param {string} [subPath] directory relative to the root (default: root)
   */
  async detectStack(subPath = "") {
    const base = resolvePath(this.root, subPath);
    const info = {
      category: "documentation",
      languages: new Set(),
      manifests: {},
      projectName: "",
      version: "",
      keyDeps: [],
      scripts: {},
      isMonorepo: false,
    };

    const rootManifests = {};
    const nestedManifests = {};
    for (const [name, kind] of Object.entries(MANIFESTS)) {
      if (await pathExists(path.join(base, name))) rootManifests[name] = kind;
    }
    let children = [];
    try {
      children = await fs.readdir(base, { withFileTypes: true });
    } catch {
      children = [];
    }
    for (const child of children) {
      if (!child.isDirectory() || child.name.startsWith(".")) continue;
      for (const [name, kind] of Object.entries(MANIFESTS)) {
        const rel = `${child.name}/${name}`;
        if (await pathExists(path.join(base, rel))) nestedManifests[rel] = kind;
      }
    }
    info.manifests = { ...rootManifests, ...nestedManifests };
    info.isMonorepo =
      Object.keys(nestedManifests).length > 0 ||
      (await pathExists(path.join(base, "pnpm-workspace.yaml")));

    try {
      const data = JSON.parse(await fs.readFile(path.join(base, "package.json"), "utf8"));
      info.projectName = data.name ?? "";
      info.version = data.version ?? "";
      const allDeps = { ...(data.dependencies ?? {}), ...(data.devDependencies ?? {}) };
      info.keyDeps = Object.keys(allDeps)
        .filter((d) => KEY_DEP_PREFIXES.some((prefix) => d.startsWith(prefix)))
        .sort()
        .slice(0, 20);
      info.scripts = Object.fromEntries(Object.entries(data.scripts ?? {}).slice(0, 10));
    } catch {
      // No readable package.json — leave metadata empty.
    }

    const kindToLang = {
      node: "typescript", rust: "rust", python: "python", go: "go",
      php: "php", elixir: "elixir", ruby: "ruby",
    };
    for (const kind of Object.values(info.manifests)) {
      if (kind.startsWith("java")) info.languages.add("java");
      else if (kindToLang[kind]) info.languages.add(kindToLang[kind]);
    }

    if (Object.keys(rootManifests).length > 0) {
      info.category = info.isMonorepo ? "hybrid" : "coding";
    } else if (Object.keys(nestedManifests).length > 0) {
      info.category = "hybrid";
    }
    return { ...info, languages: [...info.languages].sort() };
  }

  /**
   * Phase 2: walk the tree respecting default ignores + .gitignore/.ignore.
   * @param {string} base absolute directory to walk
   * @param {number} [maxFiles] scan cap
   */
  async walkWorkspace(base, maxFiles = MAX_SCAN_FILES) {
    const files = [];
    const stats = { visitedDirs: 0, consideredFiles: 0, ignoredFiles: 0, codeFiles: 0, docFiles: 0 };
    const rootPatterns = await readIgnorePatterns(base);
    const queue = [{ dir: base, patterns: rootPatterns }];

    while (queue.length > 0 && files.length < maxFiles) {
      const { dir, patterns } = queue.shift();
      stats.visitedDirs += 1;
      let entries;
      try {
        entries = await fs.readdir(dir, { withFileTypes: true });
      } catch {
        continue;
      }
      entries.sort((a, b) => a.name.localeCompare(b.name));
      const localPatterns = patterns.concat(await readIgnorePatterns(dir));

      for (const entry of entries) {
        if (files.length >= maxFiles) break;
        const abs = path.join(dir, entry.name);
        const rel = relativePosix(base, abs);
        if (entry.isDirectory()) {
          if (entry.name.startsWith(".") || DEFAULT_IGNORE_DIRS.has(entry.name)) continue;
          if (matchesIgnore(rel, localPatterns)) continue;
          queue.push({ dir: abs, patterns: localPatterns });
          continue;
        }
        if (!entry.isFile()) continue;
        if (entry.name.startsWith(".") && !IGNORE_FILE_NAMES.includes(entry.name)) continue;
        stats.consideredFiles += 1;
        if (matchesIgnore(rel, localPatterns)) {
          stats.ignoredFiles += 1;
          continue;
        }
        const ext = path.extname(entry.name).toLowerCase();
        if (SUPPORTED_EXTS[ext]) {
          files.push({ abs, rel, lang: SUPPORTED_EXTS[ext], doc: false });
          stats.codeFiles += 1;
        } else if (DOC_EXTS.has(ext)) {
          files.push({ abs, rel, doc: true });
          stats.docFiles += 1;
        }
      }
    }
    return { files, stats };
  }

  /**
   * Phase 3: extract definition and reference tags from source text.
   * @param {string} rel workspace-relative file path (posix)
   * @param {string} text file contents
   * @param {string} lang language key from SUPPORTED_EXTS
   */
  extractFromText(rel, text, lang) {
    const defs = [];
    const refs = [];
    const seenDefs = new Set();
    const patterns = DEF_PATTERNS[lang] ?? [];
    const lines = splitLines(text);

    for (let lineno = 0; lineno < lines.length; lineno += 1) {
      const line = lines[lineno];
      for (const { re, kind } of patterns) {
        const m = re.exec(line);
        if (m?.groups?.name && !seenDefs.has(m.groups.name)) {
          seenDefs.add(m.groups.name);
          let sig = line.trimEnd();
          if (sig.length > 120) sig = `${sig.slice(0, 117)}...`;
          defs.push({ rel, line: lineno + 1, name: m.groups.name, kind, sig });
          break;
        }
      }
      IDENT_RE.lastIndex = 0;
      let m;
      while ((m = IDENT_RE.exec(line)) !== null) {
        const tok = m[0];
        if (tok.length < 2 || KEYWORDS.has(tok) || seenDefs.has(tok)) continue;
        refs.push({ rel, line: lineno + 1, name: tok });
      }
    }
    return { defs, refs };
  }

  /**
   * Phases 3+6: extract symbols for walked files, using the mtime/size cache.
   * Populates the result Maps in `files` order (not async completion order) so
   * downstream stable sorts see a deterministic insertion order — required for
   * context_map determinism when PPR scores tie.
   */
  async extractAll(files, { refresh = false } = {}) {
    const defsByFile = new Map();
    const refsByFile = new Map();
    let cacheHits = 0;
    let cacheMisses = 0;

    const results = await Promise.all(files.map(async (file) => {
      let stat;
      try {
        stat = await fs.stat(file.abs);
      } catch {
        return null;
      }
      if (file.doc) {
        const cached = refresh ? null : this.cache.get(file.rel);
        if (cached && cached.mtimeMs === stat.mtimeMs && cached.size === stat.size) {
          cacheHits += 1;
        } else {
          cacheMisses += 1;
          this.cache.set(file.rel, {
            mtimeMs: stat.mtimeMs,
            size: stat.size,
            lang: "doc",
            defs: [],
            refs: [],
          });
        }
        return { rel: file.rel, defs: [], refs: [] };
      }
      if (stat.size > MAX_EXTRACT_BYTES) return null;

      const cached = refresh ? null : this.cache.get(file.rel);
      if (cached && cached.mtimeMs === stat.mtimeMs && cached.size === stat.size) {
        cacheHits += 1;
        return { rel: file.rel, defs: cached.defs, refs: cached.refs };
      }
      cacheMisses += 1;
      let text;
      try {
        text = await fs.readFile(file.abs, "utf8");
      } catch {
        return null;
      }
      const { defs, refs } = this.extractFromText(file.rel, text, file.lang);
      this.cache.set(file.rel, { mtimeMs: stat.mtimeMs, size: stat.size, lang: file.lang, defs, refs });
      return { rel: file.rel, defs, refs };
    }));

    // Insert in walk order (results keep `files` order because Promise.all
    // preserves array order), so Map iteration order is deterministic.
    for (const result of results) {
      if (!result) continue;
      defsByFile.set(result.rel, result.defs);
      refsByFile.set(result.rel, result.refs);
    }

    return { defsByFile, refsByFile, cacheHits, cacheMisses };
  }

  /**
   * Phase 4: build the directed graph and rank files with Personalized
   * PageRank. Edges point from the referencing file to the defining file
   * (B references a symbol defined in A  =>  B -> A), so rank flows toward
   * foundational files whose symbols are widely used — Aider's repo-map
   * behavior. This intentionally reverses the Python reference's edge
   * direction, which left definers with no inbound rank.
   * @param {Map<string, Array<object>>} defsByFile
   * @param {Map<string, Array<object>>} refsByFile
   * @param {object} [options]
   * @param {string} [options.activeFile]
   * @param {string} [options.query]
   * @param {string} [options.role] planner | executor | reviewer — omit for legacy ranks
   * @param {Map<string, number>} [options.mtimes] rel → mtimeMs for recency decay
   * @param {number} [options.now] clock ms (injectable for tests)
   */
  rankFiles(defsByFile, refsByFile, { activeFile, query, role, mtimes, now } = {}) {
    const symbolToDefiners = new Map();
    const nodes = new Set();
    for (const [rel, defs] of defsByFile) {
      nodes.add(rel);
      for (const def of defs) {
        if (!symbolToDefiners.has(def.name)) symbolToDefiners.set(def.name, new Set());
        symbolToDefiners.get(def.name).add(rel);
      }
    }

    const outEdges = new Map();
    for (const [rel, refs] of refsByFile) {
      nodes.add(rel);
      for (const ref of refs) {
        const definers = symbolToDefiners.get(ref.name);
        if (!definers) continue;
        for (const definer of definers) {
          if (definer === rel) continue;
          if (!outEdges.has(rel)) outEdges.set(rel, new Set());
          outEdges.get(rel).add(definer);
        }
      }
    }

    const useRole = Boolean(role && ROLE_BUDGET_PARAMS[role]);

    /** @type {Record<string, number>} */
    const personalization = {};
    if (activeFile) personalization[toPosix(activeFile)] = ACTIVE_FILE_BOOST;
    const terms = (query ?? "")
      .toLowerCase()
      .split(/[^A-Za-z0-9_$]+/)
      .filter((t) => t.length >= 2);
    if (terms.length > 0) {
      for (const [rel, defs] of defsByFile) {
        if (defs.some((d) => terms.some((t) => d.name.toLowerCase().includes(t)))) {
          personalization[rel] = (personalization[rel] ?? 1) * QUERY_MATCH_BOOST;
        }
      }
    }
    if (useRole) {
      for (const rel of nodes) {
        const mult = roleMatchMultiplier(rel, role);
        if (mult !== 1) {
          personalization[rel] = (personalization[rel] ?? 1) * mult;
        }
      }
    }

    const graph = { nodes: [...nodes], outEdges };
    const scores = personalizedPagerank(graph, personalization);
    const clock = Number.isFinite(now) ? now : Date.now();
    /** @type {Array<{ path: string, score: number, cost: number, roleMatch: number, recency: number }>} */
    const roleScores = [];
    let ranked;
    if (useRole) {
      ranked = Object.entries(scores).map(([rel, baseScore]) => {
        const mtimeMs = mtimes?.get(rel) ?? clock;
        const recency = recencyDecay(mtimeMs, clock);
        const roleMatch = roleMatchMultiplier(rel, role);
        const score = baseScore * recency;
        const defCost = Math.max(
          1,
          (defsByFile.get(rel) ?? []).slice(0, MAX_DEFS_PER_FILE)
            .reduce((acc, d) => acc + estimateTokens(d.sig ?? ""), 0),
        );
        roleScores.push({
          path: rel,
          score: Number(score.toFixed(6)),
          cost: defCost,
          roleMatch,
          recency: Number(recency.toFixed(6)),
        });
        return [rel, score];
      });
      ranked.sort(compareRankedDesc);
      roleScores.sort((a, b) => b.score - a.score || a.path.localeCompare(b.path));
    } else {
      ranked = Object.entries(scores).sort(compareRankedDesc);
    }
    const edgeCount = [...outEdges.values()].reduce((acc, s) => acc + s.size, 0);
    return {
      ranked,
      graphStats: { nodes: nodes.size, edges: edgeCount },
      ...(useRole ? { roleScores } : {}),
    };
  }

  /**
   * Phase 5: render the markdown repo map within the token budget via binary
   * search over the number of ranked files included.
   * @param {Array<[string, number]>} ranked
   * @param {Map<string, Array<object>>} defsByFile
   * @param {number} budget
   * @param {object} [options]
   * @param {string} [options.activeFile]
   */
  buildRepoMap(ranked, defsByFile, budget, { activeFile } = {}) {
    const rendered = ranked.map(([rel, score]) => ({
      rel,
      score,
      sigs: (defsByFile.get(rel) ?? []).slice(0, MAX_DEFS_PER_FILE).map((d) => d.sig),
    }));

    const render = (k) => {
      const lines = ["# Workspace Context Map", ""];
      if (activeFile) lines.push(`_active file: \`${activeFile}\`_`, "");
      let filesShown = 0;
      let symbolsShown = 0;
      for (const item of rendered.slice(0, k)) {
        filesShown += 1;
        lines.push(`## \`${item.rel}\`  (rank ${item.score.toFixed(4)})`);
        if (item.sigs.length === 0) lines.push("_no top-level definitions_");
        for (const sig of item.sigs) {
          symbolsShown += 1;
          lines.push(`- ${sig} ⋮`);
        }
        lines.push("");
      }
      const md = lines.join("\n");
      return { md, filesShown, symbolsShown, tokensUsed: estimateTokens(md) };
    };

    if (rendered.length === 0) {
      return { ...render(0), elidedBodies: 0 };
    }
    let lo = 1;
    let hi = rendered.length;
    let best = render(lo);
    if (best.tokensUsed > budget) {
      return { ...best, elidedBodies: best.symbolsShown };
    }
    while (lo < hi) {
      const mid = Math.floor((lo + hi + 1) / 2);
      const attempt = render(mid);
      if (attempt.tokensUsed <= budget) {
        best = attempt;
        lo = mid;
      } else {
        hi = mid - 1;
      }
    }
    return { ...best, elidedBodies: best.symbolsShown };
  }

  /**
   * Full pipeline (phases 1-6): classify, walk, extract, rank, elide, fit.
   * @param {object} [options]
   * @param {string} [options.path] directory relative to the root
   * @param {number} [options.budget] token budget for the map
   * @param {string} [options.activeFile] file to boost (relative, 50x)
   * @param {string} [options.query] symbol-name terms to boost (10x)
   * @param {string} [options.role] planner | executor | reviewer — role-aware
   *   budget + ranking; omit for legacy byte-identical behavior
   * @param {number} [options.now] injectable clock for recency (tests)
   * @param {number} [options.maxFiles] scan cap
   * @param {boolean} [options.refresh] bypass the tag cache
   */
  async contextMap(options = {}) {
    const {
      path: subPath = "",
      budget = 1024,
      activeFile,
      query,
      role,
      now,
      maxFiles = MAX_SCAN_FILES,
      refresh = false,
    } = options;
    const useRole = Boolean(role && ROLE_BUDGET_PARAMS[role]);
    const effectiveBudget = useRole ? allocateRoleBudget(budget, role) : budget;
    const started = performance.now();
    const base = resolvePath(this.root, subPath);
    await validateRoot(base);

    let t = performance.now();
    const stack = await this.detectStack(subPath);
    const classifyMs = performance.now() - t;

    t = performance.now();
    const { files, stats: walk } = await this.walkWorkspace(base, maxFiles);
    const walkMs = performance.now() - t;

    t = performance.now();
    const { defsByFile, refsByFile, cacheHits, cacheMisses } = await this.extractAll(files, { refresh });
    const extractMs = performance.now() - t;

    /** @type {Map<string, number>|undefined} */
    let mtimes;
    if (useRole) {
      mtimes = new Map();
      for (const file of files) {
        const cached = this.cache.get(file.rel);
        if (cached) mtimes.set(file.rel, cached.mtimeMs);
      }
    }

    t = performance.now();
    const { ranked, graphStats, roleScores } = this.rankFiles(defsByFile, refsByFile, {
      activeFile,
      query,
      ...(useRole ? { role, mtimes, now } : {}),
    });
    const graphMs = performance.now() - t;

    t = performance.now();
    const mapResult = this.buildRepoMap(ranked, defsByFile, effectiveBudget, { activeFile });
    const elideMs = performance.now() - t;

    return {
      map: mapResult.md,
      stack,
      ranks: ranked.slice(0, 20).map(([rel, score]) => [rel, Number(score.toFixed(6))]),
      ...(useRole && roleScores ? { roleScores: roleScores.slice(0, 20) } : {}),
      stats: {
        tokensUsed: mapResult.tokensUsed,
        filesShown: mapResult.filesShown,
        symbolsShown: mapResult.symbolsShown,
        filesScanned: walk.codeFiles + walk.docFiles,
        cacheHits,
        cacheMisses,
        walk,
        graph: graphStats,
        ...(useRole ? { role, effectiveBudget } : {}),
        timingMs: {
          classify: Math.round(classifyMs * 100) / 100,
          walk: Math.round(walkMs * 100) / 100,
          extract: Math.round(extractMs * 100) / 100,
          graph: Math.round(graphMs * 100) / 100,
          elide: Math.round(elideMs * 100) / 100,
          total: Math.round((performance.now() - started) * 100) / 100,
        },
      },
    };
  }

  /**
   * Symbol listing: definitions for one file (`path`) or the top-ranked files
   * whose definitions match `query`.
   * @param {object} options
   * @param {string} [options.path] single file relative to the root
   * @param {string} [options.query] case-insensitive symbol-name filter
   * @param {number} [options.limit] max files in query mode
   */
  async listSymbols(options = {}) {
    const { path: filePath, query, limit = 20 } = options;
    if (filePath) {
      const abs = resolvePath(this.root, filePath);
      const lang = SUPPORTED_EXTS[path.extname(abs).toLowerCase()];
      if (!lang) {
        throw new Error(`Unsupported file type for symbol extraction: ${filePath}`);
      }
      const stat = await fs.stat(abs);
      if (!stat.isFile()) throw new Error(`Not a file: ${filePath}`);
      if (stat.size > MAX_EXTRACT_BYTES) {
        throw new Error(`File too large for symbol extraction (max 1 MB): ${filePath}`);
      }
      const text = await fs.readFile(abs, "utf8");
      const rel = relativePosix(this.root, abs);
      const { defs } = this.extractFromText(rel, text, lang);
      return {
        path: filePath,
        language: lang,
        symbols: defs.map(({ name, line, kind, sig }) => ({ name, line, kind, signature: sig })),
      };
    }

    if (!query) {
      throw new Error("list_symbols requires either a file path or a query");
    }
    const { files } = await this.walkWorkspace(this.root);
    const { defsByFile, refsByFile } = await this.extractAll(files);
    const { ranked } = this.rankFiles(defsByFile, refsByFile, { query });
    const lowered = query.toLowerCase();
    const matches = [];
    for (const [rel, score] of ranked) {
      if (matches.length >= limit) break;
      const defs = (defsByFile.get(rel) ?? []).filter((d) =>
        d.name.toLowerCase().includes(lowered));
      if (defs.length === 0) continue;
      matches.push({
        path: rel,
        rank: Number(score.toFixed(6)),
        symbols: defs.map(({ name, line, kind, sig }) => ({ name, line, kind, signature: sig })),
      });
    }
    return { query, limit, files: matches };
  }
}
