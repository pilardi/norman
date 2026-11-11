package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// newEventPanel creates a list-based event/task panel.
func newEventPanel() list.Model {
	items := make([]list.Item, 0)
	delegate := list.NewDefaultDelegate()

	return list.New(items, delegate, 30, 20) // ~15 items visible
}

// RenderEventItem formats a single event item for display.
func RenderEventItem(item EventItem) string {
	prefix := "●"
	switch item.Type {
	case "tool_call":
		prefix = "🔧"
	case "notification":
		prefix = "📢"
	case "progress":
		prefix = "⏳"
	case "turn_completed":
		prefix = "✅"
	case "item_started":
		prefix = "▶️"
	case "item_completed":
		prefix = "✓"
	}

	ts := item.Timestamp.Format("15:04")
	return fmt.Sprintf("%s [%s] %s", prefix, ts, truncate(item.Message, 24))
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

// RenderEventPanel renders the event panel with lipgloss borders.
func RenderEventPanel(items []EventItem) string {
	var sb strings.Builder

	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#585858")).Render("Events / Tasks"))
	sb.WriteString("\n")

	if len(items) == 0 {
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(
			"Events will appear here as the session progresses.\n\n" +
				"Press Ctrl+S to switch sessions, or / for command mode.",
		)
		sb.WriteString(hint)
	} else {
		for _, item := range items {
			sb.WriteString(RenderEventItem(item))
			sb.WriteString("\n")
		}
	}

	return lipgloss.NewStyle().
		Width(30).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#585858")).
		Render(sb.String())
}
