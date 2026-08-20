package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Agent-readable text receipts for the Files plugin.
// Structured payloads stay typed for the UI; text is what the model sees.
// Port of files/mcp/agent-output.js.

func formatSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		if bytes < 10*1024 {
			return fmt.Sprintf("%.1fK", float64(bytes)/1024)
		}
		return fmt.Sprintf("%.0fK", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(bytes)/(1024*1024))
	}
}

func displayPath(value string) string {
	if value == "" {
		return "."
	}
	return value
}

// headerLines renders key=value pairs, skipping empty values.
func headerLines(fields map[string]any) string {
	// Preserve insertion order for stable output.
	order := []string{
		"ok", "path", "count", "depth", "lines", "bytes", "truncated",
		"pattern", "exists", "is_file", "is_dir", "written", "created",
		"moved", "copied", "deleted", "appended", "touched", "patched",
		"applied", "preview", "session_id", "exited", "exit_code",
		"ansi_stripped", "platform", "default", "shell", "shell_path",
		"cwd", "cols", "rows",
	}
	lines := []string{}
	for _, key := range order {
		if v, ok := fields[key]; ok {
			lines = append(lines, fmt.Sprintf("%s=%v", key, v))
		}
	}
	for key, v := range fields {
		if containsStr(order, key) {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s=%v", key, v))
	}
	return joinLines(lines)
}

func containsStr(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

func formatListText(result map[string]any) string {
	items, _ := result["items"].([]DirEntry)
	lines := []string{}
	lines = append(lines, headerLines(map[string]any{
		"ok":    true,
		"path":  displayPath(fmt.Sprintf("%v", result["path"])),
		"count": len(items),
	}))
	lines = append(lines, "")
	for _, item := range items {
		if item.IsDir {
			lines = append(lines, "d  "+item.Name+"/")
		} else {
			lines = append(lines, fmt.Sprintf("f  %s  %s  %s", item.Name, formatSize(item.Size), orDefault(item.Type, "file")))
		}
	}
	lines = append(lines, "")
	return joinLines(lines)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func appendTreeNodes(nodes []TreeNode, indent int, lines *[]string) {
	for _, node := range nodes {
		pad := ""
		for i := 0; i < indent; i++ {
			pad += "  "
		}
		if node.IsDir {
			*lines = append(*lines, pad+node.Name+"/")
			if len(node.Children) > 0 {
				appendTreeNodes(node.Children, indent+1, lines)
			}
		} else {
			*lines = append(*lines, pad+node.Name)
		}
	}
}

func formatTreeText(result map[string]any) string {
	tree, _ := result["tree"].([]TreeNode)
	lines := []string{
		headerLines(map[string]any{
			"ok":    true,
			"path":  displayPath(fmt.Sprintf("%v", result["path"])),
			"count": len(tree),
		}),
		"",
	}
	appendTreeNodes(tree, 0, &lines)
	lines = append(lines, "")
	return joinLines(lines)
}

func formatReadText(result map[string]any) string {
	lines := []string{
		headerLines(map[string]any{
			"ok":        true,
			"path":      fmt.Sprintf("%v", result["path"]),
			"lines":     result["totalLines"],
			"bytes":     result["totalBytes"],
			"truncated": result["truncated"],
		}),
		"",
		"=== content ===",
		strings.TrimRight(fmt.Sprintf("%v", result["content"]), " \t\r\n"),
		"",
	}
	return joinLines(lines)
}

// formatGrepText renders the agent-facing grep receipt honoring output_mode:
// content (default) shows path:line:content, files_with_matches shows only
// the file paths (never the matched lines — the schema promises paths), and
// count shows per-file match counts. Previously the formatter only handled
// the content shape, so files_with_matches returned no paths at all.
func formatGrepText(result map[string]any) string {
	meta, _ := result["meta"].(map[string]any)
	count := 0
	truncated := false
	if meta != nil {
		if c, ok := meta["count"].(int); ok {
			count = c
		} else if c, ok := meta["fileCount"].(int); ok {
			count = c
		}
		if t, ok := meta["truncated"].(bool); ok {
			truncated = t
		}
	}
	lines := []string{
		headerLines(map[string]any{
			"ok":        true,
			"path":      displayPath(fmt.Sprintf("%v", result["path"])),
			"pattern":   fmt.Sprintf("%v", result["pattern"]),
			"count":     count,
			"truncated": truncated,
		}),
		"",
	}
	switch {
	case result["files"] != nil:
		files, _ := result["files"].([]string)
		for _, f := range files {
			lines = append(lines, f)
		}
	case result["counts"] != nil:
		counts, _ := result["counts"].(map[string]int)
		paths := make([]string, 0, len(counts))
		for p := range counts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			lines = append(lines, fmt.Sprintf("%d\t%s", counts[p], p))
		}
	default:
		hits, _ := result["results"].([]GrepResult)
		for _, hit := range hits {
			lines = append(lines, fmt.Sprintf("%s:%d:%s", hit.Path, hit.Line, hit.Content))
		}
	}
	lines = append(lines, "")
	return joinLines(lines)
}

func formatSearchText(result map[string]any) string {
	hits, _ := result["results"].([]SearchResult)
	meta, _ := result["meta"].(map[string]any)
	count := 0
	truncated := false
	if meta != nil {
		if c, ok := meta["count"].(int); ok {
			count = c
		}
		if t, ok := meta["truncated"].(bool); ok {
			truncated = t
		}
	}
	lines := []string{
		headerLines(map[string]any{
			"ok":        true,
			"path":      displayPath(fmt.Sprintf("%v", result["path"])),
			"pattern":   fmt.Sprintf("%v", result["pattern"]),
			"count":     count,
			"truncated": truncated,
		}),
		"",
	}
	for _, hit := range hits {
		kind := "file"
		if hit.IsDir {
			kind = "dir "
		}
		lines = append(lines, kind+"  "+hit.Path)
	}
	lines = append(lines, "")
	return joinLines(lines)
}

func formatExistsText(result map[string]any) string {
	return headerLines(map[string]any{
		"ok":      true,
		"path":    fmt.Sprintf("%v", result["path"]),
		"exists":  result["exists"],
		"is_file": result["isFile"],
		"is_dir":  result["isDir"],
	}) + "\n"
}

func formatMutationText(result map[string]any) string {
	fields := map[string]any{"ok": true}
	for k, v := range result {
		switch v.(type) {
		case string, int, int64, float64, bool:
			fields[k] = v
		}
	}
	return headerLines(fields) + "\n"
}

func formatGenericText(result any) string {
	if result == nil {
		return "ok=true\n"
	}
	switch v := result.(type) {
	case map[string]any:
		// If the result carries an "error" key, do not add ok=true — the
		// agent must see a clear failure, not an ambiguous ok+error mix.
		if _, hasErr := v["error"]; hasErr {
			msg, _ := v["error"].(string)
			return "error=" + msg + "\n"
		}
		fields := map[string]any{"ok": true}
		var complex []string
		for k, val := range v {
			switch val.(type) {
			case string, bool, int, int64, float64:
				fields[k] = val
			default:
				complex = append(complex, k)
			}
		}
		lines := []string{headerLines(fields), ""}
		for _, k := range complex {
			lines = append(lines, "=== "+k+" ===")
			raw, err := json.MarshalIndent(v[k], "", "  ")
			if err != nil {
				lines = append(lines, fmt.Sprintf("%v", v[k]))
			} else {
				lines = append(lines, string(raw))
			}
			lines = append(lines, "")
		}
		return joinLines(lines)
	default:
		return fmt.Sprintf("ok=true\nvalue=%v\n", v)
	}
}

// formatFilesToolText picks the best text formatter for a Files tool result.
func formatFilesToolText(toolName string, result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return formatGenericText(result)
	}
	switch toolName {
	case "list":
		return formatListText(m)
	case "tree":
		return formatTreeText(m)
	case "read":
		return formatReadText(m)
	case "grep":
		return formatGrepText(m)
	case "search":
		return formatSearchText(m)
	case "exists":
		return formatExistsText(m)
	case "write", "mkdir", "move", "copy", "delete", "append", "touch", "info":
		return formatMutationText(m)
	default:
		return formatGenericText(result)
	}
}
