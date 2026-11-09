package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/norman/norman/pkg/protocol"
)

// Command represents a keyboard shortcut with its handler.
type Command struct {
	Key    string
	Action func(*Model) tea.Cmd
	Help   string
}

// Commands returns the registered keyboard commands for the TUI client.
func Commands() []Command {
	return []Command{
		{Key: "ctrl+h", Action: showHelp, Help: "Show help overlay"},
		{Key: "ctrl+s", Action: openSessionSwitcher, Help: "Open session switcher"},
		{Key: "ctrl+n", Action: createNewSession, Help: "Create new session"},
		{Key: "ctrl+t", Action: focusEventPanel, Help: "Focus event panel"},
		{Key: "ctrl+c", Action: closeSession, Help: "Close current session"},
		{Key: "/", Action: enterCommandMode, Help: "Enter command mode"},
	}
}

// showHelp displays the help overlay.
func showHelp(m *Model) tea.Cmd {
	m.helpOverlay.showing = true
	return nil
}

// openSessionSwitcher opens the session switcher modal.
func openSessionSwitcher(m *Model) tea.Cmd {
	return loadSessionsList(m)
}

// createNewSession creates a new session for the current workspace.
func createNewSession(m *Model) tea.Cmd {
	m.statusBar.SetConnection("● Creating session...")
	return func() tea.Msg {
		client, err := getProtocolClient()
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("failed to create client: %v", err)}
		}

		cliType := protocol.Cli
		body := protocol.CreateSessionJSONRequestBody{
			ClientType: &cliType,
		}

		resp, err := client.CreateSession(context.Background(), body)
		if err != nil {
			m.statusBar.SetConnection("✗ Failed to create session")
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("Create session failed: %v", err)}
		}

		parsed, err := protocol.ParseCreateSessionResponse(resp)
		if err != nil {
			m.statusBar.SetConnection("✗ Parse error: " + err.Error())
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("Parse error: %v", err)}
		}

		result := parsed.GetJSON201()
		if result == nil || result.Id == nil {
			m.statusBar.SetConnection("✗ No session ID returned")
			return WSMessageEvent{Event: "error", Message: "No session ID in response"}
		}

		newID := result.Id.String()
		m.sessionID = newID
		m.statusBar.SetConnection(fmt.Sprintf("● New session created: %s", shortID(newID)))
		return WSMessageEvent{Event: "session_created", Message: fmt.Sprintf("Created session %s", shortID(newID))}
	}
}

// focusEventPanel switches focus to the event panel.
func focusEventPanel(m *Model) tea.Cmd {
	m.chatPanel.focused = false
	return nil
}

// closeSession sends a close command to the current session.
func closeSession(m *Model) tea.Cmd {
	m.statusBar.SetConnection("● Closing session...")
	return func() tea.Msg {
		client, err := getProtocolClient()
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("failed to create client: %v", err)}
		}

		resp, err := client.ArchiveSessionByID(context.Background(), m.sessionID)
		if err != nil {
			m.statusBar.SetConnection("✗ Failed to close session")
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("Archive session failed: %v", err)}
		}

		parsed, err := protocol.ParseArchiveSessionByIDResponse(resp)
		if err != nil {
			m.statusBar.SetConnection("✗ Parse error: " + err.Error())
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("Parse error: %v", err)}
		}

		if parsed.HTTPResponse != nil && parsed.HTTPResponse.StatusCode == 204 {
			m.statusBar.SetConnection(fmt.Sprintf("✓ Session closed: %s", shortID(m.sessionID)))
			return WSMessageEvent{Event: "session_closed", Message: fmt.Sprintf("Closed session %s", shortID(m.sessionID))}
		}

		return WSMessageEvent{Event: "error", Message: "Unexpected response from archive"}
	}
}

// enterCommandMode enters command mode (reserves the `/` key).
func enterCommandMode(m *Model) tea.Cmd {
	m.commandMode = !m.commandMode
	if m.commandMode {
		m.statusBar.SetConnection("⊶ Command mode — type a command")
	} else {
		m.statusBar.SetConnection(fmt.Sprintf("● Connected (%s)", m.connectionState))
	}
	return nil
}

// loadSessionsList fetches available sessions and opens the session switcher.
func loadSessionsList(m *Model) tea.Cmd {
	return func() tea.Msg {
		client, err := getProtocolClient()
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("failed to create client: %v", err)}
		}

		resp, err := client.ListSessions(context.Background())
		if err != nil {
			m.statusBar.SetConnection("✗ Failed to list sessions")
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("List sessions failed: %v", err)}
		}

		parsed, err := protocol.ParseListSessionsResponse(resp)
		if err != nil {
			m.statusBar.SetConnection("✗ Parse error: " + err.Error())
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("Parse error: %v", err)}
		}

		sessions := parsed.GetJSON200()
		if sessions == nil || len(*sessions) == 0 {
			m.statusBar.SetConnection("⚠ No sessions found")
			return WSMessageEvent{Event: "error", Message: "No sessions available"}
		}

		items := make([]list.Item, len(*sessions))
		for i, s := range *sessions {
			name := ""
			if s.Name != nil {
				name = *s.Name
			}
			if name == "" {
				name = "Session " + shortID(s.Id.String())
			}
			status := "active"
			if s.SessionStatus != nil {
				status = string(*s.SessionStatus)
			}
			items[i] = SessionItem{
				ID:        s.Id.String(),
				Name:      name,
				Status:    status,
				CreatedAt: formatTime(s.CreatedAt),
			}
		}

		return sessionListMsg{items: items}
	}
}

// sessionItem implements list.Item for sessions.
type SessionItem struct {
	ID        string
	Name      string
	Status    string
	CreatedAt string
}

func (s SessionItem) Title() string { return s.Name }
func (s SessionItem) Description() string {
	return fmt.Sprintf("ID: %s | Status: %s", shortID(s.ID), s.Status)
}
func (s SessionItem) FilterValue() string { return s.Name + " " + s.Status }

// sessionListMsg is the tea.Msg returned when sessions are loaded.
type sessionListMsg struct {
	items []list.Item
}

// formatTime formats a *time.Time to a readable string, returning "N/A" if nil.
func formatTime(t interface{}) string {
	if t == nil {
		return "N/A"
	}
	switch v := t.(type) {
	case *string:
		if v != nil {
			return *v
		}
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return "N/A"
	}
	return "N/A"
}

// helpText returns the help overlay content.
func helpText() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4a9eff")).Render(" Norman TUI Help "))
	sb.WriteString("\n\n")

	commands := Commands()
	for _, cmd := range commands {
		keyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff6b6b"))
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cccccc"))
		sb.WriteString(keyStyle.Render(cmd.Key) + "  ")
		sb.WriteString(helpStyle.Render(cmd.Help))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("Press Esc or q to close this overlay"))
	return sb.String()
}
