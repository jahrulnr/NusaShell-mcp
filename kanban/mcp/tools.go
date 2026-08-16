package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTools(s *server.MCPServer, store *Store) {
	// Projects
	s.AddTool(
		mcp.NewTool("list_projects",
			mcp.WithDescription("List all Kanban projects."),
		),
		handleListProjects(store),
	)
	s.AddTool(
		mcp.NewTool("create_project",
			mcp.WithDescription("Create a Kanban project with the default workflow columns."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Project name."),
				mcp.MinLength(1),
				mcp.MaxLength(100),
			),
		),
		handleCreateProject(store),
	)
	s.AddTool(
		mcp.NewTool("list_columns",
			mcp.WithDescription("List workflow columns for a project. Always use this before move_ticket."),
			mcp.WithString("project_id",
				mcp.Required(),
				mcp.Description("Project ID."),
				mcp.MinLength(1),
			),
		),
		handleListColumns(store),
	)

	// Tickets
	s.AddTool(
		mcp.NewTool("create_ticket",
			mcp.WithDescription("Create a story ticket. Provide acceptance criteria in the description."),
			mcp.WithString("title",
				mcp.Required(),
				mcp.Description("Ticket title."),
				mcp.MinLength(1),
				mcp.MaxLength(200),
			),
			mcp.WithString("description",
				mcp.Description("Ticket description / acceptance criteria."),
				mcp.MaxLength(20000),
			),
			mcp.WithString("project_id",
				mcp.Description("Project ID. Defaults to the first project."),
			),
			mcp.WithString("session_id",
				mcp.Description("Agent session ID for filtering."),
			),
			mcp.WithString("priority",
				mcp.Description("Priority level."),
				mcp.Enum("urgent", "high", "medium", "low"),
			),
			mcp.WithString("column_id",
				mcp.Description("Column ID. Defaults to Backlog."),
			),
		),
		handleCreateTicket(store),
	)
	s.AddTool(
		mcp.NewTool("update_ticket",
			mcp.WithDescription("Update ticket title, description, priority, or session."),
			mcp.WithString("ticket_id",
				mcp.Required(),
				mcp.Description("Ticket ID."),
			),
			mcp.WithString("title",
				mcp.Description("New title."),
				mcp.MinLength(1),
				mcp.MaxLength(200),
			),
			mcp.WithString("description",
				mcp.Description("New description."),
				mcp.MaxLength(20000),
			),
			mcp.WithString("priority",
				mcp.Description("New priority."),
				mcp.Enum("urgent", "high", "medium", "low"),
			),
			mcp.WithString("session_id",
				mcp.Description("New session ID. Pass empty string to unlink."),
			),
		),
		handleUpdateTicket(store),
	)
	s.AddTool(
		mcp.NewTool("move_ticket",
			mcp.WithDescription("Move a ticket to a workflow column. Use list_columns first."),
			mcp.WithString("ticket_id",
				mcp.Required(),
			),
			mcp.WithString("column_id",
				mcp.Required(),
			),
			mcp.WithNumber("order",
				mcp.Description("Position within the column. Defaults to end."),
				mcp.Min(0.0),
				mcp.Max(100000.0),
			),
		),
		handleMoveTicket(store),
	)
	s.AddTool(
		mcp.NewTool("delete_ticket",
			mcp.WithDescription("Delete a ticket and its subtasks."),
			mcp.WithString("ticket_id", mcp.Required()),
		),
		handleDeleteTicket(store),
	)
	s.AddTool(
		mcp.NewTool("create_subtask",
			mcp.WithDescription("Create an actionable subtask under a story."),
			mcp.WithString("parent_ticket_id", mcp.Required()),
			mcp.WithString("title",
				mcp.Required(),
				mcp.MinLength(1),
				mcp.MaxLength(200),
			),
			mcp.WithString("description",
				mcp.MaxLength(20000),
			),
			mcp.WithString("priority",
				mcp.Enum("urgent", "high", "medium", "low"),
			),
		),
		handleCreateSubtask(store),
	)
	s.AddTool(
		mcp.NewTool("complete_subtask",
			mcp.WithDescription("Move a subtask to its project's Done column and report parent progress."),
			mcp.WithString("ticket_id", mcp.Required()),
		),
		handleCompleteSubtask(store),
	)
	s.AddTool(
		mcp.NewTool("list_tickets",
			mcp.WithDescription("List tickets, optionally filtered by project, column, session, or parent."),
			mcp.WithString("project_id"),
			mcp.WithString("column_id"),
			mcp.WithString("session_id"),
			mcp.WithString("parent_ticket_id",
				mcp.Description("Parent ticket ID. Pass empty string for top-level tickets only."),
			),
		),
		handleListTickets(store),
	)
	s.AddTool(
		mcp.NewTool("get_ticket",
			mcp.WithDescription("Get a ticket with its subtasks and progress."),
			mcp.WithString("ticket_id", mcp.Required()),
		),
		handleGetTicket(store),
	)

	// Sessions
	s.AddTool(
		mcp.NewTool("create_session",
			mcp.WithDescription("Create an agent session used to filter board work."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.MinLength(1),
				mcp.MaxLength(100),
			),
			mcp.WithString("color",
				mcp.Description("Hex color (e.g. #3B82F6). Auto-assigned if omitted."),
			),
		),
		handleCreateSession(store),
	)
	s.AddTool(
		mcp.NewTool("delete_session",
			mcp.WithDescription("Delete a session while preserving and unlinking its tickets."),
			mcp.WithString("session_id", mcp.Required()),
		),
		handleDeleteSession(store),
	)
	s.AddTool(
		mcp.NewTool("list_sessions",
			mcp.WithDescription("List all agent sessions."),
		),
		handleListSessions(store),
	)
}

