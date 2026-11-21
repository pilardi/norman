package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderMarkdownString renders a markdown string as styled terminal text using the default dark color scheme.
func renderMarkdownString(content string, maxWidth int) string {
	return renderMarkdownStringWithScheme(content, DefaultDarkColorScheme(), maxWidth)
}

// renderMarkdownStringWithScheme renders a markdown string as styled terminal text using the provided color scheme.
func renderMarkdownStringWithScheme(content string, cs *ColorScheme, maxWidth int) string {
	var lines []string
	renderMarkdownTextWithScheme(content, &lines, cs, maxWidth)
	return strings.Join(lines, "\n")
}

// renderMarkdown renders basic Markdown content as styled terminal text using the default dark color scheme.
func renderMarkdown(data *map[string]interface{}, maxWidth int) string {
	return renderMarkdownWithScheme(data, DefaultDarkColorScheme(), maxWidth)
}

// renderMarkdownWithScheme renders basic Markdown content as styled terminal text using the provided color scheme.
func renderMarkdownWithScheme(data *map[string]interface{}, cs *ColorScheme, maxWidth int) string {
	if data == nil || (*data)["content"] == nil {
		return "┌─ Markdown ─────────────────────────────────────────┐\n│  (no content)                                       │\n└─────────────────────────────────────────────────────┘"
	}

	content, ok := (*data)["content"].(string)
	if !ok {
		return "[invalid markdown content]"
	}

	var lines []string
	lines = append(lines, "┌─ Markdown ─────────────────────────────────────────┐")

	renderMarkdownTextWithScheme(content, &lines, cs, maxWidth)

	lines = append(lines, "└─────────────────────────────────────────────────────┘")
	return strings.Join(lines, "\n")
}

var (
	reHeader      = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	reBold        = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic      = regexp.MustCompile(`\*(.+?)\*`)
	reCodeInline  = regexp.MustCompile("`([^`]+)`")
	reListItem    = regexp.MustCompile(`^[-*+]\s+(.+)$`)
	reOrderedList = regexp.MustCompile(`^\d+\.\s+(.+)$`)
	reTable       = regexp.MustCompile(`\|(.+)\|`)
)

// renderMarkdownTextWithScheme processes a markdown string line by line using the provided color scheme.
func renderMarkdownTextWithScheme(content string, lines *[]string, cs *ColorScheme, maxWidth int) {
	styles := cs.ToLipglossStyles()
	linesSlice := strings.Split(content, "\n")

	inCodeBlock := false
	codeBlockContent := []string{}

	for _, line := range linesSlice {
		trimmedLine := strings.TrimSpace(line)

		// Check for code block start or end
		if strings.HasPrefix(trimmedLine, "```") {
			if !inCodeBlock {
				// Start of code block
				inCodeBlock = true
				codeBlockContent = []string{}
			} else {
				// End of code block
				inCodeBlock = false

				// Render code block
				codeBlockStr := strings.Join(codeBlockContent, "\n")

				// Style the code block using color scheme
				renderedCode := "  " + codeBlockStr
				*lines = append(*lines, styles["code-bg"].Render(renderedCode))
			}
			continue
		}

		if inCodeBlock {
			codeBlockContent = append(codeBlockContent, line)
			continue
		}

		if trimmedLine == "" {
			// Preserve empty lines for proper markdown spacing
			*lines = append(*lines, "")
			continue
		}

		// Headers
		if headerMatches := reHeader.FindStringSubmatch(trimmedLine); headerMatches != nil {
			level := len(headerMatches[1])
			var style lipgloss.Style
			switch level {
			case 1:
				style = styles["heading-1"]
			case 2:
				style = styles["heading-2"]
			case 3:
				style = styles["heading-3"]
			default:
				style = styles["heading-4-plus"]
			}
			wrappedLine := wrapStyledLine("  "+headerMatches[2], style, maxWidth)
			*lines = append(*lines, wrappedLine)
			continue
		}

		// Tables
		if tableMatches := reTable.FindAllStringSubmatch(trimmedLine, -1); len(tableMatches) > 0 {
			*lines = append(*lines, renderTableLine(tableMatches))
			continue
		}

		// List items
		if listMatches := reListItem.FindStringSubmatch(trimmedLine); listMatches != nil {
			wrappedLine := wrapStyledLine("  • "+renderInlineStyleWithScheme(listMatches[1], styles), lipgloss.NewStyle(), maxWidth)
			*lines = append(*lines, wrappedLine)
			continue
		}

		// Ordered list
		if olMatches := reOrderedList.FindStringSubmatch(trimmedLine); olMatches != nil {
			wrappedLine := wrapStyledLine("  "+olMatches[1], lipgloss.NewStyle(), maxWidth)
			*lines = append(*lines, wrappedLine)
			continue
		}

		// Regular text with inline formatting
		wrappedLine := wrapStyledLine("  "+renderInlineStyleWithScheme(trimmedLine, styles), lipgloss.NewStyle(), maxWidth)
		*lines = append(*lines, wrappedLine)
	}
}

