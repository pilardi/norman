package tui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/norman/norman/pkg/protocol"
)

// ChatPanel is a scrollable chat message display with input textarea.
type ChatPanel struct {
	focused          bool
	width            int
	height           int
	messages         []ChatMessage
	viewport         viewport.Model
	textInput        textarea.Model
	streamingMessage *ChatMessage // Accumulates streaming chunks from the LLM
}

// NewChatPanel creates a new chat panel with default settings.
func NewChatPanel() ChatPanel {
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()
	ti := textarea.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 10000
	ti.SetWidth(78)
	ti.ShowLineNumbers = false

	viewportModel := viewport.New(78, 30)
	viewportModel.Style = styles["border-default"].Border(lipgloss.RoundedBorder())

	return ChatPanel{
		width:     80,
		height:    30,
		textInput: ti,
		viewport:  viewportModel,
	}
}

// setStyles applies focused/blurred border styles to the textarea and viewport.
func (p *ChatPanel) setStyles() {
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()
	if p.focused {
		p.textInput.FocusedStyle.Base = styles["border-focused"].Border(lipgloss.RoundedBorder())
		p.viewport.Style = styles["border-focused"].Border(lipgloss.RoundedBorder())
	} else {
		p.textInput.BlurredStyle.Base = styles["border-default"].Border(lipgloss.RoundedBorder())
		p.viewport.Style = styles["border-default"].Border(lipgloss.RoundedBorder())
	}
}

// updateViewportHeight adjusts the viewport to leave room for the header and input.
func (p *ChatPanel) updateViewportHeight() {
	// Reserve 1 line for header + 2 lines for text input (prompt + cursor line)
	vh := p.height - 3
	if vh < 1 {
		vh = 1
	}
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()
	p.viewport.Style = styles["border-default"].Border(lipgloss.RoundedBorder()).MaxHeight(vh)
	// Also set a fixed height so the viewport always takes exactly this much space,
	// guaranteeing the text input is visible below it.
	p.viewport.Height = vh
}

// AppendMessage adds a message to the chat stream and updates the viewport.
func (p *ChatPanel) AppendMessage(msg ChatMessage) {
	// If there's a streaming message, finish it as a regular message
	if p.streamingMessage != nil {
		p.messages = append(p.messages, *p.streamingMessage)
		p.streamingMessage = nil
	}
	p.messages = append(p.messages, msg)
	p.updateViewport()
}