// --- Handlers ---

func handleListProjects(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return jsonResult(store.ListProjects())
	}
}

func handleCreateProject(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, _ := req.GetArguments()["name"].(string)
		if name == "" {
			return errorResult("name is required"), nil
		}
		return jsonResult(store.CreateProject(name))
	}
}

func handleListColumns(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectID, _ := req.GetArguments()["project_id"].(string)
		if projectID == "" {
			return errorResult("project_id is required"), nil
		}
		return jsonResult(store.GetColumns(projectID))
	}
}

func handleCreateTicket(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		title, _ := args["title"].(string)
		if title == "" {
			return errorResult("title is required"), nil
		}

		projectID, _ := args["project_id"].(string)
		if projectID == "" {
			p := store.GetOrCreateDefaultProject()
			projectID = p.ID
		}

		input := CreateTicketInput{
			Title:    title,
			ProjectID: projectID,
		}
		if desc, ok := args["description"].(string); ok && desc != "" {
			input.Description = &desc
		}
		if sessID, ok := args["session_id"].(string); ok && sessID != "" {
			input.SessionID = &sessID
		}
		if pri, ok := args["priority"].(string); ok && pri != "" {
			p := Priority(pri)
			input.Priority = &p
		}
		if colID, ok := args["column_id"].(string); ok && colID != "" {
			input.ColumnID = &colID
		}

		ticket, err := store.CreateTicket(input)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(ticket)
	}
}

func handleUpdateTicket(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		ticketID, _ := args["ticket_id"].(string)
		if ticketID == "" {
			return errorResult("ticket_id is required"), nil
		}

		var updates UpdateTicketInput
		if title, ok := args["title"].(string); ok && title != "" {
			updates.Title = &title
		}
		if desc, exists := args["description"]; exists {
			if s, ok := desc.(string); ok {
				updates.Description = &s
			}
		}
		if pri, ok := args["priority"].(string); ok && pri != "" {
			p := Priority(pri)
			updates.Priority = &p
		}
		if sessID, exists := args["session_id"]; exists {
			if s, ok := sessID.(string); ok {
				if s == "" {
					updates.SessionID = &s
				} else {
					updates.SessionID = &s
				}
			}
		}

		ticket := store.UpdateTicket(ticketID, updates)
		if ticket == nil {
			return errorResult("Ticket not found"), nil
		}
		return jsonResult(*ticket)
	}
}

