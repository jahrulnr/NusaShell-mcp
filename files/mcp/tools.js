import { z } from "zod";
import { FILES_TOOL_NAMES } from "./tool-catalog.js";
import { ContextEngine } from "./context-engine.js";
import { RetrievalEngine } from "./search-relevant.js";

const filePath = z.string().trim().min(1).max(4096);
const rootPath = z.string().trim().min(0).max(4096).default("");

/**
 * Coerce + clamp an integer into [min, max] instead of rejecting out-of-range
 * values. Agents frequently ask past tool caps (e.g. after=12 when max is 10);
 * auto-recovering is better than "Files tool input is invalid" dead-ends.
 * Non-finite / missing values use defaultValue when provided, else undefined.
 * @param {number} min
 * @param {number} max
 * @param {number} [defaultValue]
 */
function clampedInt(min, max, defaultValue) {
  const inner = z.number().int().min(min).max(max);
  const schema = z.preprocess((value) => {
    if (value === undefined || value === null || value === "") {
      return defaultValue;
    }
    const n = typeof value === "number" ? value : Number(value);
    if (!Number.isFinite(n)) return defaultValue;
    return Math.trunc(Math.min(max, Math.max(min, n)));
  }, defaultValue === undefined ? inner.optional() : inner);
  return defaultValue === undefined ? schema : schema.default(defaultValue);
}

const depth = clampedInt(1, 10, 3);
const head = clampedInt(1, 100000);
const tail = clampedInt(1, 100000);
const startLine = clampedInt(1, 100000);
const endLine = clampedInt(1, 100000);
const lineNumbers = z.boolean().default(false);
const maxBytes = clampedInt(1, 100 * 1024 * 1024, 10 * 1024 * 1024);
const recursive = z.boolean().default(false);
const pattern = z.string().trim().min(1).max(500);

const grepGlob = z.string().trim().min(1).max(500).optional();
const excludeGlobs = z.array(z.string().trim().min(1).max(500)).max(20).optional();
const oldString = z.string().min(1).max(1024 * 1024);
const newString = z.string().max(1024 * 1024);
const grepContext = clampedInt(0, 10, 0);
const grepMaxResults = clampedInt(1, 1000, 500);
const searchMaxDepth = clampedInt(1, 20, 10);
const contextBudget = clampedInt(64, 8192, 1024);
const contextMaxFiles = clampedInt(1, 20000);
const listSymbolsLimit = clampedInt(1, 100, 20);

const schemas = {
  list: z.object({ path: rootPath }).strict(),
  tree: z.object({ path: rootPath, depth, exclude: excludeGlobs, includeFiles: z.boolean().default(true) }).strict(),
  read: z.object({ path: filePath, head, tail, start: startLine, end: endLine, lineNumbers, maxBytes }).strict(),
  write: z.object({ path: filePath, content: z.string().max(10 * 1024 * 1024), encoding: z.enum(["utf8", "base64"]).default("utf8") }).strict(),
  mkdir: z.object({ path: filePath }).strict(),
  move: z.object({ source: filePath, destination: filePath }).strict(),
  copy: z.object({ source: filePath, destination: filePath }).strict(),
  delete: z.object({ path: filePath, recursive }).strict(),
  search: z.object({ path: rootPath, pattern, exclude: excludeGlobs, type: z.enum(["file", "dir", "any"]).default("any"), maxDepth: searchMaxDepth }).strict(),
  info: z.object({ path: filePath }).strict(),
  grep: z.object({
    path: rootPath,
    pattern,
    glob: grepGlob,
    before: grepContext,
    after: grepContext,
    ignoreCase: z.boolean().default(false),
    exclude: excludeGlobs,
    maxResults: grepMaxResults,
  }).strict(),
  patch: z.object({
    path: filePath,
    edits: z.union([
      z.object({ old_string: oldString, new_string: newString, replace_all: z.boolean().default(false) }),
      z.array(z.object({ old_string: oldString, new_string: newString, replace_all: z.boolean().default(false) })),
    ]),
    preview: z.boolean().default(false),
  }).strict(),
  append: z.object({ path: filePath, content: z.string().max(10 * 1024 * 1024) }).strict(),
  exists: z.object({ path: filePath }).strict(),
  touch: z.object({ path: filePath, createParents: z.boolean().default(true), updateOnly: z.boolean().default(false) }).strict(),
  context_map: z.object({
    path: rootPath,
    budget: contextBudget,
    activeFile: z.string().trim().min(1).max(4096).optional(),
    query: z.string().trim().min(1).max(500).optional(),
    role: z.enum(["planner", "executor", "reviewer"]).optional(),
    maxFiles: contextMaxFiles,
    refresh: z.boolean().default(false),
  }).strict(),
  detect_stack: z.object({ path: rootPath }).strict(),
  list_symbols: z.object({
    path: filePath.optional(),
    query: z.string().trim().min(1).max(500).optional(),
    limit: listSymbolsLimit,
  }).strict(),
  search_relevant: z.object({
    query: z.string().trim().min(1).max(500),
    topK: clampedInt(1, 20, 5),
    path: rootPath,
    refresh: z.boolean().default(false),
  }).strict(),
};

