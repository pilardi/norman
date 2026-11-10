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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/norman/norman/pkg/protocol"
)

// Main launches the TUI application with the given workspace and optional session ID.
func Main(workspaceName, sessionID string) {
	model := NewModel(workspaceName)
	if sessionID != "" {
		model.sessionID = sessionID
	}

	p := tea.NewProgram(model, tea.WithAltScreen())

	_, err := p.Run()
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
		return nil
	}

	// Fetch workspaces to auto-select one.
	rsp, err := client.ListWorkspaces(ctx, nil)
	if err != nil {
		model.statusBar.SetConnection("✗ Failed to list workspaces")
		return nil
	}

	parsed, err := protocol.ParseListWorkspacesResponse(rsp)
	if err != nil {
		model.statusBar.SetConnection("✗ Parse error: " + err.Error())
		return nil
	}

	workspaces := parsed.GetJSON200()
	if workspaces == nil || len(*workspaces) == 0 {
		model.statusBar.SetConnection("⚠ No workspaces found")
		return nil
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
