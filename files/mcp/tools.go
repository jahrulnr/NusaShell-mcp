package main

import (
	"context"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTools(s *server.MCPServer, svc *FileService) {
	// list
	s.AddTool(mcp.NewTool("list",
		mcp.WithDescription("List directory contents with file metadata (name, size, modified, type)."),
		mcp.WithString("path",
			mcp.Description("Absolute directory path. Use empty string for the workspace root."),
		),
		mcp.WithReadOnlyHintAnnotation(true),
	), withHandlerTimeout(30*time.Second, handleList(svc)))

	// tree
	s.AddTool(mcp.NewTool("tree",
		mcp.WithDescription("Recursive directory tree up to a depth limit. Supports exclude globs and includeFiles filter."),
		mcp.WithString("path",
			mcp.Description("Absolute directory path."),
		),
		mcp.WithNumber("depth",
			mcp.Description("Maximum tree depth (1-10)."),
			mcp.Min(1.0), mcp.Max(10.0),
		),
		mcp.WithArray("exclude",
			mcp.Description("Glob patterns to exclude (e.g. [\"node_modules\", \".git\"]). Max 20."),
			mcp.MaxItems(20),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithBoolean("includeFiles",
			mcp.Description("Include files in the tree (default true). Set false for dirs-only."),
		),
		mcp.WithReadOnlyHintAnnotation(true),
	), withHandlerTimeout(30*time.Second, handleTree(svc)))

	// read
	s.AddTool(mcp.NewTool("read",
		mcp.WithDescription("Read a text file. Use start/end for a line range, or head/tail for first/last N lines. Binary files are rejected."),
		mcp.WithString("path", mcp.Required(),
			mcp.Description("Absolute file path."),
		),
		mcp.WithNumber("head", mcp.Description("Number of lines from the top."), mcp.Min(1.0)),
		mcp.WithNumber("tail", mcp.Description("Number of lines from the bottom."), mcp.Min(1.0)),
		mcp.WithNumber("start", mcp.Description("1-based start line."), mcp.Min(1.0)),
		mcp.WithNumber("end", mcp.Description("1-based end line (inclusive)."), mcp.Min(1.0)),
		mcp.WithBoolean("lineNumbers", mcp.Description("Prefix each line with NNN|.")),
		mcp.WithNumber("maxBytes", mcp.Description("Maximum UTF-8 bytes returned."), mcp.Min(1.0)),
		mcp.WithReadOnlyHintAnnotation(true),
	), withHandlerTimeout(30*time.Second, handleRead(svc)))

	// write
	s.AddTool(mcp.NewTool("write",
		mcp.WithDescription("Write content to a file. Creates parent directories. Atomic (write-to-temp-then-rename)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file path.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Content to write (UTF-8 text, max 10 MB).")),
		mcp.WithString("encoding", mcp.Description("Encoding: 'utf8' (default) or 'base64'."), mcp.Enum("utf8", "base64")),
		mcp.WithIdempotentHintAnnotation(true),
	), writeCap(handleWrite(svc)))

	// mkdir
	s.AddTool(mcp.NewTool("mkdir",
		mcp.WithDescription("Create a directory, including any missing parents."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute directory path.")),
		mcp.WithIdempotentHintAnnotation(true),
	), handleMkdir(svc))

	// move
	s.AddTool(mcp.NewTool("move",
		mcp.WithDescription("Move/rename a file or directory."),
		mcp.WithString("source", mcp.Required(), mcp.Description("Absolute source path.")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Absolute destination path.")),
		mcp.WithIdempotentHintAnnotation(true),
	), handleMove(svc))

	// copy
	s.AddTool(mcp.NewTool("copy",
		mcp.WithDescription("Copy a file or directory recursively."),
		mcp.WithString("source", mcp.Required(), mcp.Description("Absolute source path.")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Absolute destination path.")),
		mcp.WithIdempotentHintAnnotation(true),
	), handleCopy(svc))

	// delete
	s.AddTool(mcp.NewTool("delete",
		mcp.WithDescription("Delete a file or directory. Directories require recursive=true if not empty."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to delete.")),
		mcp.WithBoolean("recursive", mcp.Description("Allow deleting non-empty directories.")),
		mcp.WithDestructiveHintAnnotation(true),
	), handleDelete(svc))

	// search
	s.AddTool(mcp.NewTool("search",
		mcp.WithDescription("Search for files by name pattern (glob: * and ?). Supports exclude, type filter, and maxDepth."),
		mcp.WithString("path", mcp.Description("Absolute search root directory.")),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern (e.g. *.txt, config.*).")),
		mcp.WithArray("exclude",
			mcp.Description("Glob patterns to exclude."),
			mcp.MaxItems(20),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithString("type", mcp.Description("Filter by entry type (default any)."), mcp.Enum("file", "dir", "any")),
		mcp.WithNumber("maxDepth", mcp.Description("Maximum search depth (1-20)."), mcp.Min(1.0), mcp.Max(20.0)),
		mcp.WithReadOnlyHintAnnotation(true),
	), withHandlerTimeout(30*time.Second, handleSearch(svc)))

	// info
	s.AddTool(mcp.NewTool("info",
		mcp.WithDescription("Get detailed file metadata (size, dates, permissions, type)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file or directory path.")),
		mcp.WithReadOnlyHintAnnotation(true),
	), withHandlerTimeout(30*time.Second, handleInfo(svc)))

	// grep
	s.AddTool(mcp.NewTool("grep",
		mcp.WithDescription("Search file contents for a regex pattern (like grep). path may be a directory (recursive) or a single file. "+
			"output_mode: 'content' (default, returns matching lines with context), 'files_with_matches' (returns just file paths that contain matches), 'count' (returns per-file match counts)."),
		mcp.WithString("path", mcp.Description("Absolute directory or single file to search.")),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Regular expression pattern.")),
		mcp.WithString("glob", mcp.Description("Optional file name glob filter (e.g. '*.js').")),
		mcp.WithNumber("before", mcp.Description("Context lines before match (0-10)."), mcp.Min(0.0), mcp.Max(10.0)),
		mcp.WithNumber("after", mcp.Description("Context lines after match (0-10)."), mcp.Min(0.0), mcp.Max(10.0)),
		mcp.WithBoolean("ignoreCase", mcp.Description("Case-insensitive matching.")),
		mcp.WithString("output_mode", mcp.Description("Output shape: 'content' (default, matching lines), 'files_with_matches' (just file paths), 'count' (per-file match counts)."), mcp.Enum("content", "files_with_matches", "count")),
		mcp.WithArray("exclude",
			mcp.Description("Glob patterns to exclude."),
			mcp.MaxItems(20),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithNumber("maxResults", mcp.Description("Maximum results (1-1000)."), mcp.Min(1.0), mcp.Max(1000.0)),
		mcp.WithReadOnlyHintAnnotation(true),
	), withHandlerTimeout(30*time.Second, handleGrep(svc)))

	// patch — edits accepts a single edit object or an array of edits (oneOf).
	s.AddTool(mcp.NewTool("patch",
		mcp.WithDescription("Replace one or more string occurrences in a file. "+
			"By default old_string must match exactly once; if it matches multiple times the call fails with the line numbers of every match. "+
			"Disambiguate duplicates with replace_all=true, occurrence_index=N (1-based), or context_before/context_after (anchor text immediately before/after the match). "+
			"Supports preview mode."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file path.")),
		mcp.WithAny("edits",
			mcp.Description("A single edit object or an array of edits. Each edit: { old_string, new_string, replace_all?, occurrence_index?, context_before?, context_after? }."),
			mcp.Required(),
		),
		mcp.WithBoolean("preview", mcp.Description("If true, return patched content without writing.")),
		mcp.WithIdempotentHintAnnotation(true),
	), handlePatch(svc))

	// append
	s.AddTool(mcp.NewTool("append",
		mcp.WithDescription("Append content to the end of a file. Creates the file if it does not exist."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file path.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Content to append.")),
		mcp.WithIdempotentHintAnnotation(true),
	), writeCap(handleAppend(svc)))

	// exists
	s.AddTool(mcp.NewTool("exists",
		mcp.WithDescription("Check if a path exists. Returns { exists, isFile, isDir }. Does NOT throw on missing paths."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file or directory path.")),
		mcp.WithReadOnlyHintAnnotation(true),
	), withHandlerTimeout(10*time.Second, handleExists(svc)))

	// touch
	s.AddTool(mcp.NewTool("touch",
		mcp.WithDescription("Create an empty file if it doesn't exist, or update its timestamps if it does."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file path.")),
		mcp.WithBoolean("createParents", mcp.Description("Create parent directories if needed (default true).")),
		mcp.WithBoolean("updateOnly", mcp.Description("Only update timestamps of an existing file; throw if it doesn't exist.")),
		mcp.WithIdempotentHintAnnotation(true),
	), handleTouch(svc))

	// Advanced context tools (ported from files/mcp/context-engine.js and search-relevant.js).
	s.AddTool(contextMapTool(), withHandlerTimeout(60*time.Second, handleContextMap(svc)))
	s.AddTool(detectStackTool(), handleDetectStack(svc))
	s.AddTool(listSymbolsTool(), withHandlerTimeout(30*time.Second, handleSearchSymbols(svc)))
	s.AddTool(searchRelevantTool(), withHandlerTimeout(30*time.Second, handleSearchRelevant(svc)))
}

// --- Tool builders (advanced) ---

func contextMapTool() mcp.Tool {
	return mcp.NewTool("context_map",
		mcp.WithDescription("Build a token-budgeted markdown map of the repo: stack classification, top files ranked by Personalized PageRank over the symbol reference graph, and elided signatures. Optional role (planner|executor|reviewer) enables role-aware budgeting. Returns { map, stack, ranks, stats } and roleScores when role is set."),
		mcp.WithString("path", mcp.Description("Absolute directory path to map. Empty string maps the whole workspace root.")),
		mcp.WithNumber("budget", mcp.Description("Approximate token budget (default 1024)."), mcp.Min(64.0), mcp.Max(8192.0)),
		mcp.WithString("activeFile", mcp.Description("Workspace-relative file currently in focus; boosted 50x.")),
		mcp.WithString("query", mcp.Description("Space-separated symbol-name terms; matching files get a 10x boost.")),
		mcp.WithString("role", mcp.Description("Optional role for token budgeting + ranking."), mcp.Enum("planner", "executor", "reviewer")),
		mcp.WithNumber("maxFiles", mcp.Description("Scan cap (default 20000)."), mcp.Min(1.0), mcp.Max(20000.0)),
		mcp.WithBoolean("refresh", mcp.Description("Bypass the mtime/size symbol cache.")),
	)
}

func detectStackTool() mcp.Tool {
	return mcp.NewTool("detect_stack",
		mcp.WithDescription("Classify the workspace from manifest files (package.json, Cargo.toml, pyproject.toml, go.mod, ...): category, languages, manifests, key deps, package.json scripts. Reads manifests only."),
		mcp.WithString("path", mcp.Description("Absolute directory path to classify.")),
	)
}

func listSymbolsTool() mcp.Tool {
	return mcp.NewTool("list_symbols",
		mcp.WithDescription("List symbol definitions (class/function/const/type with kind, line, signature) for one file, or across top-ranked files matching a query."),
		mcp.WithString("path", mcp.Description("Absolute single file path. Omit to search by query.")),
		mcp.WithString("query", mcp.Description("Case-insensitive symbol-name filter; required when path omitted.")),
		mcp.WithNumber("limit", mcp.Description("Max files in query mode (default 20)."), mcp.Min(1.0), mcp.Max(100.0)),
	)
}

func searchRelevantTool() mcp.Tool {
	return mcp.NewTool("search_relevant",
		mcp.WithDescription("Semantic code search: retrieve most relevant files/chunks for a query (BM25 + TF-IDF, Reciprocal Rank Fusion, cross-encoder proxy). Returns { files, results, meta }."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query - plain-text description of the code/symbols you are looking for.")),
		mcp.WithNumber("topK", mcp.Description("Max results (1-20, default 5)."), mcp.Min(1.0), mcp.Max(20.0)),
		mcp.WithString("path", mcp.Description("Absolute directory to scope the search to. Empty = whole root.")),
		mcp.WithBoolean("refresh", mcp.Description("Bypass the mtime/size chunk cache.")),
	)
}

// withHandlerTimeout bounds a tool handler so a slow filesystem operation
// (huge grep/search/context_map) cannot leave the MCP request hanging when
// the process also services other tool calls.
func withHandlerTimeout(d time.Duration, h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return h(ctx, req)
	}
}

// --- Handlers ---

func handleList(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, _ := req.GetArguments()["path"].(string)
		items, err := svc.ListDir(path)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data := map[string]any{"path": path, "items": items}
		return textAndJSON(formatFilesToolText("list", data), data)
	}
}

func handleTree(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		depth := 3
		if d, ok := toInt(args["depth"]); ok {
			depth = d
		}
		exclude := toStringSlice(args["exclude"])
		includeFiles := true
		if v, ok := args["includeFiles"].(bool); ok {
			includeFiles = v
		}
		tree, err := svc.Tree(path, depth, exclude, includeFiles)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data := map[string]any{"path": path, "tree": tree}
		return textAndJSON(formatFilesToolText("tree", data), data)
	}
}

func handleRead(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		opts := ReadOpts{
			Head:        toIntOr(args["head"], 0),
			Tail:        toIntOr(args["tail"], 0),
			Start:       toIntOr(args["start"], 0),
			End:         toIntOr(args["end"], 0),
			LineNumbers: toBool(args["lineNumbers"]),
			MaxBytes:    toIntOr(args["maxBytes"], 0),
		}
		result, err := svc.ReadFile(path, opts)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data := map[string]any{
			"path":            path,
			"content":         result.Content,
			"totalLines":      result.TotalLines,
			"totalBytes":      result.TotalBytes,
			"truncated":       result.Truncated,
			"truncatedReason": result.TruncatedReason,
		}
		return textAndJSON(formatFilesToolText("read", data), data)
	}
}

func handleWrite(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		content, _ := args["content"].(string)
		encoding, _ := args["encoding"].(string)
		if encoding == "" {
			encoding = "utf8"
		}
		result, err := svc.WriteFile(path, content, encoding)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "write", args)
		return textAndJSON(formatFilesToolText("write", result), result)
	}
}

func handleMkdir(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		result, err := svc.MakeDir(path)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "mkdir", args)
		return textAndJSON(receiptFor("mkdir", result), result)
	}
}

func handleMove(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		source, _ := args["source"].(string)
		destination, _ := args["destination"].(string)
		result, err := svc.MoveFile(source, destination)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "move", args)
		return textAndJSON(receiptFor("move", result), result)
	}
}

func handleCopy(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		source, _ := args["source"].(string)
		destination, _ := args["destination"].(string)
		result, err := svc.CopyFile(source, destination)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "copy", args)
		return textAndJSON(receiptFor("copy", result), result)
	}
}

