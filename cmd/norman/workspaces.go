package main

import (
	"context"
	"fmt"

	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/protocol"
	"github.com/urfave/cli/v3"
)

// handleWorkspacesCreate delegates workspace creation to the agent server.
func handleWorkspacesCreate(ctx context.Context, args []string, purpose string, tags []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: norman workspaces create <name> [--purpose \"text\" | -f <file>] [--tags tag1,tag2]")
	}

	name := args[0]

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	var purposePtr *string
	if purpose != "" {
		purposePtr = &purpose
	}

	body := protocol.WorkspaceCreateRequest{
		Name:    name,
		Purpose: purposePtr,
		Tags:    &tags,
	}

	rsp, err := c.CreateWorkspace(ctx, body)
	if err != nil {
		return fmt.Errorf("error creating workspace: %v", err)
	}

	parsed, err := protocol.ParseCreateWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	if parsed.StatusCode() != 201 {
		return fmt.Errorf("server error (%d): %s", parsed.StatusCode(), string(parsed.GetBody()))
	}

	ws := parsed.GetJSON201()
	fmt.Printf("Created workspace '%s' (delegated to agent server)\n", name)
	fmt.Println("(Server manages OpenShell sandbox lifecycle)")

	if ws != nil && ws.Status != nil {
		fmt.Printf("  Status: %s\n", string(*ws.Status))
	}
	return nil
}

// handleWorkspaceUpdate updates a workspace's purpose, scope, or tags.
func handleWorkspaceUpdate(ctx context.Context, args []string, purpose string, tags []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: norman workspaces update <name> [--purpose \"text\"] [--tags tag1,tag2]")
	}

	name := args[0]

	// Validate at least one update parameter is provided.
	if purpose == "" && len(tags) == 0 {
		return fmt.Errorf("at least one of --purpose or --tags must be provided")
	}

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	var purposePtr *string
	if purpose != "" {
		purposePtr = &purpose
	}

	body := protocol.UpdateWorkspaceJSONRequestBody{
		Purpose: purposePtr,
		Tags:    &tags,
	}

	rsp, err := c.UpdateWorkspace(ctx, name, body)
	if err != nil {
		return fmt.Errorf("error updating workspace '%s': %v", name, err)
	}

	parsed, err := protocol.ParseUpdateWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	if parsed.StatusCode() != 200 {
		return fmt.Errorf("server error (%d): %s", parsed.StatusCode(), string(parsed.GetBody()))
	}

	ws := parsed.GetJSON200()
	fmt.Printf("Workspace '%s' updated successfully\n", name)
	if ws != nil && ws.Purpose != nil {
		fmt.Printf("  Purpose: %s\n", *ws.Purpose)
	}
	return nil
}

// handleWorkspacesList fetches workspace info from the server.
func handleWorkspacesList(ctx context.Context, cmd *cli.Command) error {
	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	rsp, err := c.ListWorkspaces(ctx, nil)
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
		name := client.StrVal(ws.Name)
		path := client.StrVal(ws.Path)
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

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.StopWorkspaceJSONRequestBody{
		Reason: strPtr(reason),
	}

	rsp, err := c.StopWorkspace(ctx, name, body)
	if err != nil {
		return fmt.Errorf("error stopping workspace '%s': %v", name, err)
	}

	parsed, err := protocol.ParseStopWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	if parsed.StatusCode() != 200 {
		return fmt.Errorf("server error (%d): %s", parsed.StatusCode(), string(parsed.GetBody()))
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

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.RestoreWorkspaceJSONRequestBody{
		RecreateWorkspace: &recreateSandbox,
	}

	rsp, err := c.RestoreWorkspace(ctx, name, body)
	if err != nil {
		return fmt.Errorf("error restoring workspace '%s': %v", name, err)
	}

	parsed, err := protocol.ParseRestoreWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	if parsed.StatusCode() != 200 {
		return fmt.Errorf("server error (%d): %s", parsed.StatusCode(), string(parsed.GetBody()))
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
func handleWorkspacePurge(ctx context.Context, args []string, confirm bool) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: norman workspaces purge <name> --confirm")
	}

	name := args[0]

	if !confirm {
		return fmt.Errorf("'purge' requires --confirm flag to proceed (destructive operation)")
	}

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.PurgeWorkspaceJSONRequestBody{
		Confirm: true,
	}

	rsp, err := c.PurgeWorkspace(ctx, name, body)
	if err != nil {
		return fmt.Errorf("error purging workspace '%s': %v", name, err)
	}

	parsed, err := protocol.ParsePurgeWorkspaceResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	if parsed.StatusCode() != 200 {
		return fmt.Errorf("server error (%d): %s", parsed.StatusCode(), string(parsed.GetBody()))
	}

	fmt.Printf("Workspace '%s' purged successfully\n", name)
	return nil
}
