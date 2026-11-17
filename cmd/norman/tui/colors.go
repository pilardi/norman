package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// ColorScheme defines the color palette for the TUI client.
type ColorScheme struct {
	// Primary colors
	Primary        string
	PrimaryVariant string
	Secondary      string

	// Surface colors
	Surface          string
	Background       string
	OnPrimary        string
	OnSurface        string
	OnSurfaceVariant string

	// Code block colors
	CodeBg string
	CodeFg string

	// Table colors
	TableRowHover string

	// Blockquote colors
	BlockquoteBg string

	// Border colors
	BorderDefault string
	BorderFocused string

	// Status colors
	StatusConnected    string
	StatusDisconnected string
	StatusInfo         string

	// Help/Command colors
	HelpTitle  string
	HelpKey    string
	HelpDesc   string
	HelpFooter string

	// Markdown heading colors
	Heading1     string
	Heading2     string
	Heading3     string
	Heading4Plus string

	// Inline code color
	InlineCode string
}

// DefaultDarkColorScheme returns the default dark color scheme based on Material Design tokens.
func DefaultDarkColorScheme() *ColorScheme {
	return &ColorScheme{
		// Primary colors
		Primary:        "#D4AF37",
		PrimaryVariant: "#B8942E",
		Secondary:      "#A52A2A",

		// Surface colors
		Surface:          "#1C1C1C",
		Background:       "#000000",
		OnPrimary:        "#000000",
		OnSurface:        "#E5E5E5",
		OnSurfaceVariant: "#A0A0A0",

		// Code block colors
		CodeBg: "#2C2C2C",
		CodeFg: "#E5E5E5",

		// Table colors
		TableRowHover: "#2A2A2A",

		// Blockquote colors
		BlockquoteBg: "#1C1C1C",

		// Border colors
		BorderDefault: "#585858",
		BorderFocused: "#4a9eff",

		// Status colors
		StatusConnected:    "#4a9eff",
		StatusDisconnected: "#ff6b6b",
		StatusInfo:         "#a0a0a0",

		// Help/Command colors
		HelpTitle:  "#4a9eff",
		HelpKey:    "#ff6b6b",
		HelpDesc:   "#cccccc",
		HelpFooter: "#666666",

		// Markdown heading colors
		Heading1:     "#ff6b6b",
		Heading2:     "#ffa94d",
		Heading3:     "#ffd43b",
		Heading4Plus: "#8ec07c",

		// Inline code color
		InlineCode: "#a8e6cf",
	}
}

// ToLipglossStyles converts the ColorScheme to lipgloss styles for convenience.
func (cs *ColorScheme) ToLipglossStyles() map[string]lipgloss.Style {
	styles := make(map[string]lipgloss.Style)

	styles["primary"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.Primary))
	styles["primary-variant"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.PrimaryVariant))
	styles["secondary"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.Secondary))

	styles["surface"] = lipgloss.NewStyle().Background(lipgloss.Color(cs.Surface))
	styles["background"] = lipgloss.NewStyle().Background(lipgloss.Color(cs.Background))
	styles["on-primary"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.OnPrimary))
	styles["on-surface"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.OnSurface))
	styles["on-surface-variant"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.OnSurfaceVariant))

	styles["code-bg"] = lipgloss.NewStyle().Background(lipgloss.Color(cs.CodeBg))
	styles["code-fg"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.CodeFg))

	styles["table-row-hover"] = lipgloss.NewStyle().Background(lipgloss.Color(cs.TableRowHover))

	styles["blockquote-bg"] = lipgloss.NewStyle().Background(lipgloss.Color(cs.BlockquoteBg))

	styles["border-default"] = lipgloss.NewStyle().BorderForeground(lipgloss.Color(cs.BorderDefault))
	styles["border-focused"] = lipgloss.NewStyle().BorderForeground(lipgloss.Color(cs.BorderFocused))

	styles["status-connected"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.StatusConnected))
	styles["status-disconnected"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.StatusDisconnected))
	styles["status-info"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.StatusInfo))

	styles["help-title"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.HelpTitle)).Bold(true)
	styles["help-key"] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(cs.HelpKey))
	styles["help-desc"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.HelpDesc))
	styles["help-footer"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.HelpFooter))

	styles["heading-1"] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(cs.Heading1))
	styles["heading-2"] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(cs.Heading2))
	styles["heading-3"] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(cs.Heading3))
	styles["heading-4-plus"] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(cs.Heading4Plus))

	styles["inline-code"] = lipgloss.NewStyle().Foreground(lipgloss.Color(cs.InlineCode))

	return styles
}
