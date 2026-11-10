package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderMarkdown renders basic Markdown content as styled terminal text.
func renderMarkdown(data *map[string]interface{}) string {
	if data == nil || (*data)["content"] == nil {
		return "┌─ Markdown ─────────────────────────────────────────┐\n│  (no content)                                       │\n└─────────────────────────────────────────────────────┘"
	}

	content, ok := (*data)["content"].(string)
	if !ok {
		return "[invalid markdown content]"
	}

	var lines []string
	lines = append(lines, "┌─ Markdown ─────────────────────────────────────────┐")

	renderMarkdownText(content, &lines)

	lines = append(lines, "└─────────────────────────────────────────────────────┘")
	return strings.Join(lines, "\n")
}

var (
	reHeader      = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	reBold        = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic      = regexp.MustCompile(`\*(.+?)\*`)
	reCodeInline  = regexp.MustCompile("`([^`]+)`")
	reCodeBlock   = regexp.MustCompile("```([\\s\\S]*?)```")
	reListItem    = regexp.MustCompile(`^[-*+]\s+(.+)$`)
	reOrderedList = regexp.MustCompile(`^\d+\.\s+(.+)$`)
	reTable       = regexp.MustCompile(`\|(.+)\|`)
)

// renderMarkdownText processes a markdown string line by line.
func renderMarkdownText(content string, lines *[]string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Code block
		if codeBlockMatches := reCodeBlock.FindStringSubmatch(line); codeBlockMatches != nil {
			*lines = append(*lines, lipgloss.NewStyle().Background(lipgloss.Color("#1e1e2e")).Render("  "+strings.TrimSpace(codeBlockMatches[1])))
			continue
		}

		// Headers
		if headerMatches := reHeader.FindStringSubmatch(line); headerMatches != nil {
			level := len(headerMatches[1])
			style := lipgloss.NewStyle().Bold(true)
			switch level {
			case 1:
				style = style.Foreground(lipgloss.Color("#ff6b6b"))
			case 2:
				style = style.Foreground(lipgloss.Color("#ffa94d"))
			case 3:
				style = style.Foreground(lipgloss.Color("#ffd43b"))
			default:
				style = style.Foreground(lipgloss.Color("#8ec07c"))
			}
			*lines = append(*lines, "  "+style.Render(headerMatches[2]))
			continue
		}

		// Tables
		if tableMatches := reTable.FindAllStringSubmatch(line, -1); len(tableMatches) > 0 {
			*lines = append(*lines, renderTableLine(tableMatches))
			continue
		}

		// List items
		if listMatches := reListItem.FindStringSubmatch(line); listMatches != nil {
			*lines = append(*lines, "  • "+renderInlineStyle(listMatches[1]))
			continue
		}

		// Ordered list
		if olMatches := reOrderedList.FindStringSubmatch(line); olMatches != nil {
			*lines = append(*lines, "  "+olMatches[1])
			continue
		}

		// Regular text with inline formatting
		*lines = append(*lines, "  "+renderInlineStyle(line))
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

// renderInlineStyle applies bold, italic, and code formatting to a string.
func renderInlineStyle(s string) string {
	// Code inline
	s = reCodeInline.ReplaceAllStringFunc(s, func(match string) string {
		code := strings.TrimPrefix(match, "`")
		code = strings.TrimSuffix(code, "`")
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#a8e6cf")).Render("`" + code + "`")
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
