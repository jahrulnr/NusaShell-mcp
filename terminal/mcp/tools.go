package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTools(s *server.MCPServer, mgr *SessionManager) {
	s.AddTool(mcp.NewTool("exec",
		mcp.WithDescription("Run a one-shot shell command and return stdout/stderr plus structured fields. cwd defaults to the user's home directory; pass an absolute cwd for a specific folder."),
		mcp.WithString("command", mcp.Required(), mcp.Description("Shell command to execute.")),
		mcp.WithString("cwd", mcp.Description("Absolute working directory (default: user home).")),
		mcp.WithNumber("timeoutMs", mcp.Description("Optional timeout in milliseconds before the command is killed.")),
		mcp.WithString("shell", mcp.Description("Shell kind or absolute executable path. Kinds: auto, bash, zsh, pwsh, powershell, cmd, wsl.")),
	), handleExec(mgr))

	s.AddTool(mcp.NewTool("shells",
		mcp.WithDescription("List shells available on this host (bash, zsh, pwsh, powershell, cmd, wsl) with resolved paths and the auto default."),
	), handleShells(mgr))

	s.AddTool(mcp.NewTool("open",
		mcp.WithDescription("Open a new interactive terminal session (PTY). cwd defaults to the user's home directory."),
		mcp.WithString("shell", mcp.Description("Shell kind or absolute executable path.")),
		mcp.WithString("cwd", mcp.Description("Absolute working directory (default: user home).")),
		mcp.WithNumber("cols", mcp.Description("Columns (default: 120)."), mcp.Min(1.0)),
		mcp.WithNumber("rows", mcp.Description("Rows (default: 30)."), mcp.Min(1.0)),
	), handleOpen(mgr))

	s.AddTool(mcp.NewTool("write",
		mcp.WithDescription("Write input to a terminal session."),
		mcp.WithString("sessionId", mcp.Required()),
		mcp.WithString("data", mcp.Required(), mcp.Description("Text to send to the terminal (include \\n to run a command).")),
	), handleWrite(mgr))

	s.AddTool(mcp.NewTool("read",
		mcp.WithDescription("Read buffered output from a terminal session. Agent text strips ANSI by default; structured stdout keeps raw PTY bytes for the UI."),
		mcp.WithString("sessionId", mcp.Required()),
		mcp.WithBoolean("clear", mcp.Description("Clear the buffer after reading (default: true).")),
		mcp.WithBoolean("stripAnsi", mcp.Description("Strip ANSI/OSC sequences in the agent text receipt (default: true).")),
	), handleRead(mgr))

	s.AddTool(mcp.NewTool("resize",
		mcp.WithDescription("Resize a terminal session."),
		mcp.WithString("sessionId", mcp.Required()),
		mcp.WithNumber("cols", mcp.Required(), mcp.Min(1.0)),
		mcp.WithNumber("rows", mcp.Required(), mcp.Min(1.0)),
	), handleResize(mgr))

	s.AddTool(mcp.NewTool("close",
		mcp.WithDescription("Close a terminal session."),
		mcp.WithString("sessionId", mcp.Required()),
	), handleClose(mgr))

	s.AddTool(mcp.NewTool("list",
		mcp.WithDescription("List active terminal sessions."),
	), handleList(mgr))
}

// --- Handlers ---

func handleExec(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		command, _ := args["command"].(string)
		cwd, _ := args["cwd"].(string)
		shell, _ := args["shell"].(string)
		timeoutMs, _ := args["timeoutMs"].(float64)

		if timeoutMs < 0 {
			timeoutMs = 0
		}
		// Apply a context deadline so a runaway command (no explicit timeoutMs)
		// cannot hold the request forever.
		eff := int64(timeoutMs)
		if eff <= 0 {
			eff = defaultExecTimeoutMs
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(eff)*time.Millisecond)
		defer cancel()

		result, err := RunExecWithContext(ctx, command, cwd, shell, int(eff))
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return textJSON(result, formatExecReceipt(result))
	}
}

func handleShells(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		data := ListAvailableShells()
		return textJSON(data, formatShellsText(data))
	}
}

