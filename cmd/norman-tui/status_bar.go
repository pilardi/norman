package main

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
	var parts []string

	// Connection state indicator
	connStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4a9eff"))
	if strings.HasPrefix(s.connection, "✗") || strings.HasPrefix(s.connection, "⚠") {
		connStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6b6b"))
	}
	parts = append(parts, connStyle.Render(s.connection))

	// Workspace name
	if workspaceName != "" {
		wsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a0a0a0")).Bold(true)
		parts = append(parts, wsStyle.Render("workspace: "+workspaceName))
	}

	// Session ID (truncated)
	if sessionID != "" {
		short := truncate(sessionID, 8)
		sidStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
		parts = append(parts, sidStyle.Render("session: "+short))
	}

	bar := strings.Join(parts, " │ ")
	return lipgloss.NewStyle().Width(s.width).Height(1).Foreground(lipgloss.Color("#aaaaaa")).Render(bar)
}
