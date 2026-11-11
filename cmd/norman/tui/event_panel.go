package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// EventItem represents an item in the event/task panel list.
type EventItem struct {
	Type      string
	Message   string
	Timestamp time.Time
}

func (e EventItem) Title() string       { return e.Type }
func (e EventItem) Description() string { return e.Message }
func (e EventItem) FilterValue() string { return e.Type + " " + e.Message }

// newEventPanel creates a list-based event/task panel.
func newEventPanel() list.Model {
	items := make([]list.Item, 0)
	delegate := list.NewDefaultDelegate()
	delegate.SetHeight(2) // compact items

	return list.New(items, delegate, 30, 0) // height=0 means auto-fit
}

// RenderEventPanel renders the event panel using bubbles/list component.
func RenderEventPanel(items []EventItem) string {
	l := newEventPanel()
	converted := make([]list.Item, len(items))
	for i, item := range items {
		converted[i] = item
	}
	l.SetItems(converted)

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Foreground(lipgloss.Color("#585858")).Render("Events / Tasks"),
		l.View(),
	)
}

// truncate shortens a string to max length, appending "..." if truncated.
func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