// AppendStreamingChunk appends a chunk to the streaming message, or creates a new one if none exists.
func (p *ChatPanel) AppendStreamingChunk(chunk string) {
	if p.streamingMessage == nil {
		p.streamingMessage = &ChatMessage{
			Role:      "assistant",
			Content:   chunk,
			Timestamp: time.Now(),
		}
	} else {
		p.streamingMessage.Content += chunk
	}
	p.updateStreamingViewport()
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

func (p *ChatPanel) updateStreamingViewport() {
	if p.streamingMessage == nil {
		return
	}

	// Detect content type of the accumulated streaming content
	_, renderHint := detectContentType(p.streamingMessage.Content)

	var lines []string
	speaker := "Assistant"
	ts := p.streamingMessage.Timestamp.Format("15:04:05")
	lines = append(lines, "["+ts+"] "+speaker+":")

	// Render based on detected content type
	switch renderHint {
	case protocol.Json:
		// For JSON, render as JSON tree
		var jsonMap map[string]interface{}
		if err := json.Unmarshal([]byte(p.streamingMessage.Content), &jsonMap); err == nil {
			lines = append(lines, renderJSONTree(&jsonMap))
		} else {
			// Fallback to text if JSON is incomplete
			content := p.streamingMessage.Content
			allocatedWidth := int(float64(p.width) * 0.7)
			maxWidth := allocatedWidth - 23
			if maxWidth < 0 {
				maxWidth = 0
			}
			content = wrapString(content, maxWidth)
			lines = append(lines, "  "+content)
		}
	case protocol.Html:
		// For HTML, render as HTML text
		content := p.streamingMessage.Content
		allocatedWidth := int(float64(p.width) * 0.7)
		maxWidth := allocatedWidth - 23
		if maxWidth < 0 {
			maxWidth = 0
		}
		content = wrapString(content, maxWidth)
		lines = append(lines, "  "+content)
	case protocol.Markdown:
		// For markdown, render with markdown renderer
		contentMap := map[string]interface{}{
			"content": p.streamingMessage.Content,
		}
		lines = append(lines, renderMarkdown(&contentMap))
	case protocol.Text:
		// For text, wrap and display as plain text
		content := p.streamingMessage.Content
		allocatedWidth := int(float64(p.width) * 0.7)
		maxWidth := allocatedWidth - 23
		if maxWidth < 0 {
			maxWidth = 0
		}
		content = wrapString(content, maxWidth)
		lines = append(lines, "  "+content)
	default:
		// Fallback to text
		content := p.streamingMessage.Content
		allocatedWidth := int(float64(p.width) * 0.7)
		maxWidth := allocatedWidth - 23
		if maxWidth < 0 {
			maxWidth = 0
		}
		content = wrapString(content, maxWidth)
		lines = append(lines, "  "+content)
	}

	content := joinLines(lines)
	p.viewport.SetContent(content)
	// Scroll to bottom of visible area.
	p.viewport.YOffset = max(0, strings.Count(content, "\n")-p.height)
}

func (p *ChatPanel) updateViewport() {
	var lines []string
	if len(p.messages) == 0 && p.streamingMessage == nil {
		lines = append(lines, "Type a message below to start chatting.")
		lines = append(lines, "")
		lines = append(lines, "Keyboard: Tab = toggle focus | Ctrl+L = clear | Ctrl+H = help | Ctrl+S = sessions | / = command mode")
	} else {
		// First, render streaming message if it exists
		if p.streamingMessage != nil {
			_, renderHint := detectContentType(p.streamingMessage.Content)

			speaker := "Assistant"
			ts := p.streamingMessage.Timestamp.Format("15:04:05")
			lines = append(lines, "["+ts+"] "+speaker+":")

			switch renderHint {
			case protocol.Json:
				var jsonMap map[string]interface{}
				if err := json.Unmarshal([]byte(p.streamingMessage.Content), &jsonMap); err == nil {
					lines = append(lines, renderJSONTree(&jsonMap))
				} else {
					content := p.streamingMessage.Content
					allocatedWidth := int(float64(p.width) * 0.7)
					maxWidth := allocatedWidth - 23
					if maxWidth < 0 {
						maxWidth = 0
					}
					content = wrapString(content, maxWidth)
					lines = append(lines, "  "+content)
				}
			case protocol.Html:
				content := p.streamingMessage.Content
				allocatedWidth := int(float64(p.width) * 0.7)
				maxWidth := allocatedWidth - 23
				if maxWidth < 0 {
					maxWidth = 0
				}
				content = wrapString(content, maxWidth)
				lines = append(lines, "  "+content)
			case protocol.Markdown:
				contentMap := map[string]interface{}{
					"content": p.streamingMessage.Content,
				}
				lines = append(lines, renderMarkdown(&contentMap))
			case protocol.Text:
				fallthrough
			default:
				content := p.streamingMessage.Content
				allocatedWidth := int(float64(p.width) * 0.7)
				maxWidth := allocatedWidth - 23
				if maxWidth < 0 {
					maxWidth = 0
				}
				content = wrapString(content, maxWidth)
				lines = append(lines, "  "+content)
			}
			lines = append(lines, "")
		}

		// Then, render regular messages
		for _, msg := range p.messages {
			speaker := "Assistant"
			if msg.Role == "user" {
				speaker = "You"
			}
			ts := msg.Timestamp.Format("15:04:05")
			lines = append(lines, "["+ts+"] "+speaker+":")

			// Render content using markdown renderer to properly handle code blocks
			renderedContent := renderMarkdownString(msg.Content)
			lines = append(lines, renderedContent)
			lines = append(lines, "")
		}
	}
	content := joinLines(lines)
	p.viewport.SetContent(content)
	// Scroll to bottom of visible area.
	p.viewport.YOffset = max(0, strings.Count(content, "\n")-p.height)
}

// wrapString wraps a long string to fit within maxWidth characters,
// breaking on word boundaries (spaces), while preserving newlines.
func wrapString(s string, maxWidth int) string {
	if maxWidth <= 0 || len(s) == 0 {
		return s
	}

	// Process line by line to preserve newlines
	lines := strings.Split(s, "\n")
	var result strings.Builder

	for i, line := range lines {
		if i > 0 {
			result.WriteByte('\n')
		}

		// Wrap this line
		words := strings.Fields(line)
		var lineBuilder strings.Builder
		for _, w := range words {
			if lineBuilder.Len()+len(w)+1 > maxWidth && lineBuilder.Len() > 0 {
				result.WriteString(lineBuilder.String())
				result.WriteByte('\n')
				lineBuilder.Reset()
			}
			if lineBuilder.Len() > 0 {
				lineBuilder.WriteByte(' ')
			}
			lineBuilder.WriteString(w)
		}
		if lineBuilder.Len() > 0 {
			result.WriteString(lineBuilder.String())
		}
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
	cs := DefaultDarkColorScheme()
	styles := cs.ToLipglossStyles()
	header := styles["primary"].Bold(true).PaddingTop(1).PaddingLeft(2).Render("Chat")
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
