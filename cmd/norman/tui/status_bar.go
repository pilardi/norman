package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar displays session ID, workspace name, and connection state.
type StatusBar struct {
	connection string
	width      int
}

// NewStatusBar creates a new status bar with default styling.
func NewStatusBar() StatusBar {
	return StatusBar{
		connection: "● Connecting...",
		width:      80,
	}
}

// SetConnection updates the connection state text.
func (s *StatusBar) SetConnection(state string) {
	s.connection = state
}

// Render returns the status bar view with session and workspace info.
func (s *StatusBar) Render(workspaceName, sessionID string) string {
	// Connection state indicator
	connStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4a9eff"))
	if s.connection != "" && (strings.HasPrefix(s.connection, "✗") || strings.HasPrefix(s.connection, "⚠")) {
		connStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6b6b"))
	}

	bar := lipgloss.JoinHorizontal(lipgloss.Left, connStyle.Render(s.connection))

	if workspaceName != "" {
		wsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a0a0a0")).Bold(true)
		bar = lipgloss.JoinHorizontal(lipgloss.Left, bar, wsStyle.Render("workspace: "+workspaceName))
	}

	if sessionID != "" {
		sidStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
		bar = lipgloss.JoinHorizontal(lipgloss.Left, bar, sidStyle.Render("session: "+shortID(sessionID)))
	}

	return lipgloss.NewStyle().Width(s.width).Height(1).Foreground(lipgloss.Color("#aaaaaa")).Render(bar)
}
