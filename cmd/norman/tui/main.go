// Package tui implements the Norman TUI client using charmbracelet/bubbletea.
//
// This is a sub-package of `cmd/norman` that provides an interactive
// terminal-based chat experience with structured rendering of RichOutput,
// real-time event visualization, and keyboard-driven session management.
package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/protocol"
)

// Main launches the TUI application with the given workspace, optional session ID, and provider.
func Main(workspaceName, sessionID string, provider string) {
	model := NewModel(workspaceName, provider)
	if sessionID != "" {
		model.sessionID = sessionID
	}

	// Pre-check server connectivity before starting the TUI.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := client.GetProtocolClient()
	if err != nil || c == nil {
		p := tea.NewProgram(model, tea.WithAltScreen())
		finalModel, runErr := p.Run()
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TUI error: program was killed: %v\n", runErr)
			os.Exit(1)
		}
		if m, ok := finalModel.(*Model); ok && m.fatalError != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", m.fatalError)
			os.Exit(1)
		}
		if m, ok := finalModel.(*Model); ok && m.done && m.fatalError == nil {
			fmt.Fprintf(os.Stderr, "Error: server not reachable\n")
			os.Exit(1)
		}
		return
	}

	// Test a quick RPC call to confirm the server is actually responding.
	if _, err = c.GetVersion(ctx); err != nil {
		p := tea.NewProgram(model, tea.WithAltScreen())
		finalModel, runErr := p.Run()
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "TUI error: program was killed: %v\n", runErr)
			os.Exit(1)
		}
		if m, ok := finalModel.(*Model); ok && m.fatalError != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", m.fatalError)
			os.Exit(1)
		}
		if m, ok := finalModel.(*Model); ok && m.done && m.fatalError == nil {
			fmt.Fprintf(os.Stderr, "Error: server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: program was killed: %v\n", err)
		os.Exit(1)
	}
	if m, ok := finalModel.(*Model); ok && m.fatalError != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", m.fatalError)
		os.Exit(1)
	}
	if m, ok := finalModel.(*Model); ok && m.done {
		fmt.Fprintf(os.Stderr, "Session closed.\n")
		os.Exit(0)
	}
}

// initializeClient connects to the server and resolves the initial session/workspace.
func initializeClient(ctx context.Context, model *Model) tea.Cmd {
	model.statusBar.SetConnection("● Connecting...")

	c, err := client.GetProtocolClient()
	if err != nil {
		model.statusBar.SetConnection("✗ Connection failed: " + err.Error())
		return func() tea.Msg { return WSMessageEvent{Event: "error", Message: "Server not reachable"} }
	}

	// Fetch workspaces to auto-select one.
	rsp, err := c.ListWorkspaces(ctx, nil)
	if err != nil {
		model.statusBar.SetConnection("✗ Failed to list workspaces: " + err.Error())
		return func() tea.Msg { return WSMessageEvent{Event: "error", Message: "Failed to list workspaces"} }
	}

	parsed, err := protocol.ParseListWorkspacesResponse(rsp)
	if err != nil {
		model.statusBar.SetConnection("✗ Parse error: " + err.Error())
		return func() tea.Msg { return WSMessageEvent{Event: "error", Message: "Parse error"} }
	}

	workspaces := parsed.GetJSON200()
	if workspaces == nil || len(*workspaces) == 0 {
		model.statusBar.SetConnection("⚠ No workspaces found")
		return func() tea.Msg { return WSMessageEvent{Event: "error", Message: "No workspaces available"} }
	}

	// Select the first workspace (or the one matching args).
	var ws *protocol.Workspace
	for i := range *workspaces {
		w := &(*workspaces)[i]
		if model.initialWorkspace != "" && client.StrVal(w.Name) == model.initialWorkspace {
			ws = w
			break
		}
	}
	if ws == nil {
		ws = &(*workspaces)[0]
	}

	model.workspaceName = client.StrVal(ws.Name)
	model.statusBar.SetConnection("● Connected")
	return nil
}
