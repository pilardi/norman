package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// handleWorkspace handles workspace subcommands.
func handleWorkspaces(args []string) {
	if len(args) == 0 {
		printWorkspacesHelp()
		return
	}

	sub := args[0]
	switch sub {
	case "create":
		handleWorkspacesCreate(args[1:])
	case "list":
		handleWorkspacesList(args[1:])
	case "--help", "-h":
		printWorkspacesHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown workspace subcommand: %s\n\n", sub)
		printWorkspacesHelp()
		os.Exit(1)
	}
}

func printWorkspacesHelp() {
	fmt.Println(`workspaces — Manage workspaces

Usage: norman workspaces <subcommand> [args...]

Subcommands:
  create <name>       Create a new workspace directory
  list                List available workspaces and their sessions`)
}

// handleWorkspaceCreate delegates workspace creation to the agent server.
func handleWorkspacesCreate(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: norman workspaces create <name>")
		os.Exit(1)
	}

	name := args[0]

	// Submit workspace creation request to server
	req := map[string]string{
		"name":        name,
		"client_type": "cli",
	}

	if err := httpPost("/api/workspaces", req); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating workspace: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created workspace '%s' (delegated to agent server)\n", name)
	fmt.Println("(Server manages OpenShell sandbox lifecycle)")
}

// handleWorkspaceList fetches workspace info from the server.
func handleWorkspacesList(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`workspace list — List available workspaces and sessions

Usage: norman workspace list [--json]

Queries the agent server for workspace information.`)
		return
	}

	body, err := httpGet("/api/workspaces")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching workspaces: %v\n", err)
		os.Exit(1)
	}

	var workspaces []map[string]interface{}
	if err := json.Unmarshal(body, &workspaces); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing workspace response: %v\n", err)
		os.Exit(1)
	}

	if len(workspaces) == 0 {
		fmt.Println("No workspaces found.")
		return
	}

	fmt.Println("Available workspaces:")
	for _, w := range workspaces {
		name, _ := w["name"].(string)
		path, _ := w["path"].(string)
		fmt.Printf("  %-20s %s\n", name, path)
	}
}
