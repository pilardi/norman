package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
	"github.com/norman/norman/pkg/client"
)

// ChatMessage represents a single message in the chat stream.
type ChatMessage struct {
	Role      string
	Content   string
	Timestamp time.Time
}

// Model is the main application state for the TUI client.
type Model struct {
	chatPanel        ChatPanel
	eventPanel       EventPanel
	statusBar        StatusBar
	spinner          spinner.Model
	connectionState  string
	workspaceName    string
	sessionID        string
	initialWorkspace string
	events           []EventItem
	done             bool
	fatalError       error
	isWorking        bool // true while a command that makes a server request is in progress

	// Phase 3 overlays
	helpOverlay     helpOverlay
	sessionSwitcher sessionSwitcher

	// WebSocket message channel for persistent event streaming.
	wsMsg chan WSMessageEvent
	// Buffered to prevent blocking on channel send (max 100 pending events).

	// Current WebSocket connection reference, used to close and reconnect on session switch.
	wsConn *websocket.Conn

	// Provider name for LLM calls (from -p flag).
	provider string
}

// NewModel creates a new application model with default settings.
func NewModel(workspaceName string, provider string) *Model {
	spinnerModel := spinner.New()
	spinnerModel.Spinner = spinner.Dot

	return &Model{
		chatPanel:        NewChatPanel(),
		eventPanel:       NewEventPanel(),
		statusBar:        NewStatusBar(),
		spinner:          spinnerModel,
		connectionState:  "● Connecting...",
		initialWorkspace: workspaceName,
		wsMsg:            make(chan WSMessageEvent, 100),
		provider:         provider,
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

	// Focus the chat panel and start cursor blink animation on startup
	cmds = append(cmds, m.chatPanel.Focus())

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
		m.chatPanel.width = msg.Width
		m.chatPanel.height = msg.Height - 14 // leave room for status bar + input line + padding

		// Calculate chat panel width (70% of total)
		chatWidth := int(float64(m.chatPanel.width) * 0.7)

		// Subtract 2 for the border (left and right border characters)
		contentWidth := chatWidth - 2
		if contentWidth < 0 {
			contentWidth = 0
		}

		// Update viewport width
		m.chatPanel.viewport.Width = contentWidth

		// Update textarea width
		m.chatPanel.textInput.SetWidth(contentWidth)

		m.chatPanel.updateViewportHeight()

	case cursor.BlinkMsg:
		// Textarea should also process cursor blinks.
		var cmd tea.Cmd
		chatPanelModel, cmd := m.chatPanel.Update(msg)
		if chatPanelPtr, ok := chatPanelModel.(*ChatPanel); ok {
			m.chatPanel = *chatPanelPtr
		}
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

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
			case "?":
				// Toggle between short and full help view
				m.helpOverlay.help.ShowAll = !m.helpOverlay.help.ShowAll
				return m, nil
			}
			// Let the help component handle other keys (arrows, etc.)
			_, cmd := m.helpOverlay.help.Update(msg)
			return m, cmd
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
					m.sessionSwitcher.showing = false
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
			var cmd tea.Cmd
			if m.chatPanel.focused {
				m.chatPanel.Blur()
				m.eventPanel.Focus()
			} else {
				cmd = m.chatPanel.Focus()
				m.eventPanel.Blur()
			}
			return m, cmd

		case "enter":
			if m.isWorking {
				m.statusBar.SetConnection("Working, please wait...")
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
			m.statusBar.SetConnection("Sending")
			go sendChatMessage(m, m.sessionID, input)

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
				if m.isWorking {
					m.statusBar.SetConnection("Working, please wait...")
					return m, nil
				}
				return m, loadSessionsList(m)
			}
			m.sessionSwitcher.showing = false
			return m, nil

		case "ctrl+n":
			// Create new session
			if !m.sessionSwitcher.IsShowing() && !m.helpOverlay.IsShowing() {
				if m.isWorking {
					m.statusBar.SetConnection("Working, please wait...")
					return m, nil
				}
				return m, createNewSession(m)
			}
			return m, nil

		case "ctrl+t":
			// Focus event panel
			if !m.sessionSwitcher.IsShowing() && !m.helpOverlay.IsShowing() {
				m.chatPanel.Blur()
				m.eventPanel.Focus()
			}
			return m, nil

		case "ctrl+z":
			// Compress session
			if !m.sessionSwitcher.IsShowing() && !m.helpOverlay.IsShowing() {
				if m.isWorking {
					m.statusBar.SetConnection("Working, please wait...")
					return m, nil
				}
				return m, compressSession(m)
			}
			return m, nil

		case "/":
			// Open help overlay with command list
			if !m.helpOverlay.IsShowing() {
				m.helpOverlay = newHelpOverlay(helpText())
			} else {
				m.helpOverlay.showing = false
			}
			return m, nil
		}

		// Forward all other keys to textinput when focused (arrows, backspace, etc.)
		if m.chatPanel.focused {
			_, cmd := m.chatPanel.Update(msg)
			return m, cmd
		}

		// Forward keys to event panel when it's focused
		if !m.chatPanel.focused {
			_, cmd := m.eventPanel.Update(msg)
			return m, cmd
		}

	default:
		switch msg := msg.(type) {
		case sessionListMsg:
			m.sessionSwitcher = newSessionSwitcher(msg.items)
			return m, nil

		case WSMessageEvent:
			if msg.Event == "chat_updated" {
				// Drain ALL remaining events from the channel before returning,
				// so View() renders with fully up-to-date content (no stale gap).
				for {
					select {
					case ev, ok := <-m.wsMsg:
						if !ok {
							m.statusBar.SetConnection("✗ WebSocket disconnected")
							return m, nil
						}
						m.handleWSMessage(ev)
					default:
						// Ensure viewport is updated with all processed messages before re-rendering
						m.chatPanel.updateViewport()
						return m, nil
					}
				}
			}
			if msg.Event == "session_switched" {
				// Close old WS connection and open a new one with the updated session ID.
				if m.wsConn != nil {
					_ = m.wsConn.Close()
				}
				// Update session ID and status bar
				m.sessionID = msg.SessionID
				m.statusBar.SetConnection(fmt.Sprintf("● Switched to session %s", client.ShortID(msg.SessionID)))
				// Load session history before starting the WebSocket connection.
				loadSessionHistory(m, msg.SessionID)
				return m, startWebSocket(m)
			}
			m.handleWSMessage(msg)
			if m.done {
				return m, tea.Quit
			}
		}

		// Drain ALL pending events from the channel before returning.
		for {
			select {
			case msg, ok := <-m.wsMsg:
				if !ok {
					// Channel closed = connection dropped.
					m.statusBar.SetConnection("✗ WebSocket disconnected")
					return m, nil
				}
				m.handleWSMessage(msg)
			default:
				return m, nil
			}
		}
	}

	return m, nil
}

