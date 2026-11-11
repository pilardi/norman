package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// helpOverlay renders a modal-style help overlay on top of the TUI.
type helpOverlay struct {
	help    help.Model
	showing bool
}

// tuiKeyMap implements help.KeyMap for our TUI commands.
type tuiKeyMap struct{}

func (k tuiKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "Toggle focus")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "Send message")),
		key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "Clear chat")),
		key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "Show help")),
		key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "Sessions")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "Command mode")),
		key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "Close overlay")),
	}
}

func (k tuiKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{
			key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "Toggle focus between chat and events")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "Send message")),
			key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "Clear all messages")),
		},
		{
			key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "Show help overlay")),
			key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "Open session switcher")),
			key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "Toggle command mode")),
		},
	}
}

// newHelpOverlay creates a new help overlay with the given content.
func newHelpOverlay(content string) helpOverlay {
	h := help.New()
	h.Styles = help.Styles{
		ShortKey:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff6b6b")),
		ShortDesc: lipgloss.NewStyle().Foreground(lipgloss.Color("#cccccc")),
		FullKey:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff6b6b")),
		FullDesc:  lipgloss.NewStyle().Foreground(lipgloss.Color("#cccccc")),
	}
	return helpOverlay{help: h, showing: true}
}

// View renders the help overlay as a modal dialog.
func (h *helpOverlay) View() string {
	if !h.showing {
		return ""
	}
	return h.help.View(tuiKeyMap{})
}

// IsShowing returns whether the overlay is currently visible.
func (h *helpOverlay) IsShowing() bool {
	return h.showing
}
