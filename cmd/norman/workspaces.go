package main

import (
	"context"
	"fmt"

	"github.com/norman/norman/pkg/protocol"
	"github.com/urfave/cli/v3"
)

// handleWorkspacesCreate delegates workspace creation to the agent server.
func handleWorkspacesCreate(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("usage: norman workspaces create <name>")
	}

	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	path := "/home/user/workspaces/" + name
	body := protocol.WorkspaceCreateRequest{
		Name: name,
		Path: path,
	}

	rsp, err := client.CreateWorkspace(ctx, body)
	if err != nil {
		return fmt.Errorf("error creating workspace: %v", err)
	}

	parsed, err := protocol.ParseCreateWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	ws := parsed.GetJSON201()
	fmt.Printf("Created workspace '%s' (delegated to agent server)\n", name)
	fmt.Println("(Server manages OpenShell sandbox lifecycle)")

	if ws != nil && ws.Status != nil {
		fmt.Printf("  Status: %s\n", string(*ws.Status))
	}
	return nil
}

// handleWorkspacesList fetches workspace info from the server.
func handleWorkspacesList(ctx context.Context, c *cli.Command) error {
	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	rsp, err := client.ListWorkspaces(ctx, nil)
	if err != nil {
		return fmt.Errorf("error fetching workspaces: %v", err)
	}

	parsed, err := protocol.ParseListWorkspacesResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	workspaces := parsed.GetJSON200()
	if workspaces == nil || len(*workspaces) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	fmt.Println("Available workspaces:")
	for _, ws := range *workspaces {
		name := strVal(ws.Name)
		path := strVal(ws.Path)
		status := ""
		if ws.Status != nil {
			status = string(*ws.Status)
		}
		fmt.Printf("  %-20s %s [%s]\n", name, path, status)
	}
	return nil
}

// handleWorkspaceStop stops a workspace (frees sandbox, keeps data).
func handleWorkspaceStop(ctx context.Context, args []string) error {
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

	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.StopWorkspaceJSONRequestBody{
		Reason: strPtr(reason),
	}

	rsp, err := client.StopWorkspace(ctx, name, body)
	if err != nil {
		return fmt.Errorf("error stopping workspace '%s': %v", name, err)
	}

	parsed, err := protocol.ParseStopWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	ws := parsed.GetJSON200()
	status := ""
	if ws != nil && ws.Status != nil {
		status = string(*ws.Status)
	}
	fmt.Printf("Workspace '%s' stopped successfully (status=%s)\n", name, status)
	return nil
}

// handleWorkspaceRestore restores a stopped workspace to active.
func handleWorkspaceRestore(ctx context.Context, args []string) error {
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

	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.RestoreWorkspaceJSONRequestBody{
		RecreateSandbox: &recreateSandbox,
	}

	rsp, err := client.RestoreWorkspace(ctx, name, body)
	if err != nil {
		return fmt.Errorf("error restoring workspace '%s': %v", name, err)
	}

	parsed, err := protocol.ParseRestoreWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	ws := parsed.GetJSON200()
	status := ""
	if ws != nil && ws.Status != nil {
		status = string(*ws.Status)
	}
	fmt.Printf("Workspace '%s' restored to active (status=%s)\n", name, status)
	return nil
}

// handleWorkspacePurge permanently deletes a workspace.
func handleWorkspacePurge(ctx context.Context, args []string) error {
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

	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.PurgeWorkspaceJSONRequestBody{
		Confirm: true,
	}

	rsp, err := client.PurgeWorkspace(ctx, name, body)
	if err != nil {
		return fmt.Errorf("error purging workspace '%s': %v", name, err)
	}

	_, err = protocol.ParsePurgeWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	fmt.Printf("Workspace '%s' purged successfully\n", name)
	return nil
}