func (m *Model) handleWSMessage(msg WSMessageEvent) {
	// Reset working state for command completion or error events
	if msg.Event == "compress_completed" || msg.Event == "error" {
		m.isWorking = false
	}

	switch msg.Event {
	case "item_started":
		if m.isSessionScoped(msg) {
			m.events = append(m.events, EventItem{
				Type:      "tool_call",
				Message:   msg.Message,
				Timestamp: time.Now(),
			})
		}
	case "turn_completed":
		m.statusBar.SetConnection("Connected")
	case "task_complete":
	case "task_failed":
		// Show error in the chat panel
		m.chatPanel.AppendMessage(ChatMessage{
			Role:      "assistant",
			Content:   "[Task failed]",
			Timestamp: time.Now(),
		})
	case "session_switched":
		m.sessionID = msg.SessionID
		m.statusBar.SetConnection(fmt.Sprintf("● Switched to session %s", client.ShortID(msg.SessionID)))
	case "session_created":
		// Load session history for the newly created or existing session
		if m.sessionID != "" {
			// Load history before appending system message
			loadSessionHistory(m, m.sessionID)
		}

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
	case "compress_completed":
		m.statusBar.SetConnection("✓ Session compressed")
		m.chatPanel.AppendMessage(ChatMessage{
			Role:      "system",
			Content:   msg.Message,
			Timestamp: time.Now(),
		})
	case "error":
		m.statusBar.SetConnection("✗ " + msg.Message)
		m.chatPanel.AppendMessage(ChatMessage{
			Role:      "system",
			Content:   msg.Message,
			Timestamp: time.Now(),
		})
		m.done = true // TUI will quit after current view render.
		m.fatalError = fmt.Errorf("%s", msg.Message)
	case "assistant_response":
		m.chatPanel.AppendMessage(ChatMessage{
			Role:      "assistant",
			Content:   msg.Message,
			Timestamp: time.Now(),
		})
	case "connection_error":
		// Ignore connection errors that are due to intentional closure (e.g., session switch)
		if strings.Contains(msg.Message, "use of closed network connection") ||
			strings.Contains(msg.Message, "websocket: close sent") ||
			strings.Contains(msg.Message, "closed network connection") {
			return
		}
		m.statusBar.SetConnection("✗ " + msg.Message)
	case "websocket_parse_error":
		m.statusBar.SetConnection("✗ " + msg.Message)
		m.events = append(m.events, EventItem{
			Type:      "error",
			Message:   msg.Message,
			Timestamp: time.Now(),
		})
	case "task_progress":
		if m.isSessionScoped(msg) {
			// Check if this is a streaming text chunk or a progress message
			msgStr := msg.Message

			if !isProgressMessage(msgStr) {
				// It's a streaming text chunk - append to chat panel's streaming message
				m.chatPanel.AppendStreamingChunk(msgStr)
			} else {
				// It's a progress message - add to events panel
				m.events = append(m.events, EventItem{
					Type:      "progress",
					Message:   msgStr,
					Timestamp: time.Now(),
				})
			}
		}
	case "thinking_progress":
		if m.isSessionScoped(msg) {
			m.events = append(m.events, EventItem{
				Type:      "thinking",
				Message:   msg.Message,
				Timestamp: time.Now(),
			})
		}
	case "agent_step":
		if m.isSessionScoped(msg) {
			m.events = append(m.events, EventItem{
				Type:      "progress",
				Message:   msg.Message,
				Timestamp: time.Now(),
			})
		}
	case "notification":
		if m.isSessionScoped(msg) {
			m.events = append(m.events, EventItem{
				Type:      "notification",
				Message:   msg.Message,
				Timestamp: time.Now(),
			})
		}
	default:
		if m.isSessionScoped(msg) {
			m.events = append(m.events, EventItem{
				Type:      msg.Event,
				Message:   msg.Message,
				Timestamp: time.Now(),
			})
		}
	}

	// Update event panel list.
	items := make([]list.Item, len(m.events))
	for i, e := range m.events {
		items[i] = e
	}
	m.eventPanel.SetItems(items)
}

