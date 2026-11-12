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

// EventPanel is the event/task panel with focus-aware border styling.
type EventPanel struct {
	focused bool
	items   []EventItem
	list    list.Model
}

// NewEventPanel creates a new event panel.
func NewEventPanel() EventPanel {
	delegate := list.NewDefaultDelegate()
	delegate.SetHeight(2) // compact items
	return EventPanel{
		list: list.New([]list.Item{}, delegate, 30, 0),
	}
}

// SetItems updates the items in the event panel.
func (p *EventPanel) SetItems(items []list.Item) {
	p.items = nil // reset typed items; we use list.Items directly
	for _, item := range items {
		if ei, ok := item.(EventItem); ok {
			p.items = append(p.items, ei)
		}
	}
	p.list.SetItems(items)
}

// Focus sets the event panel to focused state.
func (p *EventPanel) Focus() {
	p.focused = true
}

// Blur removes focus from the event panel.
func (p *EventPanel) Blur() {
	p.focused = false
}

// Render renders the event panel view with focus-aware border.
func (p *EventPanel) Render() string {
	borderColor := lipgloss.Color("#585858")
	if p.focused {
		borderColor = lipgloss.Color("#4a9eff")
	}
	header := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).Render(
		lipgloss.NewStyle().Foreground(borderColor).Render("Events / Tasks"),
	)
	return lipgloss.JoinVertical(lipgloss.Left, header, p.list.View())
}

// truncate shortens a string to max length, appending "..." if truncated.
func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
