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
	"github.com/norman/norman/pkg/protocol"
)

// Main launches the TUI application with the given workspace and optional session ID.
func Main(workspaceName, sessionID string) {
	model := NewModel(workspaceName)
	if sessionID != "" {
		model.sessionID = sessionID
	}

	// Pre-check server connectivity before starting the TUI.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := getProtocolClient()
	if err != nil || client == nil {
		model.statusBar.SetConnection("✗ Server not reachable")
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err = p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Test a quick RPC call to confirm the server is actually responding.
	if _, err = client.GetVersion(ctx); err != nil {
		model.statusBar.SetConnection("✗ Server error: " + err.Error())
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err = p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

// initializeClient connects to the server and resolves the initial session/workspace.
func initializeClient(ctx context.Context, model *Model) tea.Cmd {
	model.statusBar.SetConnection("● Connecting...")

	client, err := getProtocolClient()
	if err != nil {
		model.statusBar.SetConnection("✗ Connection failed: " + err.Error())
		return func() tea.Msg { return WSMessageEvent{Event: "error", Message: "Server not reachable"} }
	}

	// Fetch workspaces to auto-select one.
	rsp, err := client.ListWorkspaces(ctx, nil)
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
		if model.initialWorkspace != "" && strVal(w.Name) == model.initialWorkspace {
			ws = w
			break
		}
	}
	if ws == nil {
		ws = &(*workspaces)[0]
	}

	model.workspaceName = strVal(ws.Name)
	model.statusBar.SetConnection("● Connected")
	return nil
}
