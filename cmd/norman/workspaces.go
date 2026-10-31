package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/urfave/cli/v3"
)

// handleWorkspacesCreate delegates workspace creation to the agent server.
func handleWorkspacesCreate(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("usage: norman workspaces create <name>")
	}

	req := map[string]string{
		"name":        name,
		"client_type": "cli",
	}

	if err := httpPost("/api/workspaces", req); err != nil {
		return fmt.Errorf("error creating workspace: %v", err)
	}

	fmt.Printf("Created workspace '%s' (delegated to agent server)\n", name)
	fmt.Println("(Server manages OpenShell sandbox lifecycle)")
	return nil
}

// handleWorkspacesList fetches workspace info from the server.
func handleWorkspacesList(ctx context.Context, c *cli.Command) error {
	body, err := httpGet("/api/workspaces")
	if err != nil {
		return fmt.Errorf("error fetching workspaces: %v", err)
	}

	var workspaces []map[string]interface{}
	if err := json.Unmarshal(body, &workspaces); err != nil {
		return fmt.Errorf("error parsing workspace response: %v", err)
	}

	if len(workspaces) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	fmt.Println("Available workspaces:")
	for _, w := range workspaces {
		name, _ := w["name"].(string)
		path, _ := w["path"].(string)
		status, _ := w["status"].(string)
		fmt.Printf("  %-20s %s [%s]\n", name, path, status)
	}
	return nil
}

// handleWorkspaceStop stops a workspace (frees sandbox, keeps data).
func handleWorkspaceStop(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: norman workspaces stop <name> [--reason \"text\"]")
	}

	name := args[0]
	reason := ""

	for i := 1; i < len(args); i++ {
		if args[i] == "--reason" && i+1 < len(args) {
			reason = args[i+1]
			i++
		}
	}

	reqBody := map[string]interface{}{"reason": reason}
	body, err := httpPostJSON("/api/workspaces/"+name+"/stop", reqBody)
	if err != nil {
		return fmt.Errorf("error stopping workspace '%s': %v", name, err)
	}

	var ws map[string]interface{}
	if err := json.Unmarshal(body, &ws); err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	status, _ := ws["status"].(string)
	fmt.Printf("Workspace '%s' stopped successfully (status=%s)\n", name, status)
	return nil
}

// handleWorkspaceRestore restores a stopped workspace to active.
func handleWorkspaceRestore(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: norman workspaces restore <name> [--no-recreate]")
	}

	name := args[0]
	recreateSandbox := true

	for i := 1; i < len(args); i++ {
		if args[i] == "--no-recreate" {
			recreateSandbox = false
		}
	}

	reqBody := map[string]interface{}{"recreate_sandbox": recreateSandbox}
	body, err := httpPostJSON("/api/workspaces/"+name+"/restore", reqBody)
	if err != nil {
		return fmt.Errorf("error restoring workspace '%s': %v", name, err)
	}

	var ws map[string]interface{}
	if err := json.Unmarshal(body, &ws); err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	status, _ := ws["status"].(string)
	fmt.Printf("Workspace '%s' restored to active (status=%s)\n", name, status)
	return nil
}

// handleWorkspacePurge permanently deletes a workspace.
func handleWorkspacePurge(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: norman workspaces purge <name> [--confirm]")
	}

	name := args[0]
	confirm := false

	for i := 1; i < len(args); i++ {
		if args[i] == "--confirm" {
			confirm = true
		}
	}

	if !confirm {
		return fmt.Errorf("'purge' requires --confirm flag to proceed (destructive operation)")
	}

	reqBody := map[string]interface{}{"confirm": true}
	body, err := httpPostJSON("/api/workspaces/"+name+"/purge", reqBody)
	if err != nil {
		return fmt.Errorf("error purging workspace '%s': %v", name, err)
	}

	var ws map[string]interface{}
	if err := json.Unmarshal(body, &ws); err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	status, _ := ws["status"].(string)
	fmt.Printf("Workspace '%s' purged successfully (status=%s)\n", name, status)
	return nil
}