// isSessionScoped returns true if the event should be displayed for the current session.
// Events with an empty SessionID are global (e.g., emergent tasks) and always shown.
func (m *Model) isSessionScoped(msg WSMessageEvent) bool {
	return msg.SessionID == "" || msg.SessionID == m.sessionID
}

// View renders the TUI layout.
func (m *Model) View() string {
	var out string

	// Status bar at top
	out += m.statusBar.Render(m.workspaceName, m.sessionID, m.spinner.View()) + "\n"

	// Two-column layout: chat on left (~70%), events on right (~30%)
	eventView := m.eventPanel.Render()
	chatWidth := int(float64(m.chatPanel.width) * 0.7)
	eventWidth := m.chatPanel.width - chatWidth
	out += lipgloss.JoinHorizontal(lipgloss.Left,
		lipgloss.NewStyle().Width(chatWidth).Render(m.chatPanel.Render()),
		lipgloss.NewStyle().Width(eventWidth).Render(eventView),
	)

	// Phase 3: help overlay on top
	if m.helpOverlay.IsShowing() {
		out += m.helpOverlay.View()
	}

	// Phase 3: session switcher modal on top (takes full width)
	if m.sessionSwitcher.IsShowing() {
		out = m.sessionSwitcher.showModal() + "\n"
	}

	if m.done {
		out += "\nSession closed.\n"
	}

	return out
}