func handleOpen(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		shell, _ := args["shell"].(string)
		cwd, _ := args["cwd"].(string)
		cols := toIntOr(args["cols"], 120)
		rows := toIntOr(args["rows"], 30)

		session, err := OpenSession(mgr, shell, cwd, cols, rows)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		data := map[string]any{
			"sessionId": session.ID,
			"shell":     session.Shell,
			"shellKind": session.ShellKind,
			"cwd":       session.Cwd,
			"cols":      session.Cols,
			"rows":      session.Rows,
		}
		return textJSON(data, formatSessionOpenText(data))
	}
}

func handleWrite(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		sessionID, _ := args["sessionId"].(string)
		data, _ := args["data"].(string)

		session, err := mgr.Get(sessionID)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if err := session.WriteInput(data); err != nil {
			return errorResult(err.Error()), nil
		}
		return textJSON(map[string]any{"ok": true, "sessionId": session.ID}, "ok=true\nsession_id="+session.ID+"\nwritten=true\n")
	}
}

func handleRead(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		sessionID, _ := args["sessionId"].(string)
		clear := true
		if v, ok := args["clear"].(bool); ok {
			clear = v
		}
		ansiStripped := true
		if v, ok := args["stripAnsi"].(bool); ok {
			ansiStripped = v
		}

		session, err := mgr.Get(sessionID)
		if err != nil {
			return errorResult(err.Error()), nil
		}

		stdout, truncated := session.drain(clear)
		stdoutText := stdout
		if ansiStripped {
			stdoutText = stripANSI(stdout)
		}

		data := map[string]any{
			"stdout":       stdoutText,
			"stderr":       "",
			"exited":       session.Exited,
			"exitCode":     session.ExitCode,
			"truncated":    truncated,
			"sessionId":    session.ID,
			"ansiStripped": ansiStripped,
		}
		return textJSON(data, formatPtyReadText(map[string]any{
			"sessionId": session.ID, "exited": session.Exited, "exitCode": session.ExitCode,
			"truncated": truncated, "stdout": stdout, "ansiStripped": ansiStripped,
		}))
	}
}

func handleResize(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		sessionID, _ := args["sessionId"].(string)
		cols := toIntOr(args["cols"], 120)
		rows := toIntOr(args["rows"], 30)

		session, err := mgr.Get(sessionID)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if err := session.Resize(cols, rows); err != nil {
			return errorResult(err.Error()), nil
		}
		return textJSON(map[string]any{
			"ok":        true,
			"sessionId": session.ID,
			"cols":      session.Cols,
			"rows":      session.Rows,
		}, fmt.Sprintf("ok=true\nsession_id=%s\ncols=%d\nrows=%d\n", session.ID, session.Cols, session.Rows))
	}
}

func handleClose(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID, _ := req.GetArguments()["sessionId"].(string)
		session, err := mgr.Get(sessionID)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		session.Close()
		mgr.Delete(sessionID)
		return textJSON(map[string]any{"ok": true, "sessionId": sessionID}, "ok=true\nsession_id="+sessionID+"\n")
	}
}

func handleList(mgr *SessionManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessions := mgr.List()
		items := make([]map[string]any, 0, len(sessions))
		for _, s := range sessions {
			items = append(items, map[string]any{
				"sessionId": s.ID,
				"shell":     s.Shell,
				"shellKind": s.ShellKind,
				"cwd":       s.Cwd,
				"cols":      s.Cols,
				"rows":      s.Rows,
				"createdAt": s.CreatedAt,
				"exited":    s.Exited,
				"exitCode":  s.ExitCode,
			})
		}
		return textJSON(map[string]any{"sessions": items, "count": len(items)}, formatListSessionsText(items))
	}
}

// --- helpers ---

func toIntOr(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return def
	}
}

func textJSON(data any, text string) (*mcp.CallToolResult, error) {
	if text == "" {
		raw, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal result: %w", err)
		}
		text = string(raw)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(text),
		},
		StructuredContent: data,
	}, nil
}

func jsonResult(data any) (*mcp.CallToolResult, error) {
	return textJSON(data, "")
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent(msg),
		},
	}
}
