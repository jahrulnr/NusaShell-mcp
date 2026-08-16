package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Tool names — must not start with "notes_" or equal "notes" (create rule).
const (
	toolCreate = "create"
	toolList   = "list"
	toolGet    = "get"
	toolUpdate = "update"
	toolDelete = "delete"
	toolSearch = "search"
)

func registerTools(s *server.MCPServer, svc *NoteService) {
	s.AddTool(
		mcp.NewTool(toolCreate,
			mcp.WithDescription("Create a new note with optional tags. Text supports markdown."),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("Note content (markdown supported, max 100 KB)."),
				mcp.MaxLength(maxTextLength),
				mcp.MinLength(1),
			),
			mcp.WithArray("tags",
				mcp.Description("Optional tags (max 20, each max 60 chars)."),
				mcp.MaxItems(maxTags),
				mcp.Items(map[string]any{"type": "string"}),
			),
		),
		handleCreate(svc),
	)

	s.AddTool(
		mcp.NewTool(toolList,
			mcp.WithDescription("List all notes, optionally filtered by tag. Results are sorted by updatedAt (default) or createdAt."),
			mcp.WithString("tag",
				mcp.Description("Filter notes by tag name. Omit to list all."),
				mcp.MaxLength(maxTagLength),
			),
			mcp.WithString("sort",
				mcp.Description("Sort order: 'updated' (default) or 'created'."),
				mcp.Enum("updated", "created"),
			),
		),
		handleList(svc),
	)

	s.AddTool(
		mcp.NewTool(toolGet,
			mcp.WithDescription("Get a single note by its ID."),
			mcp.WithNumber("id",
				mcp.Required(),
				mcp.Description("Note ID (positive integer)."),
				mcp.Min(1.0),
			),
		),
		handleGet(svc),
	)

	s.AddTool(
		mcp.NewTool(toolUpdate,
			mcp.WithDescription("Update a note's text and/or tags. Only provided fields are changed."),
			mcp.WithNumber("id",
				mcp.Required(),
				mcp.Description("Note ID to update."),
				mcp.Min(1.0),
			),
			mcp.WithString("text",
				mcp.Description("New note text (markdown supported, max 100 KB). Omit to keep existing."),
				mcp.MaxLength(maxTextLength),
			),
			mcp.WithArray("tags",
				mcp.Description("New tags array (replaces existing). Omit to keep existing."),
				mcp.MaxItems(maxTags),
				mcp.Items(map[string]any{"type": "string"}),
			),
		),
		handleUpdate(svc),
	)

	s.AddTool(
		mcp.NewTool(toolDelete,
			mcp.WithDescription("Delete a note by its ID. This is permanent and cannot be undone."),
			mcp.WithNumber("id",
				mcp.Required(),
				mcp.Description("Note ID to delete."),
				mcp.Min(1.0),
			),
		),
		handleDelete(svc),
	)

	s.AddTool(
		mcp.NewTool(toolSearch,
			mcp.WithDescription("Search notes by text content using a regex pattern (case-insensitive)."),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Regex pattern to match against note text (case-insensitive)."),
				mcp.MaxLength(500),
			),
		),
		handleSearch(svc),
	)
}

func handleCreate(svc *NoteService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		text, _ := args["text"].(string)
		tags := toStringSlice(args["tags"])

		note, err := svc.Create(text, tags)
		if err != nil {
			return errorResult(err), nil
		}
		if err := svc.Save(); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{
			"note":       note,
			"totalNotes": svc.Total(),
		})
	}
}

func handleList(svc *NoteService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		tag, _ := args["tag"].(string)
		sortKey, _ := args["sort"].(string)
		if sortKey == "" {
			sortKey = "updated"
		}

		notes := svc.List(tag, sortKey)
		result := map[string]any{
			"notes": notes,
			"total": len(notes),
			"sort":  sortKey,
		}
		if tag != "" {
			result["tag"] = tag
		}
		return jsonResult(result)
	}
}

func handleGet(svc *NoteService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, ok := toInt(req.GetArguments()["id"])
		if !ok {
			return errorResult(fmt.Errorf("id must be a positive integer")), nil
		}
		note, err := svc.Get(id)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"note": note})
	}
}

func handleUpdate(svc *NoteService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, ok := toInt(args["id"])
		if !ok {
			return errorResult(fmt.Errorf("id must be a positive integer")), nil
		}

		var updates NoteUpdate
		if text, exists := args["text"]; exists {
			if s, ok := text.(string); ok {
				updates.Text = &s
			}
		}
		if tags, exists := args["tags"]; exists {
			slice := toStringSlice(tags)
			updates.Tags = &slice
		}

		note, err := svc.Update(id, updates)
		if err != nil {
			return errorResult(err), nil
		}
		if err := svc.Save(); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"note": note})
	}
}

func handleDelete(svc *NoteService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, ok := toInt(req.GetArguments()["id"])
		if !ok {
			return errorResult(fmt.Errorf("id must be a positive integer")), nil
		}
		deleted, err := svc.Delete(id)
		if err != nil {
			return errorResult(err), nil
		}
		if err := svc.Save(); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{
			"deleted":    deleted,
			"totalNotes": svc.Total(),
		})
	}
}

func handleSearch(svc *NoteService) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.GetArguments()["query"].(string)
		results := svc.Search(query)
		return jsonResult(map[string]any{
			"results": results,
			"total":   len(results),
			"query":   query,
		})
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

func jsonResult(data any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(raw)),
		},
		StructuredContent: data,
	}, nil
}

func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent(safeError(err)),
		},
	}
}

func safeError(err error) string {
	msg := err.Error()
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	return msg
}
