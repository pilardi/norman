package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ChatPanel is a scrollable chat message display with input textarea.
type ChatPanel struct {
	focused   bool
	width     int
	height    int
	messages  []ChatMessage
	viewport  viewport.Model
	textInput textarea.Model
}

// NewChatPanel creates a new chat panel with default settings.
func NewChatPanel() ChatPanel {
	ti := textarea.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 10000
	ti.SetWidth(78)
	ti.ShowLineNumbers = false

	viewportModel := viewport.New(78, 30)
	viewportModel.Style = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#585858"))

	return ChatPanel{
		width:     80,
		height:    30,
		textInput: ti,
		viewport:  viewportModel,
	}
}

// setStyles applies focused/blurred border styles to the textarea and viewport.
func (p *ChatPanel) setStyles() {
	if p.focused {
		p.textInput.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#4a9eff"))
		p.viewport.Style.BorderForeground(lipgloss.Color("#4a9eff"))
	} else {
		p.textInput.BlurredStyle.Base = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#585858"))
		p.viewport.Style.BorderForeground(lipgloss.Color("#585858"))
	}
}

// updateViewportHeight adjusts the viewport to leave room for the header and input.
func (p *ChatPanel) updateViewportHeight() {
	// Reserve 1 line for header + 2 lines for text input (prompt + cursor line)
	vh := p.height - 3
	if vh < 1 {
		vh = 1
	}
	p.viewport.Style = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#585858")).MaxHeight(vh)
	// Also set a fixed height so the viewport always takes exactly this much space,
	// guaranteeing the text input is visible below it.
	p.viewport.Height = vh
}

// AppendMessage adds a message to the chat stream and updates the viewport.
func (p *ChatPanel) AppendMessage(msg ChatMessage) {
	p.messages = append(p.messages, msg)
	p.updateViewport()
}

// Clear removes all messages from the panel.
func (p *ChatPanel) Clear() {
	p.messages = nil
	p.viewport.SetContent("")
}

// Init implements tea.Model interface.
func (p *ChatPanel) Init() tea.Cmd {
	// Set initial empty-state content and adjust height to avoid visibleLines panic.
	p.updateViewport()
	return nil
}

// Update handles messages for this panel.
func (p *ChatPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if p.focused {
		p.textInput, cmd = p.textInput.Update(msg)
		return p, cmd
	}

	// Pass mouse/key events to viewport when not focused on input
	switch msg := msg.(type) {
	case tea.MouseMsg:
		p.viewport, cmd = p.viewport.Update(msg)
		return p, cmd
	default:
		p.viewport, cmd = p.viewport.Update(msg)
		return p, cmd
	}
}

func (p *ChatPanel) updateViewport() {
	var lines []string
	if len(p.messages) == 0 {
		lines = append(lines, "Type a message below to start chatting.")
		lines = append(lines, "")
		lines = append(lines, "Keyboard: Tab = toggle focus | Ctrl+L = clear | Ctrl+H = help | Ctrl+S = sessions | / = command mode")
	} else {
		for _, msg := range p.messages {
			speaker := "Assistant"
			if msg.Role == "user" {
				speaker = "You"
			}
			ts := msg.Timestamp.Format("15:04:05")
			lines = append(lines, "["+ts+"] "+speaker+":")

			// Handle RichOutput rendering
			if msg.RichOutput != nil {
				lines = append(lines, renderRichOutput(msg.RichOutput))
			} else {
				content := msg.Content
				// Use the layout-allocated width (70% of total), not full terminal width.
				allocatedWidth := int(float64(p.width) * 0.7)
				maxWidth := allocatedWidth - 23
				if maxWidth < 0 {
					maxWidth = 0
				}
				// Wrap content to fill the text area instead of truncating.
				content = wrapString(content, maxWidth)
				lines = append(lines, "  "+content)
			}
			lines = append(lines, "")
		}
	}
	content := joinLines(lines)
	p.viewport.SetContent(content)
	// Scroll to bottom of visible area.
	p.viewport.YOffset = max(0, strings.Count(content, "\n")-p.height)
}

// wrapString wraps a long string to fit within maxWidth characters,
// breaking on word boundaries (spaces).
func wrapString(s string, maxWidth int) string {
	if maxWidth <= 0 || len(s) == 0 {
		return s
	}
	var result strings.Builder
	words := strings.Fields(s)
	var line strings.Builder
	for _, w := range words {
		if line.Len()+len(w)+1 > maxWidth && line.Len() > 0 {
			result.WriteString(line.String())
			result.WriteByte('\n')
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(w)
	}
	if line.Len() > 0 {
		if result.Len() > 0 {
			result.WriteByte('\n')
		}
		result.WriteString(line.String())
	}
	return result.String()
}

func joinLines(lines []string) string {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Render returns the chat panel view.
func (p *ChatPanel) Render() string {
	header := lipgloss.NewStyle().Bold(true).PaddingTop(1).PaddingLeft(2).Render("Chat")
	return lipgloss.JoinVertical(lipgloss.Left, header, p.viewport.View(), p.textInput.View())
}

// Focus sets the textarea to focused state and updates styles.
func (p *ChatPanel) Focus() tea.Cmd {
	p.focused = true
	p.setStyles()
	return p.textInput.Focus()
}

// Blur removes focus from the textarea.
func (p *ChatPanel) Blur() {
	p.focused = false
	p.textInput.Blur()
	p.setStyles()
}

// View implements tea.Model interface.
func (p *ChatPanel) View() string {
	return p.Render()
}