func handleMoveTicket(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		ticketID, _ := args["ticket_id"].(string)
		columnID, _ := args["column_id"].(string)
		if ticketID == "" || columnID == "" {
			return errorResult("ticket_id and column_id are required"), nil
		}

		var order *int
		if o, ok := toInt(args["order"]); ok {
			order = &o
		}

		ticket := store.MoveTicket(ticketID, columnID, order)
		if ticket == nil {
			return errorResult("Ticket not found"), nil
		}
		return jsonResult(*ticket)
	}
}

func handleDeleteTicket(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ticketID, _ := req.GetArguments()["ticket_id"].(string)
		if ticketID == "" {
			return errorResult("ticket_id is required"), nil
		}
		return jsonResult(map[string]bool{"deleted": store.DeleteTicket(ticketID)})
	}
}

func handleCreateSubtask(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		parentID, _ := args["parent_ticket_id"].(string)
		title, _ := args["title"].(string)
		if parentID == "" || title == "" {
			return errorResult("parent_ticket_id and title are required"), nil
		}

		var desc *string
		if d, ok := args["description"].(string); ok && d != "" {
			desc = &d
		}
		var pri *Priority
		if p, ok := args["priority"].(string); ok && p != "" {
			pr := Priority(p)
			pri = &pr
		}

		ticket, err := store.CreateSubtask(parentID, title, desc, pri)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(ticket)
	}
}

func handleCompleteSubtask(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ticketID, _ := req.GetArguments()["ticket_id"].(string)
		if ticketID == "" {
			return errorResult("ticket_id is required"), nil
		}

		ticket := store.GetTicket(ticketID)
		if ticket == nil {
			return errorResult("Ticket not found"), nil
		}

		done := store.GetDoneColumn(ticket.ProjectID)
		if done == nil {
			return errorResult("Done column not found"), nil
		}

		moved := store.MoveTicket(ticketID, done.ID, nil)
		var progress *StoryProgress
		if ticket.ParentTicketID != nil {
			p := store.GetStoryProgress(*ticket.ParentTicketID)
			progress = &p
		}

		result := map[string]any{"ticket": moved}
		if progress != nil {
			result["progress"] = progress
		}
		return jsonResult(result)
	}
}

func handleListTickets(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		filters := TicketFilters{
			ProjectID: getString(args, "project_id"),
			ColumnID:  getString(args, "column_id"),
			SessionID: getString(args, "session_id"),
		}
		if pID, exists := args["parent_ticket_id"]; exists {
			if s, ok := pID.(string); ok {
				filters.ParentTicketID = &s
			}
		}
		return jsonResult(store.ListTickets(filters))
	}
}

func handleGetTicket(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ticketID, _ := req.GetArguments()["ticket_id"].(string)
		if ticketID == "" {
			return errorResult("ticket_id is required"), nil
		}
		ticket := store.GetTicketWithSubtasks(ticketID)
		if ticket == nil {
			return errorResult("Ticket not found"), nil
		}
		return jsonResult(ticket)
	}
}

func handleCreateSession(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, _ := req.GetArguments()["name"].(string)
		if name == "" {
			return errorResult("name is required"), nil
		}
		var color *string
		if c, ok := req.GetArguments()["color"].(string); ok && c != "" {
			color = &c
		}
		return jsonResult(store.CreateSession(name, color))
	}
}

func handleDeleteSession(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID, _ := req.GetArguments()["session_id"].(string)
		if sessionID == "" {
			return errorResult("session_id is required"), nil
		}
		if !store.DeleteSession(sessionID) {
			return errorResult("Session not found"), nil
		}
		return jsonResult(map[string]bool{"deleted": true})
	}
}

func handleListSessions(store *Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return jsonResult(store.ListSessions())
	}
}

// --- helpers ---

func getString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
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

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent(msg),
		},
	}
}