// renderTableLine formats a table row with alignment.
func renderTableLine(matches [][]string) string {
	var cells []string
	for _, match := range matches {
		if len(match) >= 2 {
			cells = append(cells, strings.TrimSpace(match[1]))
		}
	}

	if len(cells) == 0 {
		return "  (empty table cell)"
	}

	// Simple: just pipe-separate cells with padding
	maxCellLen := 0
	for _, c := range cells {
		if len(c) > maxCellLen {
			maxCellLen = len(c)
		}
	}

	var parts []string
	for _, c := range cells {
		parts = append(parts, fmt.Sprintf("%-*s", maxCellLen+2, c))
	}
	return "  │" + strings.Join(parts, "│") + "│"
}

// renderInlineStyleWithScheme applies bold, italic, and code formatting to a string using the provided styles.
func renderInlineStyleWithScheme(s string, styles map[string]lipgloss.Style) string {
	// Code inline
	s = reCodeInline.ReplaceAllStringFunc(s, func(match string) string {
		code := strings.TrimPrefix(match, "`")
		code = strings.TrimSuffix(code, "`")
		return styles["inline-code"].Render("`" + code + "`")
	})

	// Bold
	s = reBold.ReplaceAllStringFunc(s, func(match string) string {
		return lipgloss.NewStyle().Bold(true).Render(strings.TrimSuffix(strings.TrimPrefix(match, "**"), "**"))
	})

	// Italic
	s = reItalic.ReplaceAllStringFunc(s, func(match string) string {
		return lipgloss.NewStyle().Italic(true).Render(strings.TrimSuffix(strings.TrimPrefix(match, "*"), "*"))
	})

	return s
}

// wrapStyledLine wraps a styled line (with lipgloss ANSI codes) to fit within maxWidth characters,
// breaking on word boundaries while preserving ANSI styling.
// Long words (e.g., hyphenated UUIDs or workspace IDs) are not broken and are allowed to overflow
// or be placed on a new line as a whole unit.
func wrapStyledLine(s string, style lipgloss.Style, maxWidth int) string {
	if maxWidth <= 0 || len(s) == 0 {
		return s
	}

	// Process line by line to preserve newlines
	lines := strings.Split(s, "\n")
	var result strings.Builder

	for i, line := range lines {
		if i > 0 {
			result.WriteByte('\n')
		}

		// Wrap this line using words
		words := strings.Fields(line)
		var lineBuilder strings.Builder
		var currentLineVisibleWidth int

		for _, w := range words {
			// Calculate visible width of the word (excluding ANSI codes)
			wordVisibleWidth := lipgloss.Width(w)
			spaceNeeded := 0
			if lineBuilder.Len() > 0 {
				spaceNeeded = 1 // space before word
			}

			// Check if adding this word would exceed maxWidth
			if currentLineVisibleWidth+spaceNeeded+wordVisibleWidth > maxWidth && lineBuilder.Len() > 0 {
				// Check if the word itself is longer than or equal to maxWidth
				if wordVisibleWidth >= maxWidth {
					// Word is too long to fit on a single line with other content, render the current line first
					if lineBuilder.Len() > 0 {
						renderedLine := style.Render(lineBuilder.String())
						result.WriteString(renderedLine)
						result.WriteByte('\n')
						lineBuilder = strings.Builder{}
						currentLineVisibleWidth = 0
					}
					// Render the long word as-is without breaking it
					renderedWord := style.Render(w)
					result.WriteString(renderedWord)
					// Skip adding to lineBuilder since we already rendered it
					continue
				} else {
					// Render the current line with style and add to result
					renderedLine := style.Render(lineBuilder.String())
					result.WriteString(renderedLine)
					result.WriteByte('\n')
					lineBuilder = strings.Builder{}
					currentLineVisibleWidth = 0
				}
			}

			// Add space before word if not the first word on the line
			if lineBuilder.Len() > 0 {
				lineBuilder.WriteByte(' ')
				currentLineVisibleWidth += 1
			}
			lineBuilder.WriteString(w)
			currentLineVisibleWidth += wordVisibleWidth
		}
		if lineBuilder.Len() > 0 {
			renderedLine := style.Render(lineBuilder.String())
			result.WriteString(renderedLine)
		}
	}

	return result.String()
}
