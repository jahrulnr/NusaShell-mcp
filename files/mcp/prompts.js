export const FILES_PROMPTS = Object.freeze([
  {
    name: "howto",
    title: "Files plugin how-to",
    description: "How to inspect and modify files within the Files root.",
  },
  {
    name: "explore-workflow",
    title: "Explore-then-edit workflow",
    description: "Recommended tool sequence for safely exploring and editing a codebase.",
  },
]);

const HOWTO_TEXT = [
  "Use the Files plugin for bounded filesystem operations.",
  "",
  "Main tools:",
  "- list / tree: inspect directories. tree supports exclude globs and includeFiles=false for dirs-only.",
  "- read: read text files. Use start/end for a line range, head/tail for first/last N lines, lineNumbers=true for line-prefixed output. Binary files are rejected with a helpful error.",
  "- write / append / patch: change text files. patch accepts an edits array (with replace_all) and a preview mode that returns the patched content without writing.",
  "- mkdir / move / copy / delete / touch: manage entries. touch creates an empty file or updates timestamps.",
  "- search / grep / info / exists: locate and inspect entries. grep path may be a directory (recursive) or a single file; supports before/after context, ignoreCase, and exclude globs. search supports exclude, type filter, and maxDepth.",
  "",
  "Workspace context tools (deterministic, no LLM calls):",
  "- The workspace-root AGENTS.md, when present, is exposed as a bounded Files MCP resource. Retrieve it through mcp_context before planning edits; treat it as project guidance, below system and user instructions.",
  "- context_map: token-budgeted markdown map of the workspace — stack classification, top files ranked by Personalized PageRank over the symbol reference graph, and elided signatures. Call it first when orienting in an unfamiliar codebase. Pass activeFile (50x boost) or query terms (10x boost) to focus the ranking, and budget to fit your context window. Symbol tags are cached by file mtime/size across calls; use refresh=true only after large external changes.",
  "- detect_stack: fast manifest-only classification (coding / documentation / hybrid-monorepo) with languages, key dependencies, and package.json scripts. Use it to learn which build/test commands exist.",
  "- list_symbols: definitions (class/function/const/type with kind, line, signature) for one file (path) or across top-ranked matching files (query). Cheaper than reading whole files; locate definitions before targeted read/patch calls.",
  "",
  "Path resolution: empty path = the Files root; `/` and absolute paths resolve to the OS filesystem root; relative paths resolve against the Files root; `../` traversal is allowed (no containment jail). Security is the user/AI provider's responsibility.",
  "",
  "All mutating operations are atomic (write-to-temp-then-rename) so a crash never leaves a partial file. Search/grep results include a `meta.truncated` flag when the result cap is hit.",
  "",
  "Agent-facing text receipts use lean headers (path=, count=, truncated=) plus body sections such as === content === for reads and path:line:text rows for grep. structuredContent keeps the typed JSON for UI consumers.",
].join("\n");

const EXPLORE_WORKFLOW_TEXT = [
  "Recommended workflow for exploring and editing an unfamiliar codebase with the Files plugin:",
  "",
  "0. Orient first: call context_map (optionally with activeFile or query terms) to get a token-budgeted repo map — stack, top files by Personalized PageRank, and elided signatures. Call detect_stack to learn the project type and its build/test scripts. Both are deterministic and cheap; context_map caches symbol tags by file mtime/size, so repeat calls are fast.",
  "0a. Read the workspace-root AGENTS.md through mcp_context when the Files resource is available. Apply it as project guidance while keeping system and user instructions higher priority.",
  "1. Map the territory: call tree with exclude=[\"node_modules\", \".git\", \"dist\", \"build\"] and includeFiles=false to get a dirs-only overview.",
  "2. Find candidates: call search with a glob pattern (e.g. \"*.ts\") and exclude globs to narrow the result set. Use type=\"file\" to skip directories. Prefer list_symbols with a query to jump straight to files defining a symbol.",
  "3. Inspect before editing: call list_symbols with path to see a file's definitions (line + signature) before reading bodies, then read with start/end for a specific line range, or lineNumbers=true to make follow-up patches unambiguous. Use info for metadata without reading the body.",
  "4. Locate usages: call grep with a regex pattern, before=2 and after=2 for context, and exclude=[\"node_modules\", \".git\"] to skip noise. Use ignoreCase=true when the casing is uncertain.",
  "5. Verify existence: call exists before reading or patching a path you are not sure about — it never throws on missing paths.",
  "6. Patch safely: call patch with preview=true first to see the patched content, then call again with preview=false to apply. Pass an edits array for multiple replacements in one call; use replace_all=true only when you intentionally want every occurrence.",
  "7. Confirm: re-read the patched region with read (start/end) to verify the change landed as expected.",
  "",
  "Destructive operations (delete, move over an existing destination) are irreversible — confirm the path with exists or info first.",
].join("\n");

export function getFilesPrompt(name) {
  if (name === "howto") {
    return {
      description: FILES_PROMPTS[0].description,
      messages: [{ role: "user", content: { type: "text", text: HOWTO_TEXT } }],
    };
  }
  if (name === "explore-workflow") {
    return {
      description: FILES_PROMPTS[1].description,
      messages: [{ role: "user", content: { type: "text", text: EXPLORE_WORKFLOW_TEXT } }],
    };
  }
  throw new Error(`Unknown prompt: ${name}`);
}