func handleDelete(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		recursive := toBool(args["recursive"])
		result, err := svc.DeleteFile(path, recursive)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "delete", args)
		return textAndJSON(receiptFor("delete", result), result)
	}
}

func handleSearch(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		pattern, _ := args["pattern"].(string)
		exclude := toStringSlice(args["exclude"])
		ftype, _ := args["type"].(string)
		if ftype == "" {
			ftype = "any"
		}
		maxDepth := toIntOr(args["maxDepth"], 10)
		result, err := svc.SearchFiles(path, pattern, exclude, ftype, maxDepth)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"path":    path,
			"pattern": pattern,
			"results": result["results"],
			"meta":    result["meta"],
		})
	}
}

func handleInfo(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, _ := req.GetArguments()["path"].(string)
		result, err := svc.FileInfo(path)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return textAndJSON(receiptFor("info", result), result)
	}
}

func handleGrep(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		pattern, _ := args["pattern"].(string)
		outputMode, _ := args["output_mode"].(string)
		opts := GrepOpts{
			Glob:       getString(args, "glob"),
			Before:     toIntOr(args["before"], 0),
			After:      toIntOr(args["after"], 0),
			IgnoreCase: toBool(args["ignoreCase"]),
			Exclude:    toStringSlice(args["exclude"]),
			MaxResults: toIntOr(args["maxResults"], 500),
			OutputMode: outputMode,
		}
		result, err := svc.GrepFiles(path, pattern, opts)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		// Forward the mode-specific keys (results / files / counts) plus
		// the common meta. The text summary uses the same data map.
		data := map[string]any{
			"path":    path,
			"pattern": pattern,
			"meta":    result["meta"],
		}
		for _, key := range []string{"results", "files", "counts"} {
			if v, ok := result[key]; ok {
				data[key] = v
			}
		}
		return textAndJSON(formatFilesToolText("grep", data), data)
	}
}

