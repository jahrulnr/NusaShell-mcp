package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Telegram Bot API parse_mode=HTML supports a small whitelist of tags:
// <b>, <i>, <u>, <s>, <code>, <pre>, <a href="...">, <blockquote>,
// <span class="tg-spoiler">. There are no header or list tags, so headers are
// rendered as <b> and lists as bullet characters. Only &, <, > need escaping
// in text; Telegram rejects unsupported tags, so producing valid, escaped HTML
// is both a correctness and a safety concern (the same strings may be rendered
// in the NusaShell UI).

// sanitizeForTelegram escapes the HTML special characters &, <, > so a plain
// text fragment can be safely embedded in a parse_mode=HTML message (or shown
// in a web UI) without injecting markup.
func sanitizeForTelegram(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

// tgCodeHolder keeps fenced and inline code blocks out of the regex passes so
// their contents are not mangled by the markdown → HTML transformations.
type tgCodeHolder struct {
	text  string   // text with placeholders substituted in
	codes []string // raw code contents, in placeholder order
}

var tgFencedRe = regexp.MustCompile("(?s)```[ \\t]*[\\w]*\\n?(.*?)```")
var tgInlineCodeRe = regexp.MustCompile("`([^`]+)`")

// tgExtractCode pulls fenced then inline code out of text, replacing each with
// a \x00CB{n}\x00 / \x00IC{n}\x00 placeholder. Contents are returned raw; the
// caller escapes them when restoring.
func tgExtractCode(text string) tgCodeHolder {
	var codes []string

	text = tgFencedRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := tgFencedRe.FindStringSubmatch(m)
		content := ""
		if len(sub) > 1 {
			content = strings.TrimRight(sub[1], "\n")
		}
		placeholder := fmt.Sprintf("\x00CB%d\x00", len(codes))
		codes = append(codes, content)
		return placeholder
	})

	text = tgInlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := tgInlineCodeRe.FindStringSubmatch(m)
		content := ""
		if len(sub) > 1 {
			content = sub[1]
		}
		placeholder := fmt.Sprintf("\x00IC%d\x00", len(codes))
		codes = append(codes, content)
		return placeholder
	})

	return tgCodeHolder{text: text, codes: codes}
}

// markdownToHTML converts Markdown-formatted LLM output to Telegram HTML
// (parse_mode=HTML). Supported: fenced/inline code, bold, italic, strikethrough,
// links, headers (→ <b>), blockquotes, and bullet/numbered lists. All non-code
// text is HTML-escaped, so the output is safe from injection in both Telegram
// and the NusaShell UI.
func markdownToHTML(text string) string {
	if text == "" {
		return ""
	}

	holder := tgExtractCode(text)
	text = holder.text

	// Blockquotes: group consecutive "> ..." lines into numbered placeholders
	// HERE — before HTML escaping — because the raw ">" marker would survive
	// as "&gt;" and never be detected afterwards. Placeholders are NUL-tagged,
	// untouched by sanitizeForTelegram, and restored as real tags post-escape.
	text = groupBlockquotes(text)

	// Escape HTML special chars in the prose. Code contents are escaped
	// separately when restored.
	text = sanitizeForTelegram(text)

	// Headers (##, ###, etc.) → <b>text</b> (Telegram has no header concept).
	text = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`).ReplaceAllString(text, "<b>$1</b>")

	// Links [text](url) → <a href="url">text</a>. The URL has already been
	// HTML-escaped by sanitize above (& → &amp;); only quotes still need
	// escaping so the double-quoted attribute stays valid.
	text = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllStringFunc(text, func(m string) string {
		sub := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		label, url := sub[1], sub[2]
		url = strings.ReplaceAll(url, `"`, "&quot;")
		return fmt.Sprintf(`<a href="%s">%s</a>`, url, label)
	})

	// Bold: **text** or __text__ → <b>text</b>
	text = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(text, "<b>$1</b>")
	text = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(text, "<b>$1</b>")

	// Strikethrough: ~~text~~ → <s>text</s>
	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "<s>$1</s>")

	// Italic: _text_ → <i>text</i>. Run after bold/underscore-bold so __ is
	// consumed first. Keep it non-greedy and avoid matching across newlines.
	text = regexp.MustCompile(`(?s)_([^_\n]+?)_`).ReplaceAllString(text, "<i>$1</i>")

	// List items: leading "- ", "* ", or "N. " → bullet "• ".
	text = regexp.MustCompile(`(?m)^[-*]\s+`).ReplaceAllString(text, "• ")
	text = regexp.MustCompile(`(?m)^\d+\.\s+`).ReplaceAllString(text, "• ")

	// Restore code blocks and inline code, escaping their contents. Each
	// placeholder is kind-tagged: \x00CB{n}\x00 for fenced, \x00IC{n}\x00 for
	// inline, so we re-derive the kind from which placeholder is present.
	for i, code := range holder.codes {
		esc := sanitizeForTelegram(code)
		fencedPlaceholder := fmt.Sprintf("\x00CB%d\x00", i)
		inlinePlaceholder := fmt.Sprintf("\x00IC%d\x00", i)
		switch {
		case strings.Contains(text, fencedPlaceholder):
			text = strings.ReplaceAll(text, fencedPlaceholder, "<pre><code>"+esc+"</code></pre>")
		case strings.Contains(text, inlinePlaceholder):
			text = strings.ReplaceAll(text, inlinePlaceholder, "<code>"+esc+"</code>")
		}
	}

	// Restore blockquote markers into real tags (post-escape).
	text = regexp.MustCompile(`\x00BQ\d+\x00`).ReplaceAllString(text, "<blockquote>")
	text = regexp.MustCompile(`\x00BE\d+\x00`).ReplaceAllString(text, "</blockquote>")

	// Collapse 3+ blank lines to 2.
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

