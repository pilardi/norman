package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type sessionSwitcher struct {
	list    list.Model
	showing bool
}

func newSessionSwitcher(items []list.Item) sessionSwitcher {
	l := list.New(items, nil, 20, 15)
	return sessionSwitcher{list: l, showing: true}
}

func (s *sessionSwitcher) Init() tea.Cmd { return nil }

func (s *sessionSwitcher) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

// View implements tea.Model interface.
func (s *sessionSwitcher) View() string {
	if !s.showing {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Sessions\n")
	sb.WriteString(s.list.View())
	return sb.String()
}

// showModal renders the session switcher modal (alias for View).
func (s *sessionSwitcher) showModal() string {
	return s.View()
}

func (s *sessionSwitcher) IsShowing() bool { return s.showing }

func switchToSession(sessionID string) tea.Cmd {
	return func() tea.Msg {
		return WSMessageEvent{
			Event:     "session_switched",
			Message:   fmt.Sprintf("Switched to session %s", shortID(sessionID)),
			SessionID: sessionID,
		}
	}
}
