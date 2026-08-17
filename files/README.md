# NusaShell Files Plugin

A bundled example plugin exposing a file-system MCP server (read, write, list,
tree, search, grep, patch, move, copy, delete, info, append, exists, touch)
plus deterministic workspace-context tools (context_map, detect_stack,
list_symbols) and a bounded workspace resource for the root `AGENTS.md`.

## Workspace context tools

`mcp/context-engine.js` ports the "Riset Workspace Context Indexing" design
(Aider-style repo map) to dependency-free Node.js. Six phases run without any
LLM call: manifest-based stack classification, `.gitignore`-aware walking,
regex fallback-lexer symbol extraction (13 languages), a directed symbol
reference graph ranked by Personalized PageRank, scope-aware elision fitted to
a token budget via binary search, and an in-memory tag cache invalidated by
`(path, mtime, size)`.

- `context_map` — full pipeline; returns `{ map, stack, ranks, stats }`.
  `activeFile` gets a 50x PageRank boost, `query` symbol terms 10x. The
  research doc's SQLite cache is replaced with an in-memory Map (the stdio
  process is long-lived; node20 has no dependency-free SQLite). Edge direction
  flows referencer → definer so foundational files rank highest.
- `detect_stack` — manifest-only classification (fast, no tree walk).
- `list_symbols` — definitions for one file (`path`) or top-ranked files
  matching a symbol-name `query`.

The Files MCP also exposes `nusashell://workspace/AGENTS.md` through the MCP
Resources capability when `AGENTS.md` exists at the configured workspace root.
It is capped at 50 KiB and intentionally limited to the root file; nested
instruction files require a target-path policy and are not mixed into the
workspace-wide context. Agents can retrieve it with the shell's `mcp_context`
tool. It is project guidance and remains below system and user instructions.

## Path resolution

All file operations require **absolute paths**. The server is shared
between concurrent agents, so relative paths are rejected (they have no
stable meaning across agents with different workspaces).

- **Absolute paths** (e.g. `/home/user/project/file.go`) → accepted and
  cleaned via `filepath.Clean`.
- **Relative paths** → rejected with an error. The caller must pass an
  absolute path.
- **Empty input** → rejected (forces explicitness).

Results (list, tree, grep, etc.) always return **absolute paths** so
callers can round-trip them back as inputs without ambiguity.

There is **no containment jail**. Security is the user/AI provider's
responsibility — see
[docs/architecture/security-boundary.md](../../docs/architecture/security-boundary.md).

### Production runs the bundle

The manifest runs `node mcp/server.cjs` — the **esbuild bundle**, not the
source. After editing `mcp/config.js`, `mcp/fs-service.js`, `mcp/tools.js`, or
anything they import, rebuild so the shipped artifact matches the source:

```bash
cd plugins/files
npm run build   # regenerates mcp/server.cjs
npm test        # unit tests
```

### What is NOT in scope

- A `System32` denylist or any path-based containment — the root is a
  convenience for relative path resolution, not a jail.
- Changing the root default away from home (product decision); this plugin
  only resolves paths, it does not choose the root.
