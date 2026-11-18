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
func (s *StatusBar) Render(workspaceName, sessionID string, spinnerView string) string {
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()

	var parts []string

	// 1. Workspace name
	if workspaceName != "" {
		wsStyle := styles["on-surface-variant"].Bold(true)
		parts = append(parts, wsStyle.Render("workspace: "+workspaceName))
	}

	// 2. Session ID
	if sessionID != "" {
		sidStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(cs.HelpFooter))
		parts = append(parts, sidStyle.Render("session: "+client.ShortID(sessionID)))
	}

	// 3. Connection state indicator or spinner
	connPart := ""
	if s.connection != "" {
		if s.connection == "Sending" {
			// Combine "Sending" with the spinner view
			connStyle := styles["status-connected"]
			connPart = connStyle.Render("Sending " + spinnerView)
		} else {
			connStyle := styles["status-connected"]
			if strings.HasPrefix(s.connection, "✗") || strings.HasPrefix(s.connection, "⚠") {
				connStyle = styles["status-disconnected"]
			}
			connPart = connStyle.Render(s.connection)
		}
	}

	if connPart != "" {
		parts = append(parts, connPart)
	}

	// Join with a separator
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color(cs.OnSurfaceVariant)).Render(" • ")
	bar := ""
	if len(parts) > 0 {
		bar = parts[0]
		for i := 1; i < len(parts); i++ {
			bar = lipgloss.JoinHorizontal(lipgloss.Left, bar, separator, parts[i])
		}
	}

	return lipgloss.NewStyle().Width(s.width).Height(1).Foreground(lipgloss.Color(cs.OnSurfaceVariant)).Render(bar)
}
