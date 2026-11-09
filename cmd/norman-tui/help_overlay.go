package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpOverlay renders a modal-style help overlay on top of the TUI.
type helpOverlay struct {
	content string
	showing bool
}

// newHelpOverlay creates a new help overlay with the given content.
func newHelpOverlay(content string) helpOverlay {
	return helpOverlay{content: content, showing: true}
}

// View renders the help overlay as a modal dialog.
func (h *helpOverlay) View() string {
	if !h.showing {
		return ""
	}

	var sb strings.Builder

	// Title bar with border
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#4a9eff")).
		PaddingLeft(2).
		Border(lipgloss.NormalBorder(), false, false, true, false)
	sb.WriteString(titleStyle.Render(" Norman TUI Help "))
	sb.WriteString("\n")

	// Content (already styled in helpText())
	sb.WriteString(h.content)
	sb.WriteString("\n")

	// Bottom border
	okStyle := lipgloss.NewStyle().
		Bold(true).
		Background(lipgloss.Color("#4a9eff")).
		Foreground(lipgloss.Color("#000000")).
		Padding(1, 3)
	sb.WriteString(okStyle.Render(" OK "))

	return sb.String()
}

// IsShowing returns whether the overlay is currently visible.
func (h *helpOverlay) IsShowing() bool {
	return h.showing
}
