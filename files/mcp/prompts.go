package main

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerPrompts(s *server.MCPServer) {
	s.AddPrompt(
		mcp.NewPrompt("howto",
			mcp.WithPromptDescription("Files plugin how-to"),
		),
		handleHowto,
	)
	s.AddPrompt(
		mcp.NewPrompt("explore-workflow",
			mcp.WithPromptDescription("Explore-then-edit workflow"),
		),
		handleExploreWorkflow,
	)
}

func handleHowto(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	if req.Params.Name != "howto" {
		return nil, fmt.Errorf("unknown prompt: %s", req.Params.Name)
	}
	text := `Use the Files plugin for bounded filesystem operations.

Main tools:
- list / tree: inspect directories. tree supports exclude globs and includeFiles=false for dirs-only.
- read: read text files. Use start/end for a line range, head/tail for first/last N lines, lineNumbers=true for line-prefixed output. Binary files are rejected with a helpful error.
- write / append / patch: change text files. patch accepts an edits array (with replace_all) and a preview mode that returns the patched content without writing.
- mkdir / move / copy / delete / touch: manage entries. touch creates an empty file or updates timestamps.
- search / grep / info / exists: locate and inspect entries. grep takes pattern (a non-empty regex) and path (absolute file or directory) — never command/cwd (those belong to the Terminal plugin's exec tool). Empty path is the Files default root (user home), not the conversation workspace; the receipt reports the resolved scan path. grep/search skip .git, hidden entries, common build dirs, and .gitignore/.ignore patterns by default.

Path resolution: empty path = the Files default root (user home). This MCP server is shared across agents and cannot use a per-conversation workspace. Relative paths are rejected; pass an absolute path. ../ traversal is allowed (no containment jail). Security is the user/AI provider's responsibility.

All mutating operations are atomic (write-to-temp-then-rename) so a crash never leaves a partial file. Search/grep results include a meta.truncated flag when the result cap is hit.`

	return &mcp.GetPromptResult{
		Description: "Files plugin how-to",
		Messages: []mcp.PromptMessage{
			{Role: mcp.RoleUser, Content: mcp.NewTextContent(text)},
		},
	}, nil
}

func handleExploreWorkflow(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	if req.Params.Name != "explore-workflow" {
		return nil, fmt.Errorf("unknown prompt: %s", req.Params.Name)
	}
	text := `Recommended workflow for exploring and editing an unfamiliar codebase with the Files plugin:

1. Map the territory: call tree with includeFiles=false to get a dirs-only overview (.git, node_modules, and gitignored paths are skipped by default).
2. Find candidates: call search with a glob pattern (e.g. "*.ts"). Use type="file" to skip directories.
3. Inspect before editing: read with start/end for a specific line range, or lineNumbers=true to make follow-up patches unambiguous. Use info for metadata without reading the body.
4. Locate usages: call grep with a regex as pattern and an absolute directory as path. Do not pass a shell command or cwd — that is the Terminal exec tool. Use before=2 and after=2 for context, ignoreCase=true when casing is uncertain.
5. Verify existence: call exists before reading or patching a path you are not sure about — it never throws on missing paths.
6. Patch safely: call patch with preview=true first to see the patched content, then call again with preview=false to apply. Pass an edits array for multiple replacements in one call; use replace_all=true only when you intentionally want every occurrence.
7. Confirm: re-read the patched region with read (start/end) to verify the change landed as expected.

Destructive operations (delete, move over an existing destination) are irreversible — confirm the path with exists or info first.`

	return &mcp.GetPromptResult{
		Description: "Explore-then-edit workflow",
		Messages: []mcp.PromptMessage{
			{Role: mcp.RoleUser, Content: mcp.NewTextContent(text)},
		},
	}, nil
}