func handlePatch(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		preview := toBool(args["preview"])

		var edits []PatchEdit
		if editsRaw, ok := args["edits"]; ok {
			switch v := editsRaw.(type) {
			case map[string]any:
				edits = []PatchEdit{parsePatchEdit(v)}
			case []any:
				for _, item := range v {
					if m, ok := item.(map[string]any); ok {
						edits = append(edits, parsePatchEdit(m))
					}
				}
			}
		}
		if len(edits) == 0 {
			return errorResult("edits is required (single edit or array of edits)"), nil
		}

		result, err := svc.PatchFile(path, edits, preview)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "patch", args)
		return textAndJSON(formatFilesToolText("patch", result), result)
	}
}

func parsePatchEdit(m map[string]any) PatchEdit {
	e := PatchEdit{}
	if v, ok := m["old_string"].(string); ok {
		e.OldString = v
	}
	if v, ok := m["new_string"].(string); ok {
		e.NewString = v
	}
	if v, ok := m["replace_all"].(bool); ok {
		e.ReplaceAll = v
	}
	if v, ok := toInt(m["occurrence_index"]); ok {
		idx := v
		e.OccurrenceIndex = &idx
	}
	if v, ok := m["context_before"].(string); ok {
		e.ContextBefore = v
	}
	if v, ok := m["context_after"].(string); ok {
		e.ContextAfter = v
	}
	return e
}

