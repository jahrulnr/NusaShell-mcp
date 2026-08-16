package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Agent-readable MCP text receipts for the Terminal plugin.
// Port of terminal/mcp/agent-output.js.

func headerLines(fields map[string]any) string {
	order := []string{
		"ok", "exit_code", "signal", "shell", "shell_path", "cwd",
		"timed_out", "truncated", "duration_ms", "session_id", "exited",
		"ansi_stripped", "platform", "default", "count",
	}
	var lines []string
	seen := map[string]bool{}
	for _, k := range order {
		if v, ok := fields[k]; ok {
			lines = append(lines, fmt.Sprintf("%s=%v", k, v))
			seen[k] = true
		}
	}
	for k, v := range fields {
		if !seen[k] {
			lines = append(lines, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(lines, "\n")
}

func formatExecReceipt(result *ExecResult) string {
	fields := map[string]any{
		"exit_code": derefInt(result.ExitCode),
		"shell":     string(result.ShellKind),
		"timed_out": result.TimedOut,
		"truncated": result.Truncated,
	}
	if result.Signal != "" {
		fields["signal"] = result.Signal
	}
	if home, err := os.UserHomeDir(); err == nil && result.Cwd != home {
		fields["cwd"] = result.Cwd
	}
	return strings.Join([]string{
		headerLines(fields),
		"",
		"=== stdout ===",
		strings.TrimRight(result.Stdout, " \t\r\n"),
		"=== stderr ===",
		strings.TrimRight(result.Stderr, " \t\r\n"),
		"",
	}, "\n")
}

func derefInt(p *int) any {
	if p == nil {
		return ""
	}
	return *p
}

func formatShellsText(data map[string]any) string {
	var lines []string
	lines = append(lines, headerLines(map[string]any{
		"ok":       true,
		"platform": data["platform"],
		"default":  data["defaultKind"],
		"count":    len(data["shells"].([]any)),
	}))
	lines = append(lines, "", "kind\tpath\tsource")
	for _, sh := range data["shells"].([]any) {
		m := sh.(map[string]any)
		lines = append(lines, fmt.Sprintf("%v\t%v\t%v", m["kind"], m["path"], m["source"]))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func formatSessionOpenText(data map[string]any) string {
	return headerLines(map[string]any{
		"ok":         true,
		"session_id": data["sessionId"],
		"shell":      data["shellKind"],
		"shell_path": data["shell"],
		"cwd":        data["cwd"],
		"cols":       data["cols"],
		"rows":       data["rows"],
	}) + "\n"
}

func formatPtyReadText(data map[string]any) string {
	body := fmt.Sprintf("%v", data["stdout"])
	if ansi, _ := data["ansiStripped"].(bool); ansi {
		body = stripANSI(body)
	}
	return strings.Join([]string{
		headerLines(map[string]any{
			"ok":            true,
			"session_id":    data["sessionId"],
			"exited":        data["exited"],
			"exit_code":     data["exitCode"],
			"truncated":     data["truncated"],
			"ansi_stripped": data["ansiStripped"],
		}),
		"",
		"=== output ===",
		strings.TrimRight(body, " \t\r\n"),
		"",
	}, "\n")
}

func formatListSessionsText(items []map[string]any) string {
	var lines []string
	lines = append(lines, headerLines(map[string]any{
		"ok":    true,
		"count": len(items),
	}))
	lines = append(lines, "")
	lines = append(lines, "session_id\tshell\tcwd\texited")
	for _, s := range items {
		lines = append(lines, fmt.Sprintf("%v\t%v\t%v\t%v",
			s["sessionId"], s["shellKind"], s["cwd"], s["exited"]))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func itoa(v any) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(int(toIntOr(v, 0)))
}
