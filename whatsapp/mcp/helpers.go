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

// normalizePhone converts a friendly phone-number string into E.164 form
// suitable for whatsmeow's PairPhone. It strips non-digits, then:
//   - if the result starts with one or more leading '0', drops them and
//     prepends the defaultCountryCode (so "0812..." → "62812..." for ID);
//   - if the result already starts with a country code (no leading 0) and
//     is long enough, returns it unchanged.
//
// Returns an error if the resulting string is too short (< 7 digits) — WA
// refuses numbers below that, and emitting a clean error here is friendlier
// than a cryptic whatsmeow.ErrPhoneNumberTooShort.
//
// The default country code can be overridden by passing a non-empty
// countryCode argument; pass "" to use the ID default.
func normalizePhone(input, countryCode string) (string, error) {
	if countryCode == "" {
		countryCode = "62" // default for Indonesian numbers
	}
	// Strip everything that isn't a digit.
	stripped := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c >= '0' && c <= '9' {
			stripped = append(stripped, c)
		}
	}
	digits := string(stripped)
	if digits == "" {
		return "", fmt.Errorf("phone number is empty after stripping non-digits")
	}
	// Drop leading zeros; if everything was zeros, leave one so the
	// length check below catches it.
	for len(digits) > 1 && digits[0] == '0' {
		digits = digits[1:]
	}
	// If after dropping zeros the number doesn't start with the country
	// code, prepend it. This handles the common "08123..." case. We bail
	// out early if the digit count is too low to ever form a valid E.164
	// after prepending — otherwise a 5-digit input would silently become a
	// 7-digit "62xxxxx" number, which the server would either reject with
	// a cryptic error or, worse, accept as a non-existent user.
	if !startsWithCountryCode(digits, countryCode) {
		if len(digits) < 7 {
			return "", fmt.Errorf("phone number has %d digits — too short to form a valid E.164 number", len(digits))
		}
		digits = countryCode + digits
	}
	// Final E.164 sanity check: 7-15 digits, leading digit non-zero.
	if len(digits) < 7 || len(digits) > 15 {
		return "", fmt.Errorf("phone number length %d is out of E.164 range (7-15 digits)", len(digits))
	}
	if digits[0] == '0' {
		return "", fmt.Errorf("phone number has a leading zero after normalization: %s", digits)
	}
	return digits, nil
}

// startsWithCountryCode reports whether digits already has the country
// code as a prefix. We compare the *digits* (not the full E.164 form) so
// that, e.g., a "62" country code matches "62812..." but not "162...".
func startsWithCountryCode(digits, countryCode string) bool {
	if len(digits) < len(countryCode) {
		return false
	}
	return digits[:len(countryCode)] == countryCode
}