export const FILES_TOOLS = Object.freeze([
  descriptor("list", "List directory contents with file metadata (name, size, modified, type).", {
    path: stringProperty("Directory path relative to the files plugin root (user home by default). Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
  }),
  descriptor("tree", "Recursive directory tree up to a depth limit. Supports exclude globs and includeFiles filter.", {
    path: stringProperty("Directory path relative to the files plugin root (user home by default). Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
    depth: integerProperty(1, 10, 3, "Maximum tree depth (1-10)."),
    exclude: { type: "array", items: { type: "string" }, description: "Glob patterns to exclude (e.g. [\"node_modules\", \".git\"]). Max 20." },
    includeFiles: { type: "boolean", description: "Include files in the tree (default true). Set false for dirs-only.", default: true },
  }),
  descriptor("read", "Read a text file. Use start/end for a line range, or head/tail for first/last N lines. Binary files are rejected.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    head: integerProperty(1, 100000, undefined, "Number of lines from the top (legacy, use start/end for ranges)."),
    tail: integerProperty(1, 100000, undefined, "Number of lines from the bottom (legacy, use start/end for ranges)."),
    start: integerProperty(1, 100000, undefined, "1-based start line (inclusive). Takes priority over head/tail."),
    end: integerProperty(1, 100000, undefined, "1-based end line (inclusive). Takes priority over head/tail."),
    lineNumbers: { type: "boolean", description: "Prefix each line with its 1-based line number (NNN|content).", default: false },
    maxBytes: integerProperty(1, 104857600, 10485760, "Maximum UTF-8 bytes returned in content; larger text files are truncated (default 10 MB)."),
  }, ["path"]),
  descriptor("write", "Create or overwrite a file. Parent directories are created automatically.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    content: { type: "string", description: "File content (UTF-8 text, max 10 MB). When encoding is base64, the Base64-encoded bytes to decode." },
    encoding: { type: "string", enum: ["utf8", "base64"], description: "How to interpret content (default utf8). Use base64 for binary uploads.", default: "utf8" },
  }, ["path", "content"], false),
  descriptor("mkdir", "Create an empty directory. Missing parent directories are created automatically.", {
    path: stringProperty("Directory path relative to the files plugin root (user home by default)."),
  }, ["path"], false),
  descriptor("move", "Move or rename a file or directory.", {
    source: stringProperty("Current path relative to the files plugin root (user home by default)."),
    destination: stringProperty("New path relative to the files plugin root (user home by default)."),
  }, ["source", "destination"], false),
  descriptor("copy", "Copy a file or directory recursively. Destination parent directories are created automatically.", {
    source: stringProperty("Path to copy from, relative to the files plugin root (user home by default)."),
    destination: stringProperty("Path to copy to, relative to the files plugin root (user home by default)."),
  }, ["source", "destination"], false),
  descriptor("delete", "Delete a file or directory. Directories require recursive=true if not empty.", {
    path: stringProperty("Path to delete relative to the files plugin root (user home by default)."),
    recursive: { type: "boolean", description: "Allow deleting non-empty directories.", default: false },
  }, ["path"], false),
  descriptor("search", "Search for files by name pattern (glob: * and ?). Supports exclude, type filter, and maxDepth.", {
    path: stringProperty("Search root directory relative to the files plugin root. Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
    pattern: stringProperty("Glob pattern (e.g. *.txt, config.*, *.test.js)."),
    exclude: { type: "array", items: { type: "string" }, description: "Glob patterns to exclude (e.g. [\"node_modules\", \".git\"]). Max 20." },
    type: { type: "string", enum: ["file", "dir", "any"], description: "Filter by entry type (default any).", default: "any" },
    maxDepth: integerProperty(1, 20, 10, "Maximum search depth (1-20)."),
  }, ["pattern"]),
  descriptor("info", "Get detailed file metadata (size, dates, permissions, type).", {
    path: stringProperty("File or directory path relative to the files plugin root (user home by default)."),
  }, ["path"]),
  descriptor("grep", "Search file contents for a regex pattern (like grep). path may be a directory (recursive) or a single file. Only text files are scanned. Supports context lines, ignoreCase, and exclude globs. Out-of-range before/after/maxResults are clamped (not rejected).", {
    path: stringProperty("Directory or single file to search, relative to the files plugin root. Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
    pattern: stringProperty("Regular expression pattern to match against file contents (e.g. 'function\\s+\\w+', 'TODO.*')."),
    glob: stringProperty("Optional file name glob filter when path is a directory (e.g. '*.js', '*.ts'). Ignored when path is a single file. If omitted under a directory, all text files are scanned."),
    before: integerProperty(0, 10, 0, "Context lines before each match (0-10; values outside this range are clamped)."),
    after: integerProperty(0, 10, 0, "Context lines after each match (0-10; values outside this range are clamped)."),
    ignoreCase: { type: "boolean", description: "Case-insensitive matching.", default: false },
    exclude: { type: "array", items: { type: "string" }, description: "Glob patterns to exclude under a directory path (e.g. [\"node_modules\", \".git\"]). Max 20." },
    maxResults: integerProperty(1, 1000, 500, "Maximum number of results (1-1000; values outside this range are clamped)."),
  }, ["pattern"]),
  descriptor("patch", "Replace one or more string occurrences in a file. Supports replace_all and preview mode. Safer than write for small edits.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    edits: {
      type: "object",
      description: "A single edit object or an array of edits. Each edit: { old_string, new_string, replace_all }.",
      properties: {
        old_string: { type: "string", description: "Exact string to find (must match exactly, including whitespace)." },
        new_string: { type: "string", description: "Replacement string." },
        replace_all: { type: "boolean", description: "Replace all occurrences (default: false = first only).", default: false },
      },
    },
    preview: { type: "boolean", description: "If true, return the patched content without writing to disk.", default: false },
  }, ["path", "edits"], false),
  descriptor("append", "Append content to the end of a file. Creates the file if it does not exist.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    content: { type: "string", description: "Content to append (UTF-8 text, max 10 MB)." },
  }, ["path", "content"], false),
  descriptor("exists", "Check if a path exists. Returns { exists, isFile, isDir }. Does NOT throw on missing paths.", {
    path: stringProperty("File or directory path relative to the files plugin root (user home by default)."),
  }, ["path"]),
  descriptor("touch", "Create an empty file if it doesn't exist, or update its timestamps if it does.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    createParents: { type: "boolean", description: "Create parent directories if needed (default true).", default: true },
    updateOnly: { type: "boolean", description: "Only update timestamps of an existing file; throw if it doesn't exist.", default: false },
  }, ["path"], false),
  descriptor("context_map", "Build a token-budgeted markdown map of the workspace: stack classification, the top files ranked by Personalized PageRank over the symbol reference graph, and elided signatures (bodies replaced with ⋮). Deterministic — no LLM calls. Optional role (planner|executor|reviewer) enables role-aware token budgeting and ranking (docs for planner, implementation for executor, tests/conventions for reviewer); omit role for legacy behavior. Symbol tags are cached by file mtime/size across calls; pass refresh=true only after large external changes. Returns { map, stack, ranks, stats } and roleScores when role is set.", {
    path: stringProperty("Directory to map, relative to the files plugin root. Empty string maps the whole root.", ""),
    budget: integerProperty(64, 8192, 1024, "Approximate token budget for the markdown map (~4 chars/token). The top-ranked files are binary-searched to fit. When role is set, an effective budget is derived from this base without changing the public budget argument."),
    activeFile: { type: "string", description: "Workspace-relative file currently in focus; boosted 50x in Personalized PageRank so its dependency neighborhood ranks higher." },
    query: { type: "string", description: "Space-separated symbol-name terms; files defining matching symbols get a 10x rank boost." },
    role: {
      type: "string",
      enum: ["planner", "executor", "reviewer"],
      description: "Optional agent role for RCR-style budgeting and ranking. planner favors docs/AGENTS; executor favors non-test source; reviewer favors tests and conventions. Omit for legacy maps.",
    },
    maxFiles: integerProperty(1, 20000, undefined, "Scan cap for walked code/doc files (default 20000). .gitignore/.ignore and common build dirs are always excluded."),
    refresh: { type: "boolean", description: "Bypass the mtime/size symbol cache and re-extract every file.", default: false },
  }),
  descriptor("detect_stack", "Classify the workspace from manifest files (package.json, Cargo.toml, pyproject.toml, go.mod, ...): category (coding / documentation / hybrid-monorepo), languages, key framework dependencies, and package.json scripts. Fast — reads manifests only, no full tree walk. Use to learn which build/test commands exist before running anything.", {
    path: stringProperty("Directory to classify, relative to the files plugin root. Empty string classifies the root.", ""),
  }),
  descriptor("list_symbols", "List symbol definitions (class/function/const/type with kind, line, and signature) for one file, or across the top-ranked files whose definitions match a query. Much cheaper than reading whole files — use it to locate definitions before targeted read/patch calls. Pass either path (single file) or query (workspace-wide search).", {
    path: { type: "string", description: "Single file relative to the files plugin root. Omit to search by query instead." },
    query: { type: "string", description: "Case-insensitive symbol-name filter; returns matching definitions from the top PageRanked files. Required when path is omitted." },
    limit: integerProperty(1, 100, 20, "Maximum number of files returned in query mode."),
  }),
  descriptor("search_relevant", "Semantic code search: retrieve the most relevant files/chunks for a query using hybrid retrieval (BM25 + TF-IDF dense proxy fused with Reciprocal Rank Fusion, reranked by a cross-encoder proxy). Deterministic and cached by file mtime+size; pass refresh=true to bypass the chunk cache. Returns { query, results: [{ path, line, lineEnd, score, snippet }], meta }.", {
    query: stringProperty("Search query — plain-text description of the code/symbols you are looking for (e.g. \"plugin runtime lifecycle\")."),
    topK: integerProperty(1, 20, 5, "Maximum number of results to return (1-20)."),
    path: stringProperty("Directory to scope the search to, relative to the files plugin root. Empty string searches the whole root.", ""),
    refresh: { type: "boolean", description: "Bypass the mtime/size chunk cache and re-index the workspace.", default: false },
  }, ["query"]),
]);