// groupBlockquotes wraps runs of lines starting with "> " in numbered
// placeholders (\x00BQ{n}\x00…\x00BE{n}\x00), stripping the marker. The
// callers restore the placeholders into <blockquote> tags after HTML escaping.
func groupBlockquotes(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	inQuote := false
	n := 0
	for _, ln := range lines {
		isQuote := strings.HasPrefix(ln, ">")
		if isQuote {
			if !inQuote {
				out = append(out, fmt.Sprintf("\x00BQ%d\x00", n))
				n++
				inQuote = true
			}
			out = append(out, strings.TrimPrefix(ln, "> "))
		} else {
			if inQuote {
				out = append(out, fmt.Sprintf("\x00BE%d\x00", n-1))
				inQuote = false
			}
			out = append(out, ln)
		}
	}
	if inQuote {
		out = append(out, fmt.Sprintf("\x00BE%d\x00", n-1))
	}
	return strings.Join(out, "\n")
}

// chunkText splits text into pieces that fit within maxLen code points,
// preferring to split at paragraph (\n\n), then line (\n), then space
// boundaries. Telegram's sendMessage cap is 4096 code points (not bytes), so
// chunking is rune-aware to avoid splitting a multi-byte rune or overflowing
// the server-side limit.
func chunkText(text string, maxLen int) []string {
	if maxLen <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}
		window := string(runes[:maxLen])
		cutAt := maxLen
		if idx := strings.LastIndex(window, "\n\n"); idx > 0 {
			cutAt = idx
		} else if idx := strings.LastIndex(window, "\n"); idx > 0 {
			cutAt = idx
		} else if idx := strings.LastIndex(window, " "); idx > 0 {
			cutAt = idx
		}
		chunks = append(chunks, strings.TrimRight(string(runes[:cutAt]), " \n"))
		runes = []rune(strings.TrimLeft(string(runes[cutAt:]), " \n"))
	}
	return chunks
}

// formatMessage renders a MessageRow as a single display line for the UI and
// for tool results that summarize a message. The text is HTML-escaped so it is
// safe to render in the web UI; no Telegram parse_mode is implied here.
//
// MessageRow fields (defined in store.go): ID string, ChatID string,
// SenderName string, Text string, Timestamp int64, FromMe bool, EditedAt *int64.
func formatMessage(row MessageRow) string {
	who := row.SenderName
	if who == "" {
		who = row.ID
	}
	if who == "" {
		who = "?"
	}
	if row.FromMe {
		who = "me"
	}

	prefix := parseTime(row.Timestamp)
	if row.EditedAt != nil {
		prefix += " (edited)"
	}

	body := strings.ReplaceAll(row.Text, "\n", " ")
	body = strings.TrimSpace(body)
	if body == "" {
		body = "(no text)"
	}
	body = sanitizeForTelegram(body)

	return fmt.Sprintf("%s %s: %s", prefix, who, body)
}
