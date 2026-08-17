package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTools(s *server.MCPServer, mgr *SessionManager, pm *ProcessManager) {
	s.AddTool(mcp.NewTool("exec",
		mcp.WithDescription("Run a one-shot shell command and return stdout/stderr plus structured fields. cwd defaults to the user's home directory; pass an absolute cwd for a specific folder."),
		mcp.WithString("command", mcp.Required(), mcp.Description("Shell command to execute.")),
		mcp.WithString("cwd", mcp.Description("Absolute working directory (default: user home).")),
		mcp.WithNumber("timeoutMs", mcp.Description("Optional maximum time this MCP call waits. It does not limit process lifetime. If wait=false, the process is returned immediately and this value is ignored.")),
		mcp.WithBoolean("wait", mcp.Description("Wait for the command to finish and return its output. Defaults to true. Keep true for normal commands; set false only for long-running/background processes that you intend to inspect or manage later.")),
		mcp.WithBoolean("killOnTimeout", mcp.Description("If a wait timeout occurs, terminate the process. Default: false.")),
		mcp.WithString("shell", mcp.Description("Shell kind or absolute executable path. Kinds: auto, bash, zsh, pwsh, powershell, cmd, wsl.")),
	), handleExec(pm))

	s.AddTool(mcp.NewTool("shells",
		mcp.WithDescription("List shells available on this host (bash, zsh, pwsh, powershell, cmd, wsl) with resolved paths and the auto default."),
	), handleShells())

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
	s.AddTool(mcp.NewTool("process_read",
		mcp.WithDescription("Read buffered stdout/stderr from a long-running exec process without stopping it."),
		mcp.WithString("processId", mcp.Required()),
		mcp.WithBoolean("clear", mcp.Description("Clear buffered output after reading (default: true).")),
	), handleProcessRead(pm))

	s.AddTool(mcp.NewTool("process_wait",
		mcp.WithDescription("Wait for a long-running exec process. timeoutMs limits how long this MCP call waits; it does not kill the process."),
		mcp.WithString("processId", mcp.Required()),
		mcp.WithNumber("timeoutMs", mcp.Description("Optional maximum time to wait for this call.")),
	), handleProcessWait(pm))

	s.AddTool(mcp.NewTool("process_kill",
		mcp.WithDescription("Terminate a long-running exec process and its process group where supported."),
		mcp.WithString("processId", mcp.Required()),
	), handleProcessKill(pm))

	s.AddTool(mcp.NewTool("process_list",
		mcp.WithDescription("List long-running exec processes owned by this MCP server."),
	), handleProcessList(pm))

}

// --- Handlers ---

func handleExec(pm *ProcessManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		command, _ := args["command"].(string)
		cwd, _ := args["cwd"].(string)
		shell, _ := args["shell"].(string)
		wait := true
		if v, ok := args["wait"].(bool); ok {
			wait = v
		}
		timeoutMs := toIntOr(args["timeoutMs"], 0)
		killOnTimeout := false
		if v, ok := args["killOnTimeout"].(bool); ok {
			killOnTimeout = v
		}

		p, err := startProcess(command, cwd, shell)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		pm.Add(p)

		if !wait {
			return processJSON(p, false, false, false)
		}

		waited := p.wait(ctx, time.Duration(timeoutMs)*time.Millisecond)
		if !waited {
			if killOnTimeout {
				_ = p.kill()
			}
			return processJSON(p, true, killOnTimeout, true)
		}
		return processJSON(p, false, false, true)
	}
}

func processJSON(p *Process, waitTimedOut, killed, includeOutput bool) (*mcp.CallToolResult, error) {
	stdout, stderr, truncated, exited, exitCode, signal := p.snapshot(false)
	data := map[string]any{
		"processId": p.ID, "command": p.Command, "cwd": p.Cwd,
		"shell": p.Shell, "shellKind": p.ShellKind,
		"startedAt": p.StartedAt, "exited": exited, "exitCode": exitCode,
		"signal": signal, "stdout": stripANSI(stdout), "stderr": stripANSI(stderr),
		"truncated": truncated, "waitTimedOut": waitTimedOut,
		"timeoutIsNotProcessLifetime": true, "killed": killed,
	}
	return textJSON(data, formatProcessText(p.ID, exited, exitCode, waitTimedOut, stdout, stderr, includeOutput))
}