func handleAppend(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		content, _ := args["content"].(string)
		result, err := svc.AppendFile(path, content)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "append", args)
		return textAndJSON(receiptFor("append", result), result)
	}
}

func handleExists(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, _ := req.GetArguments()["path"].(string)
		result, err := svc.ExistsFile(path)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return textAndJSON(formatFilesToolText("exists", result), result)
	}
}

func handleTouch(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path, _ := args["path"].(string)
		createParents := true
		if v, ok := args["createParents"].(bool); ok {
			createParents = v
		}
		updateOnly := toBool(args["updateOnly"])
		result, err := svc.TouchFile(path, createParents, updateOnly)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		emitAutomation(ctx, "touch", args)
		return textAndJSON(receiptFor("touch", result), result)
	}
}

// receiptFor builds a mutation text receipt like formatMutationText.
func receiptFor(_ string, result map[string]any) string {
	return formatMutationText(result)
}

// textAndJSON returns a CallToolResult with both readable text and a
// structured payload, matching the JS mcpToolResult shape.
func textAndJSON(text string, data any) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(text),
		},
		StructuredContent: data,
	}, nil
}

func textJSON(data any) (*mcp.CallToolResult, error) {
	return textAndJSON(formatGenericText(data), data)
}

// --- Advanced handlers ---

