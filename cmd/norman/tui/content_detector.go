package tui

import (
	"encoding/json"
	"strings"

	"github.com/norman/norman/pkg/protocol"
)

// detectContentType returns a high-level content type and matching render hint from raw text.
func detectContentType(s string) (contentType string, renderHint protocol.RichOutputRenderHint) {
	trimmed := strings.TrimSpace(s)

	// Check for JSON first (most specific structured format).
	if isJSON(trimmed) {
		return "json", protocol.Json
	}

	// Check for HTML.
	if hasHTMLTags(trimmed) {
		return "html", protocol.Html
	}

	// Check for markdown (headers, code blocks, lists).
	if hasMarkdown(trimmed) {
		return "markdown", protocol.Markdown
	}

	// Default to text.
	return "text", protocol.Text
}

func isJSON(s string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(s), &js) == nil
}

func hasHTMLTags(s string) bool {
	return strings.Contains(s, "<") && strings.Contains(s, ">")
}

func hasMarkdown(s string) bool {
	// Detect common markdown patterns: headings (#), code blocks (```), lists (-/1.), bold (**), inline code (`).
	// Check for code blocks first
	if strings.Contains(s, "```") {
		return true
	}
	// Check for headings (any # followed by space or end of line)
	if strings.Contains(s, "# ") || strings.Contains(s, "## ") || strings.Contains(s, "### ") ||
		strings.Contains(s, "#### ") || strings.Contains(s, "##### ") || strings.Contains(s, "###### ") {
		return true
	}
	// Check for lists ( - or * or number at start of line or after newline)
	if strings.Contains(s, "\n- ") || strings.Contains(s, "\n* ") || strings.Contains(s, "\n1. ") ||
		strings.Contains(s, "\n2. ") || strings.Contains(s, "\n3. ") || strings.Contains(s, "\n4. ") ||
		strings.Contains(s, "\n5. ") || strings.Contains(s, "\n6. ") || strings.Contains(s, "\n7. ") ||
		strings.Contains(s, "\n8. ") || strings.Contains(s, "\n9. ") || strings.Contains(s, "\n10. ") {
		return true
	}
	// Check for bold/italic
	if strings.Contains(s, "**") || strings.Contains(s, "*") || strings.Contains(s, "__") {
		return true
	}
	// Check for inline code
	if strings.Contains(s, "`") {
		return true
	}
	return false
}

// isProgressMessage checks if a message is a progress/event message rather than streaming text.
func isProgressMessage(msg string) bool {
	// Progress messages typically start with specific phrases
	lowerMsg := strings.ToLower(msg)
	return strings.HasPrefix(lowerMsg, "executing task...") ||
		strings.HasPrefix(lowerMsg, "llm turn") ||
		strings.HasPrefix(lowerMsg, "executing tool:") ||
		strings.HasPrefix(lowerMsg, "tool '") ||
		strings.HasPrefix(lowerMsg, "tool completed:") ||
		strings.HasPrefix(lowerMsg, "tool '") && strings.Contains(lowerMsg, "completed:") ||
		strings.Contains(lowerMsg, "item.started") ||
		strings.Contains(lowerMsg, "item.completed") ||
		strings.Contains(lowerMsg, "turn completed") ||
		strings.Contains(lowerMsg, "turn failed")
}
