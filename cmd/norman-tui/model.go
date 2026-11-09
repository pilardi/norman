package main

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// ChatMessage represents a single message in the chat stream.
type ChatMessage struct {
	Role      string
	Content   string
	Timestamp time.Time
}

// EventItem represents an item in the event/task panel list.
type EventItem struct {
	Type      string
	Message   string
	Timestamp time.Time
}

func (e EventItem) Title() string       { return e.Type }
func (e EventItem) Description() string { return e.Message }
func (e EventItem) FilterValue() string { return e.Type + " " + e.Message }

// Model is the main application state for the TUI client.
type Model struct {
	chatPanel        ChatPanel
	eventPanel       list.Model
	statusBar        StatusBar
	spinner          spinner.Model
	connectionState  string
	workspaceName    string
	sessionID        string
	initialWorkspace string
	events           []EventItem
	done             bool
}

// NewModel creates a new application model with default settings.
func NewModel() *Model {
	spinnerModel := spinner.New()
	spinnerModel.Spinner = spinner.Dot

	return &Model{
		chatPanel:       NewChatPanel(),
		eventPanel:      newEventPanel(),
		statusBar:       NewStatusBar(),
		spinner:         spinnerModel,
		connectionState: "● Connecting...",
	}
}

// Init is the bubbletea initialization hook.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		initializeClient(context.Background(), m),
		connectWebSocket(m),
	)
}

// Update handles incoming messages and updates the model state.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.chatPanel.width = msg.Width - 30 // reserve space for event panel
		m.chatPanel.height = msg.Height - 12

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.done = true
			return m, tea.Quit
		case "tab":
			// Toggle focus between chat and event panels
			if m.chatPanel.focused {
				m.chatPanel.focused = false
			} else {
				m.chatPanel.focused = true
			}
		case "enter":
			if !m.chatPanel.focused {
				return m, nil
			}
			input := m.chatPanel.textInput.Value()
			if input == "" {
				return m, nil
			}
			m.chatPanel.AppendMessage(ChatMessage{
				Role:      "user",
				Content:   input,
				Timestamp: time.Now(),
			})
			m.chatPanel.textInput.SetValue("")
			m.statusBar.SetConnection("● Sending...")
			go sendChatMessage(m.sessionID, input)
			return m, nil

		case "ctrl+l":
			m.chatPanel.Clear()
			return m, nil
		}

	default:
		switch msg := msg.(type) {
		case WSMessageEvent:
			m.handleWSMessage(msg)
		}
	}

	return m, nil
}

func (m *Model) handleWSMessage(msg WSMessageEvent) {
	switch msg.Event {
	case "item_started":
		m.events = append(m.events, EventItem{
			Type:      "tool_call",
			Message:   msg.Message,
			Timestamp: time.Now(),
		})
	case "turn_completed":
		m.statusBar.SetConnection("● Connected")
	case "task_progress":
		m.events = append(m.events, EventItem{
			Type:      "progress",
			Message:   msg.Message,
			Timestamp: time.Now(),
		})
	case "notification":
		m.events = append(m.events, EventItem{
			Type:      "notification",
			Message:   msg.Message,
			Timestamp: time.Now(),
		})
	default:
		m.events = append(m.events, EventItem{
			Type:      msg.Event,
			Message:   msg.Message,
			Timestamp: time.Now(),
		})
	}

	// Update event panel list.
	items := make([]list.Item, len(m.events))
	for i, e := range m.events {
		items[i] = e
	}
	m.eventPanel.SetItems(items)
}

// View renders the TUI layout.
func (m *Model) View() string {
	if m.done {
		return "Session closed.\n"
	}

	var out string

	// Status bar at top
	out += m.statusBar.Render(m.workspaceName, m.sessionID) + "\n"

	// Chat panel (left, ~70%)
	out += m.chatPanel.Render()

	// Event panel (right, ~30%)
	out += "\n" + RenderEventPanel(m.events)

	return out
}
