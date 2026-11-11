package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/protocol"
)

// ChatMessage represents a single message in the chat stream.
type ChatMessage struct {
	Role       string
	Content    string
	RichOutput *protocol.RichOutput
	Timestamp  time.Time
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

	// Phase 3 overlays
	helpOverlay     helpOverlay
	sessionSwitcher sessionSwitcher
	commandMode     bool

	// WebSocket message channel for persistent event streaming.
	wsMsg chan WSMessageEvent
}

// NewModel creates a new application model with default settings.
func NewModel(workspaceName string) *Model {
	spinnerModel := spinner.New()
	spinnerModel.Spinner = spinner.Dot

	return &Model{
		chatPanel:        NewChatPanel(),
		eventPanel:       newEventPanel(),
		statusBar:        NewStatusBar(),
		spinner:          spinnerModel,
		connectionState:  "● Connecting...",
		initialWorkspace: workspaceName,
	}
}

// Init is the bubbletea initialization hook.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}

	// Only auto-create a session if no sessionID was provided.
	if m.sessionID == "" {
		cmds = append(cmds, createSessionCmd(m))
	} else {
		cmds = append(cmds, func() tea.Msg {
			return WSMessageEvent{Event: "session_created", SessionID: m.sessionID}
		})
	}

	cmds = append(cmds, initializeClient(context.Background(), m), startWebSocket(m))
	return tea.Batch(cmds...)
}

// createSessionCmd creates a new session and sets the sessionID on the model.
func createSessionCmd(model *Model) tea.Cmd {
	return func() tea.Msg {
		sessID, err := client.CreateNewSession(model.initialWorkspace)
		if err != nil {
			return WSMessageEvent{Event: "error", Message: err.Error()}
		}
		model.sessionID = sessID
		return WSMessageEvent{Event: "session_created", Message: model.initialWorkspace}
	}
}

// Update handles incoming messages and updates the model state.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.chatPanel.width = msg.Width - 30 // reserve space for event panel
		m.chatPanel.height = msg.Height - 12

	case tea.KeyMsg:
		// Phase 3: help overlay dismissal (always handled first)
		if m.helpOverlay.IsShowing() {
			switch msg.String() {
			case "esc", "q":
				m.helpOverlay.showing = false
				return m, nil
			case "enter":
				m.helpOverlay.showing = false
				return m, nil
			}
		}

		// Phase 3: session switcher dismissal / selection
		if m.sessionSwitcher.IsShowing() {
			switch msg.String() {
			case "esc", "q":
				m.sessionSwitcher.showing = false
				return m, nil
			case "enter":
				if len(m.sessionSwitcher.list.Items()) > 0 {
					item := m.sessionSwitcher.list.SelectedItem().(SessionItem)
					return m, switchToSession(item.ID)
				}
			}
			lst, _ := m.sessionSwitcher.list.Update(msg)
			m.sessionSwitcher.list = lst
			return m, nil
		}

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

		// Phase 3: new keyboard shortcuts
		case "ctrl+h":
			if !m.helpOverlay.IsShowing() {
				m.helpOverlay = newHelpOverlay(helpText())
			} else {
				m.helpOverlay.showing = false
			}
			return m, nil

		case "ctrl+s":
			if !m.sessionSwitcher.IsShowing() {
				return m, loadSessionsList(m)
			}
			m.sessionSwitcher.showing = false
			return m, nil

		case "/":
			m.commandMode = !m.commandMode
			if m.commandMode {
				m.statusBar.SetConnection("⊶ Command mode — type a command")
			} else {
				m.statusBar.SetConnection(fmt.Sprintf("● Connected (%s)", m.connectionState))
			}
			return m, nil
		}

	default:
		switch msg := msg.(type) {
		case sessionListMsg:
			m.sessionSwitcher = newSessionSwitcher(msg.items)
			return m, nil

		case WSMessageEvent:
			m.handleWSMessage(msg)
		}

		// Handle persistent WebSocket messages via channel.
		select {
		case msg, ok := <-m.wsMsg:
			if !ok {
				// Channel closed = connection dropped.
				m.statusBar.SetConnection("✗ WebSocket disconnected")
				return m, nil
			}
			m.handleWSMessage(msg)
		default:
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
	case "session_switched":
		m.sessionID = msg.SessionID
		m.statusBar.SetConnection(fmt.Sprintf("● Switched to session %s", shortID(msg.SessionID)))
	case "session_created":
		if msg.SessionID != "" {
			m.statusBar.SetConnection(fmt.Sprintf("✓ Using session %s", truncate(msg.SessionID, 12)))
			m.chatPanel.AppendMessage(ChatMessage{
				Role:      "system",
				Content:   fmt.Sprintf("Using existing session in workspace %s — type a message to start.", m.workspaceName),
				Timestamp: time.Now(),
			})
		} else {
			m.statusBar.SetConnection("✓ New session created")
			m.chatPanel.AppendMessage(ChatMessage{
				Role:      "system",
				Content:   fmt.Sprintf("New session created in workspace %s — type a message to start.", msg.Message),
				Timestamp: time.Now(),
			})
		}
	case "session_closed":
		m.statusBar.SetConnection(fmt.Sprintf("✗ Session closed: %s", truncate(msg.Message, 20)))
	case "session_compressed":
		m.statusBar.SetConnection("⚠ Session compressed — context trimmed")
	case "error":
		m.statusBar.SetConnection("✗ " + msg.Message)
		m.done = true // TUI will quit after current view render.
	case "connection_error":
		m.statusBar.SetConnection("✗ " + msg.Message)
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

	// Extract RichOutput from event data if present (Phase 2).
	if ro := extractRichOutput(msg.Data); ro != nil {
		m.chatPanel.AppendMessage(ChatMessage{
			Role:       "assistant",
			Content:    "[RichOutput rendered below]",
			RichOutput: ro,
			Timestamp:  time.Now(),
		})
	}

	// Update event panel list.
	items := make([]list.Item, len(m.events))
	for i, e := range m.events {
		items[i] = e
	}
	m.eventPanel.SetItems(items)
}

