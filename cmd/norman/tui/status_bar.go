package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/norman/norman/pkg/client"
)

// StatusBar displays session ID, workspace name, and connection state.
type StatusBar struct {
	connection string
	width      int
}

// NewStatusBar creates a new status bar with default styling.
func NewStatusBar() StatusBar {
	return StatusBar{
		connection: "Connecting...",
		width:      80,
	}
}

// SetConnection updates the connection state text.
func (s *StatusBar) SetConnection(state string) {
	s.connection = state
}

// Render returns the status bar view with session and workspace info.
func (s *StatusBar) Render(workspaceName, sessionID string) string {
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()

	// Connection state indicator
	connStyle := styles["status-connected"]
	if s.connection != "" && (strings.HasPrefix(s.connection, "✗") || strings.HasPrefix(s.connection, "⚠")) {
		connStyle = styles["status-disconnected"]
	}

	bar := lipgloss.JoinHorizontal(lipgloss.Left, connStyle.Render(s.connection))

	if workspaceName != "" {
		wsStyle := styles["on-surface-variant"].Bold(true)
		bar = lipgloss.JoinHorizontal(lipgloss.Left, bar, wsStyle.Render("workspace: "+workspaceName))
	}

	if sessionID != "" {
		sidStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(cs.HelpFooter))
		bar = lipgloss.JoinHorizontal(lipgloss.Left, bar, sidStyle.Render("session: "+client.ShortID(sessionID)))
	}

	return lipgloss.NewStyle().Width(s.width).Height(1).Foreground(lipgloss.Color(cs.OnSurfaceVariant)).Render(bar)
}