func formatProcessText(id string, exited bool, exitCode *int, waitTimedOut bool, stdout, stderr string, includeOutput bool) string {
	text := fmt.Sprintf("process_id=%s\nexited=%t\nexit_code=%v\nwait_timed_out=%t\n", id, exited, exitCode, waitTimedOut)
	if includeOutput {
		text += fmt.Sprintf("stdout:\n%s\nstderr:\n%s\n", stripANSI(stdout), stripANSI(stderr))
	}
	return text
}

func handleShells() server.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		exited, exitCode, _, _ := session.state()
		data := map[string]any{
			"stdout":       stdoutText,
			"stderr":       "",
			"exited":       exited,
			"exitCode":     exitCode,
			"truncated":    truncated,
			"sessionId":    session.ID,
			"ansiStripped": ansiStripped,
		}
		return textJSON(data, formatPtyReadText(map[string]any{
			"sessionId": session.ID, "exited": exited, "exitCode": exitCode,
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
		_, _, currentCols, currentRows := session.state()
		return textJSON(map[string]any{
			"ok": true, "sessionId": session.ID,
			"cols": currentCols, "rows": currentRows,
		}, fmt.Sprintf("ok=true\nsession_id=%s\ncols=%d\nrows=%d\n", session.ID, currentCols, currentRows))
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
			exited, exitCode, cols, rows := s.state()
			items = append(items, map[string]any{
				"sessionId": s.ID, "shell": s.Shell, "shellKind": s.ShellKind,
				"cwd": s.Cwd, "cols": cols, "rows": rows, "createdAt": s.CreatedAt,
				"exited": exited, "exitCode": exitCode,
			})
		}
		return textJSON(map[string]any{"sessions": items, "count": len(items)}, formatListSessionsText(items))
	}
}

func handleProcessRead(pm *ProcessManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["processId"].(string)
		clear := true
		if v, ok := req.GetArguments()["clear"].(bool); ok {
			clear = v
		}
		p, err := pm.Get(id)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		stdout, stderr, truncated, exited, exitCode, signal := p.snapshot(clear)
		data := map[string]any{"processId": id, "stdout": stripANSI(stdout), "stderr": stripANSI(stderr),
			"exited": exited, "exitCode": exitCode, "signal": signal, "truncated": truncated}
		return textJSON(data, fmt.Sprintf("process_id=%s\nexited=%t\nexit_code=%v\nstdout:\n%s\nstderr:\n%s\n", id, exited, exitCode, stripANSI(stdout), stripANSI(stderr)))
	}
}

func handleProcessWait(pm *ProcessManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["processId"].(string)
		timeoutMs := toIntOr(req.GetArguments()["timeoutMs"], 0)
		p, err := pm.Get(id)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		done := p.wait(ctx, time.Duration(timeoutMs)*time.Millisecond)
		return processJSON(p, !done, timeoutMs > 0 && !done, true)
	}
}

func handleProcessKill(pm *ProcessManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["processId"].(string)
		p, err := pm.Get(id)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if err := p.kill(); err != nil {
			return errorResult(err.Error()), nil
		}
		return processJSON(p, false, false, true)
	}
}

func handleProcessList(pm *ProcessManager) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		items := pm.List()
		out := make([]map[string]any, 0, len(items))
		for _, p := range items {
			_, _, _, exited, code, signal := p.snapshot(false)
			out = append(out, map[string]any{"processId": p.ID, "command": p.Command, "cwd": p.Cwd,
				"shell": p.Shell, "shellKind": p.ShellKind, "startedAt": p.StartedAt,
				"exited": exited, "exitCode": code, "signal": signal})
		}
		return textJSON(map[string]any{"processes": out, "count": len(out)}, "")
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