// extractRichOutput parses a data map into a *protocol.RichOutput if present.
func extractRichOutput(data map[string]interface{}) *protocol.RichOutput {
	if data == nil {
		return nil
	}

	ro := &protocol.RichOutput{}

	if ct, ok := data["content_type"].(string); ok {
		ro.ContentType = &ct
	}
	if md, ok := data["mime_type"].(string); ok {
		ro.MimeType = &md
	}
	if rh, ok := data["render_hint"].(string); ok {
		switch rh {
		case "text":
			hint := protocol.Text
			ro.RenderHint = &hint
		case "markdown":
			hint := protocol.Markdown
			ro.RenderHint = &hint
		case "html":
			hint := protocol.Html
			ro.RenderHint = &hint
		case "json":
			hint := protocol.Json
			ro.RenderHint = &hint
		case "pdf":
			hint := protocol.Pdf
			ro.RenderHint = &hint
		case "chart":
			hint := protocol.Chart
			ro.RenderHint = &hint
		case "diagram":
			hint := protocol.Diagram
			ro.RenderHint = &hint
		case "file_ref":
			hint := protocol.FileRef
			ro.RenderHint = &hint
		}
	}
	if au, ok := data["artifact_url"].(string); ok {
		ro.ArtifactUrl = &au
	}

	// Extract structured_data if present.
	if sd, ok := data["structured_data"].(map[string]interface{}); ok {
		ro.StructuredData = &sd
	}

	return ro
}

// View renders the TUI layout.
func (m *Model) View() string {
	if m.done {
		return "Session closed.\n"
	}

	var out string

	// Status bar at top
	out += m.statusBar.Render(m.workspaceName, m.sessionID) + "\n"

	// Two-column layout: chat on left (~70%), events on right (~30%)
	eventView := RenderEventPanel(m.events)
	out += lipgloss.JoinHorizontal(lipgloss.Left,
		lipgloss.NewStyle().Width(int(float64(m.chatPanel.width)*0.7)).Render(m.chatPanel.Render()),
		lipgloss.NewStyle().Width(int(float64(m.chatPanel.width)*0.3)).Render(eventView),
	)

	// Phase 3: help overlay on top
	if m.helpOverlay.IsShowing() {
		out += m.helpOverlay.View()
	}

	// Phase 3: session switcher modal on top (takes full width)
	if m.sessionSwitcher.IsShowing() {
		out = m.sessionSwitcher.showModal() + "\n"
	}

	return out
}
