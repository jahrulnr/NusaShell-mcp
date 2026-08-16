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
- search / grep / info / exists: locate and inspect entries. grep path may be a directory (recursive) or a single file; supports before/after context, ignoreCase, and exclude globs. search supports exclude, type filter, and maxDepth.

Path resolution: empty path = the Files root; / and absolute paths resolve to the OS filesystem root; relative paths resolve against the Files root; ../ traversal is allowed (no containment jail). Security is the user/AI provider's responsibility.

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

1. Map the territory: call tree with exclude=["node_modules", ".git", "dist", "build"] and includeFiles=false to get a dirs-only overview.
2. Find candidates: call search with a glob pattern (e.g. "*.ts") and exclude globs to narrow the result set. Use type="file" to skip directories.
3. Inspect before editing: read with start/end for a specific line range, or lineNumbers=true to make follow-up patches unambiguous. Use info for metadata without reading the body.
4. Locate usages: call grep with a regex pattern, before=2 and after=2 for context, and exclude=["node_modules", ".git"] to skip noise. Use ignoreCase=true when the casing is uncertain.
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
