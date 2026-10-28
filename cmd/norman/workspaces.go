package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// handleWorkspaces handles workspace subcommands.
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
	case "stop":
		handleWorkspaceStop(args[1:])
	case "restore":
		handleWorkspaceRestore(args[1:])
	case "archive":
		handleWorkspaceArchive(args[1:])
	case "purge":
		handleWorkspacePurge(args[1:])
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
  list                List available workspaces and their sessions
  stop <name>         Stop a workspace (free sandbox, keep data)
  restore <name>      Restore a stopped/archived workspace to active
  archive <name>      Archive a workspace (immutable snapshot, free sandbox)
  purge <name>        Permanently delete a workspace (requires --confirm)`)
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
		status, _ := w["status"].(string)
		fmt.Printf("  %-20s %s [%s]\n", name, path, status)
	}
}

// --- Lifecycle Subcommands ---

// handleWorkspaceStop stops a workspace (frees sandbox, keeps data).
func handleWorkspaceStop(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Error: 'stop' requires a workspace name argument")
		fmt.Fprintln(os.Stderr, "Usage: norman workspaces stop <name> [--reason \"text\"]")
		os.Exit(1)
	}

	name := args[0]
	reason := ""

	// Parse optional --reason flag
	for i := 1; i < len(args); i++ {
		if args[i] == "--reason" && i+1 < len(args) {
			reason = args[i+1]
			i++
		}
	}

	reqBody := map[string]interface{}{
		"reason": reason,
	}

	body, err := httpPostJSON("/api/workspaces/"+name+"/stop", reqBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping workspace '%s': %v\n", name, err)
		os.Exit(1)
	}

	var ws map[string]interface{}
	if err := json.Unmarshal(body, &ws); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	status, _ := ws["status"].(string)
	fmt.Printf("Workspace '%s' stopped successfully (status=%s)\n", name, status)
}

// handleWorkspaceRestore restores a stopped/archived workspace to active.
func handleWorkspaceRestore(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Error: 'restore' requires a workspace name argument")
		fmt.Fprintln(os.Stderr, "Usage: norman workspaces restore <name> [--recreate-sandbox]")
		os.Exit(1)
	}

	name := args[0]
	recreateSandbox := true

	// Parse optional --recreate-sandbox flag (default is true)
	for i := 1; i < len(args); i++ {
		if args[i] == "--no-recreate" {
			recreateSandbox = false
		}
	}

	reqBody := map[string]interface{}{
		"recreate_sandbox": recreateSandbox,
	}

	body, err := httpPostJSON("/api/workspaces/"+name+"/restore", reqBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error restoring workspace '%s': %v\n", name, err)
		os.Exit(1)
	}

	var ws map[string]interface{}
	if err := json.Unmarshal(body, &ws); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	status, _ := ws["status"].(string)
	fmt.Printf("Workspace '%s' restored to active (status=%s)\n", name, status)
}

// handleWorkspaceArchive archives a workspace (immutable snapshot, frees sandbox).
func handleWorkspaceArchive(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Error: 'archive' requires a workspace name argument")
		fmt.Fprintln(os.Stderr, "Usage: norman workspaces archive <name> [--no-snapshot]")
		os.Exit(1)
	}

	name := args[0]
	snapshotWiki := true

	// Parse optional --no-snapshot flag (default is true)
	for i := 1; i < len(args); i++ {
		if args[i] == "--no-snapshot" {
			snapshotWiki = false
		}
	}

	reqBody := map[string]interface{}{
		"snapshot_wiki": snapshotWiki,
	}

	body, err := httpPostJSON("/api/workspaces/"+name+"/archive", reqBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error archiving workspace '%s': %v\n", name, err)
		os.Exit(1)
	}

	var ws map[string]interface{}
	if err := json.Unmarshal(body, &ws); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	status, _ := ws["status"].(string)
	fmt.Printf("Workspace '%s' archived successfully (status=%s)\n", name, status)
}

// handleWorkspacePurge permanently deletes a workspace.
func handleWorkspacePurge(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Error: 'purge' requires a workspace name argument")
		fmt.Fprintln(os.Stderr, "Usage: norman workspaces purge <name> [--confirm]")
		os.Exit(1)
	}

	name := args[0]
	confirm := false

	// Parse optional --confirm flag
	for i := 1; i < len(args); i++ {
		if args[i] == "--confirm" {
			confirm = true
		}
	}

	if !confirm {
		fmt.Fprintf(os.Stderr, "Error: 'purge' requires --confirm flag to proceed (destructive operation)\n")
		os.Exit(1)
	}

	reqBody := map[string]interface{}{
		"confirm": true,
	}

	body, err := httpPostJSON("/api/workspaces/"+name+"/purge", reqBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error purging workspace '%s': %v\n", name, err)
		os.Exit(1)
	}

	var ws map[string]interface{}
	if err := json.Unmarshal(body, &ws); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	status, _ := ws["status"].(string)
	fmt.Printf("Workspace '%s' purged successfully (status=%s)\n", name, status)
}

// resolveWorkspaceName resolves a workspace name/short ID to the actual UUID.
func resolveWorkspaceName(input string) (string, bool) {
	body, err := httpGet("/api/workspaces")
	if err != nil {
		return input, false // fall back to original
	}
	var workspaces []map[string]interface{}
	if err := json.Unmarshal(body, &workspaces); err != nil {
		return input, false
	}
	for _, w := range workspaces {
		name, _ := w["name"].(string)
		id, _ := w["id"].(string)
		if name == input || strings.HasPrefix(id[:8], input[:8]) {
			return id, true
		}
	}
	return input, false
}