if (FILES_TOOLS.map((tool) => tool.name).join(",") !== FILES_TOOL_NAMES.join(",")) {
  throw new Error("Files tool descriptors are out of sync with the canonical catalog");
}

export async function callFilesTool(service, name, rawArguments = {}, contextEngine = null, retrievalEngine = null) {
  const schema = schemas[name];
  if (!schema) throw new Error(`Unknown files tool: ${name}`);
  const input = schema.parse(rawArguments ?? {});

  // The context engine is injected by the server so the tag cache persists
  // across calls; the lazy fallback keeps the dispatcher usable without one.
  let engine = contextEngine;
  function getContextEngine() {
    if (!engine) engine = new ContextEngine(service.root);
    return engine;
  }

  // Same pattern for the retrieval engine: the server injects one so the
  // mtime chunk cache survives across search_relevant calls.
  let retrieval = retrievalEngine;
  function getRetrievalEngine() {
    if (!retrieval) retrieval = new RetrievalEngine(service.root);
    return retrieval;
  }

  switch (name) {
    case "list":
      return { path: input.path, items: await service.listDir(input.path) };
    case "tree":
      return { path: input.path, tree: await service.tree(input.path, input.depth, { exclude: input.exclude, includeFiles: input.includeFiles }) };
    case "read":
      return { path: input.path, ...(await service.readFile(input.path, {
        head: input.head,
        tail: input.tail,
        start: input.start,
        end: input.end,
        lineNumbers: input.lineNumbers,
        maxBytes: input.maxBytes,
      })) };
    case "write":
      return await service.writeFile(input.path, input.content, { encoding: input.encoding });
    case "mkdir":
      return await service.makeDir(input.path);
    case "move":
      return await service.moveFile(input.source, input.destination);
    case "copy":
      return await service.copyFile(input.source, input.destination);
    case "delete":
      return await service.deleteFile(input.path, input.recursive);
    case "search": {
      const searchResult = await service.searchFiles(input.path, input.pattern, { exclude: input.exclude, type: input.type, maxDepth: input.maxDepth });
      return { path: input.path, pattern: input.pattern, results: searchResult.results, meta: searchResult.meta };
    }
    case "grep": {
      const grepResult = await service.grepFiles(input.path, input.pattern, {
        glob: input.glob,
        before: input.before,
        after: input.after,
        ignoreCase: input.ignoreCase,
        exclude: input.exclude,
        maxResults: input.maxResults,
      });
      return { path: input.path, pattern: input.pattern, ...(input.glob ? { glob: input.glob } : {}), results: grepResult.results, meta: grepResult.meta };
    }
    case "info":
      return await service.fileInfo(input.path);
    case "patch": {
      const edits = Array.isArray(input.edits) ? input.edits : [input.edits];
      return await service.patchFile(input.path, edits, input.preview);
    }
    case "append":
      return await service.appendFile(input.path, input.content);
    case "exists":
      return await service.existsFile(input.path);
    case "touch":
      return await service.touchFile(input.path, { createParents: input.createParents, updateOnly: input.updateOnly });
    case "context_map":
      return { path: input.path, ...(await getContextEngine().contextMap({
        path: input.path,
        budget: input.budget,
        activeFile: input.activeFile,
        query: input.query,
        role: input.role,
        maxFiles: input.maxFiles,
        refresh: input.refresh,
      })) };
    case "detect_stack":
      return { path: input.path, ...(await getContextEngine().detectStack(input.path)) };
    case "list_symbols":
      return await getContextEngine().listSymbols({
        path: input.path,
        query: input.query,
        limit: input.limit,
      });
    case "search_relevant":
      return await getRetrievalEngine().searchRelevant({
        query: input.query,
        topK: input.topK,
        path: input.path,
        refresh: input.refresh,
      });
    default:
      throw new Error(`Unknown files tool: ${name}`);
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
      idempotentHint: readOnly || name === "write" || name === "move",
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

function stringProperty(description, defaultValue) {
  return {
    type: "string",
    description,
    ...(defaultValue ? { default: defaultValue } : {}),
  };
}

function integerProperty(minimum, maximum, defaultValue, description) {
  return {
    type: "integer",
    description,
    minimum,
    ...(maximum ? { maximum } : {}),
    ...(defaultValue ? { default: defaultValue } : {}),
  };
}
