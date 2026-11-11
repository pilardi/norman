package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// ChatPanel is a scrollable chat message display with input textarea.
type ChatPanel struct {
	focused   bool
	width     int
	height    int
	messages  []ChatMessage
	textInput textinput.Model
}

// NewChatPanel creates a new chat panel with default styling.
func NewChatPanel() ChatPanel {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 10000
	ti.Width = 78
	return ChatPanel{
		width:     80,
		height:    30,
		textInput: ti,
	}
}

// AppendMessage adds a message to the chat stream.
func (p *ChatPanel) AppendMessage(msg ChatMessage) {
	p.messages = append(p.messages, msg)
}

// Clear removes all messages from the panel.
func (p *ChatPanel) Clear() {
	p.messages = nil
}

// Render returns the lipgloss-styled chat panel view.
func (p *ChatPanel) Render() string {
	var sb strings.Builder

	// Panel header (no inner border — outer box handles framing)
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		PaddingTop(1).
		PaddingLeft(2)

	sb.WriteString(headerStyle.Render("Chat"))
	sb.WriteString("\n")

	if len(p.messages) == 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render("No messages yet. Type a message below..."))
	} else {
		for i, msg := range p.messages {
			speaker := "Assistant"
			color := lipgloss.Color("#4a9eff") // blue for assistant
			if msg.Role == "user" {
				speaker = "You"
				color = lipgloss.Color("#ff9e64") // orange for user
			}

			ts := msg.Timestamp.Format("15:04:05")
			header := fmt.Sprintf("[%s] %s:", ts, speaker)
			sb.WriteString(lipgloss.NewStyle().Foreground(color).Render(header))
			sb.WriteString("\n")

			// RichOutput rendering (Phase 2)
			var content string
			if msg.RichOutput != nil {
				content = renderRichOutput(msg.RichOutput)
			} else {
				// Truncate long messages for display
				content = msg.Content
				if len(content) > p.width-20 {
					content = content[:p.width-23] + "..."
				}
			}
			sb.WriteString("  " + content + "\n\n")

			// Add separator between messages (but not after the last one)
			if i < len(p.messages)-1 {
				sb.WriteString(lipgloss.NewStyle().Width(p.width).Height(1).BorderForeground(lipgloss.Color("#333333")).Render(strings.Repeat("─", p.width-2)) + "\n")
			}
		}
	}

	return lipgloss.NewStyle().
		Width(p.width).
		MaxHeight(p.height).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#585858")).
		Render(sb.String())
}
