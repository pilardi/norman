package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/norman/norman/pkg/client"
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
		{Key: "ctrl+c", Action: closeTUI, Help: "Close TUI"},
		{Key: "tab", Action: toggleFocus, Help: "Toggle focus between chat and events"},
		{Key: "enter", Action: sendMessage, Help: "Send message"},
		{Key: "ctrl+l", Action: clearChat, Help: "Clear chat"},
		{Key: "ctrl+z", Action: compressSession, Help: "Compress session"},
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
	m.state = StateCreatingSession
	return func() tea.Msg {
		newID, err := client.CreateNewSession(m.workspaceName)
		if err != nil {
			m.statusBar.SetConnection("✗ Failed to create session")
			return WSMessageEvent{Event: "session_create_error", Message: fmt.Sprintf("Create session failed: %v", err)}
		}

		m.sessionID = newID
		m.statusBar.SetConnection(fmt.Sprintf("● New session created: %s", client.ShortID(newID)))
		return WSMessageEvent{Event: "session_created", Message: fmt.Sprintf("Created session %s", client.ShortID(newID))}
	}
}

// focusEventPanel switches focus to the event panel.
func focusEventPanel(m *Model) tea.Cmd {
	m.chatPanel.focused = false
	return nil
}

// closeTUI closes the TUI application.
func closeTUI(m *Model) tea.Cmd {
	m.done = true
	return tea.Quit
}

// toggleFocus switches focus between chat and event panels.
func toggleFocus(m *Model) tea.Cmd {
	if m.chatPanel.focused {
		m.chatPanel.Blur()
		m.eventPanel.Focus()
	} else {
		m.chatPanel.Focus()
		m.eventPanel.Blur()
	}
	return nil
}

// sendMessage sends the current message in the chat input.
func sendMessage(m *Model) tea.Cmd {
	input := m.chatPanel.textInput.Value()
	if input == "" {
		return nil
	}
	m.chatPanel.AppendMessage(ChatMessage{
		Role:      "user",
		Content:   input,
		Timestamp: time.Now(),
	})
	m.chatPanel.textInput.SetValue("")
	m.statusBar.SetConnection("Sending")

	// Create a cancellable context for this request
	sendCtx, sendCancel := context.WithCancel(context.Background())
	m.sendCtx = sendCtx
	m.sendCancel = sendCancel
	m.state = StateSendingMessage

	go sendChatMessage(m, sendCtx, m.sessionID, input)
	return nil
}

// clearChat clears the chat panel.
func clearChat(m *Model) tea.Cmd {
	m.chatPanel.Clear()
	return nil
}

// compressSession compresses the current session's conversation history.
func compressSession(m *Model) tea.Cmd {
	m.statusBar.SetConnection("● Compressing session...")
	m.state = StateCompressingSession
	return func() tea.Msg {
		// Use the client.CompressSession function
		escapedID := client.UrlPathEscape(m.sessionID)
		result, err := client.CompressSession(context.Background(), escapedID, false)
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("compress failed: %v", err)}
		}

		compressedCount := client.DerefInt(result.CompressedCount)
		keptCount := client.DerefInt(result.KeptCount)
		tokensSaved := client.DerefInt(result.TokensSaved)
		summary := client.StrPtrVal(result.Summary)

		msg := fmt.Sprintf("Session compressed: %d turns → %d kept, saved ~%d tokens", compressedCount, keptCount, tokensSaved)
		if summary != "" {
			msg += fmt.Sprintf(" | Summary: %s", summary)
		}

		return WSMessageEvent{Event: "compress_completed", Message: msg}
	}
}

// loadSessionsList fetches available sessions and opens the session switcher.
func loadSessionsList(m *Model) tea.Cmd {
	m.statusBar.SetConnection("● Loading sessions...")
	m.state = StateLoadingSessions
	return func() tea.Msg {
		c, err := client.GetProtocolClient()
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("failed to create client: %v", err)}
		}

		resp, err := c.ListSessions(context.Background())
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
				name = "Session " + client.ShortID(s.Id.String())
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
	return fmt.Sprintf("ID: %s | Status: %s", client.ShortID(s.ID), s.Status)
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
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()
	var sb strings.Builder
	sb.WriteString(styles["help-title"].Render(" Norman TUI Help "))
	sb.WriteString("\n\n")

	commands := Commands()
	for _, cmd := range commands {
		sb.WriteString(styles["help-key"].Render(cmd.Key) + "  ")
		sb.WriteString(styles["help-desc"].Render(cmd.Help))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(styles["help-footer"].Render("Press Esc or q to close this overlay"))
	return sb.String()
}
