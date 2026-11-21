package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
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
		key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "New session")),
		key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "Focus events")),
		key.NewBinding(key.WithKeys("ctrl+z"), key.WithHelp("ctrl+z", "Compress session")),
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
			key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "Create new session")),
			key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "Focus event panel")),
			key.NewBinding(key.WithKeys("ctrl+z"), key.WithHelp("ctrl+z", "Compress session")),
		},
	}
}

func (k tuiKeyMap) ShortFull() bool { return true }

// newHelpOverlay creates a new help overlay with the given content.
func newHelpOverlay(content string) helpOverlay {
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()
	h := help.New()
	h.Styles = help.Styles{
		ShortKey:  styles["help-key"],
		ShortDesc: styles["help-desc"],
		FullKey:   styles["help-key"],
		FullDesc:  styles["help-desc"],
	}
	return helpOverlay{help: h, showing: true}
}

// View renders the help overlay as a modal dialog.
func (h *helpOverlay) View() string {
	if !h.showing {
		return ""
	}
	// Force full help grid layout for better readability
	h.help.ShowAll = true
	return h.help.View(tuiKeyMap{})
}

// ToggleHelp toggles between short and full help view.
func (h *helpOverlay) ToggleHelp() {
	h.help.ShowAll = !h.help.ShowAll
}

// IsShowing returns whether the overlay is currently visible.
func (h *helpOverlay) IsShowing() bool {
	return h.showing
}
