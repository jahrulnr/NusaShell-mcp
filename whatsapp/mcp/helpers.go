package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// jsonResult wraps data as a JSON MCP tool result with both text content
// and structured content.
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

// errorResult wraps an error as an MCP error result with a sanitized message.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.NewTextContent(safeErrorMessage(err)),
		},
	}
}

// safeErrorMessage sanitizes an error for output: strips control chars,
// truncates to 1000 chars, and returns a generic message for nil/empty.
func safeErrorMessage(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := err.Error()
	if msg == "" {
		return "unknown error"
	}
	// Strip control characters.
	out := make([]rune, 0, len(msg))
	for _, r := range msg {
		if r < 0x20 || r == 0x7f {
			out = append(out, ' ')
		} else {
			out = append(out, r)
		}
	}
	msg = string(out)
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	return msg
}

// isRetryable returns true if the error indicates a transient failure that
// the LLM can retry (not connected, network, rate-limit).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotConnected) {
		return true
	}
	msg := err.Error()
	for _, sub := range []string{"timeout", "connection reset", "EOF", "rate limit", "temporarily unavailable"} {
		if containsCI(msg, sub) {
			return true
		}
	}
	return false
}

func containsCI(s, sub string) bool {
	return indexOfCI(s, sub) >= 0
}

func indexOfCI(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// argString extracts a string argument from the tool call args map.
func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// argInt extracts an int argument from the tool call args map.
func argInt(args map[string]any, key string, def int) int {
	switch n := args[key].(type) {
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

// argBool extracts a bool argument from the tool call args map.
func argBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}
