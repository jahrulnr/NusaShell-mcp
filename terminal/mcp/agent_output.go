package main

import (
	"fmt"
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
			if v == nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s=%v", k, v))
			seen[k] = true
		}
	}
	for k, v := range fields {
		if v == nil {
			continue
		}
		if !seen[k] {
			lines = append(lines, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(lines, "\n")
}

func formatShellsText(data map[string]any) string {
	shells, _ := data["shells"].([]ResolvedShell)
	var lines []string
	lines = append(lines, headerLines(map[string]any{
		"ok":       true,
		"platform": data["platform"],
		"default":  data["defaultKind"],
		"count":    len(shells),
	}))
	lines = append(lines, "", "kind\tpath\tsource")
	for _, sh := range shells {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", sh.Kind, sh.Path, sh.Source))
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