func handleContextMap(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		engine := svc.engine()
		subPath, _ := args["path"].(string)
		budget := toIntOr(args["budget"], 1024)
		activeFile, _ := args["activeFile"].(string)
		query, _ := args["query"].(string)
		role, _ := args["role"].(string)
		maxFiles := toIntOr(args["maxFiles"], 0)
		refresh := toBool(args["refresh"])
		result, err := engine.ContextMap(subPath, budget, activeFile, query, role, maxFiles, refresh)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return textJSON(result)
	}
}

func handleDetectStack(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		engine := svc.engine()
		subPath, _ := args["path"].(string)
		stack, err := engine.DetectStack(subPath)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		result := map[string]any{"path": subPath}
		for k, v := range stack {
			result[k] = v
		}
		return textJSON(result)
	}
}

func handleSearchSymbols(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		engine := svc.engine()
		filePath, _ := args["path"].(string)
		query, _ := args["query"].(string)
		limit := toIntOr(args["limit"], 20)
		result, err := engine.ListSymbols(filePath, query, limit)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return textJSON(result)
	}
}

func handleSearchRelevant(svc *FileService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		engine := svc.retrieval()
		query, _ := args["query"].(string)
		topK := toIntOr(args["topK"], 5)
		scope, _ := args["path"].(string)
		refresh := toBool(args["refresh"])
		result := engine.searchRelevantContext(ctx, query, topK, scope, refresh)
		return textJSON(result)
	}
}

// --- helpers ---

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func toIntOr(v any, def int) int {
	if n, ok := toInt(v); ok {
		return n
	}
	return def
}

func toBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func getString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func jsonResult(data any) (*mcp.CallToolResult, error) {
	return textAndJSON(formatGenericText(data), data)
}

// maxWriteBytes bounds a single write/append payload (10 MB, matching the
// tool description). Larger payloads are rejected before touching the disk
// so a stray huge request cannot stall the server or the reply pipe.
const maxWriteBytes = 10 * 1024 * 1024

// writeCap guards a write-like handler against oversized payloads.
func writeCap(h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		if content, ok := args["content"].(string); ok && len(content) > maxWriteBytes {
			return errorResult("content too large: max " + itoa(maxWriteBytes) + " bytes"), nil
		}
		return h(ctx, req)
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent(msg),
		},
	}
}

// itoa formats an int without importing strconv in files main package.
func itoa(v int) string {
	return strconv.Itoa(v)
}
