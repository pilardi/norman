package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/protocol"
)

// resolveSessionID resolves a session name/short ID to the actual UUID.
// Returns (resolvedID, found, error). If an error occurred (e.g., server down),
// found=false and error is non-nil — caller should surface it instead of
// treating it as "not found".
func resolveSessionID(input string) (string, bool, error) {
	c, err := client.GetProtocolClient()
	if err != nil {
		return input, false, fmt.Errorf("failed to create API client: %w", err)
	}

	rsp, err := c.ListSessions(context.Background())
	if err != nil {
		return input, false, err
	}

	parsed, err := protocol.ParseListSessionsResponse(rsp)
	if err != nil {
		return input, false, fmt.Errorf("error parsing sessions response: %w", err)
	}

	sessions := parsed.GetJSON200()
	for _, s := range *sessions {
		name := client.StrVal(s.Name)
		id := s.Id.String()
		if name == input || strings.HasPrefix(id[:8], input[:8]) {
			return id, true, nil
		}
	}
	return input, false, nil
}

// handleSessionList lists active sessions using the generated protocol client.
// Optionally filters by workspace name if a workspace arg is provided.
func handleSessionList(args []string) error {
	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.ListSessions(context.Background())
	if err != nil {
		return fmt.Errorf("error fetching sessions: %w", err)
	}

	parsed, err := protocol.ParseListSessionsResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing sessions response: %w", err)
	}

	sessions := *parsed.JSON200

	// If a workspace name is provided as the first arg, filter to that workspace.
	var workspaceFilter string
	if len(args) > 0 && args[0] != "" {
		workspaceFilter = args[0]
	}

	if len(sessions) == 0 {
		if workspaceFilter != "" {
			fmt.Printf("No active sessions for workspace '%s'.\n", workspaceFilter)
		} else {
			fmt.Println("No active sessions.")
		}
		return nil
	}

	// Build a workspace name → ID lookup map.
	wsMap := make(map[string]string)
	if resp, err := client.ListWorkspaces(context.Background(), nil); err == nil {
		parsedWS, _ := protocol.ParseListWorkspacesResponse(resp)
		workspaces := parsedWS.GetJSON200()
		for _, ws := range *workspaces {
			if ws.Name != nil {
				wsMap[*ws.Name] = ws.Id.String()
			}
		}
	}

	// If workspace filter provided, validate and resolve.
	var targetWorkspaceID string
	if workspaceFilter != "" {
		if id, ok := wsMap[workspaceFilter]; ok {
			targetWorkspaceID = id
		} else {
			fmt.Fprintf(os.Stderr, "Error: workspace '%s' not found.\n", workspaceFilter)
			return fmt.Errorf("workspace '%s' not found", workspaceFilter)
		}
	}

	// Collect matching sessions.
	var matchedSessions []*protocol.Session
	for _, s := range sessions {
		if targetWorkspaceID != "" && s.WorkspaceId != nil && *s.WorkspaceId != targetWorkspaceID {
			continue
		}
		matchedSessions = append(matchedSessions, &s)
	}

	if len(matchedSessions) == 0 {
		if workspaceFilter != "" {
			fmt.Printf("No active sessions for workspace '%s'.\n", workspaceFilter)
		} else {
			fmt.Println("No active sessions.")
		}
		return nil
	}

	// Print header with optional workspace context.
	if workspaceFilter != "" {
		fmt.Printf("Active sessions (workspace: %s):\n", workspaceFilter)
	} else {
		fmt.Println("Active sessions:")
	}

	for _, s := range matchedSessions {
		name := "(unknown)"
		if s.Name != nil {
			name = *s.Name
		}
		status := "unknown"
		if s.SessionStatus != nil {
			status = string(*s.SessionStatus)
		}

		wsName := ""
		if s.WorkspaceId != nil {
			for wsNameVal, wsID := range wsMap {
				if wsID == *s.WorkspaceId {
					wsName = wsNameVal
					break
				}
			}
		}

		fmt.Printf("  %-20s | %s  status=%s\n", name, wsName, status)
	}
	return nil
}

// handleSessionArchive archives a session's transcript via POST /api/sessions/{id}.
func handleSessionArchive(sessionID string) error {
	actualID, found, err := resolveSessionID(sessionID)
	if err != nil {
		return fmt.Errorf("resolve session '%s': %w", sessionID, err)
	}
	if !found {
		return fmt.Errorf("session '%s' not found, run 'norman sessions list' to see available sessions", sessionID)
	}

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.ArchiveSessionByID(context.Background(), actualID)
	if err != nil {
		return fmt.Errorf("archive failed for session %s: %w", client.ShortID(actualID), err)
	}

	_, err = protocol.ParseArchiveSessionByIDResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing archive response: %w", err)
	}

	fmt.Printf("Session '%s' archived successfully.\n", client.ShortID(actualID))
	return nil
}

// showHistory displays the last N messages of a session.
func showHistory(sessionID string, args []string) {
	n := 10 // default
	jsonFlag := false

	// Parse args: first arg is limit (N), remaining can include --json flag.
	for _, arg := range args {
		if arg == "--json" {
			jsonFlag = true
			continue
		}
		if num, err := fmt.Sscanf(arg, "%d", &n); err != nil || num == 0 {
			fmt.Fprintf(os.Stderr, "Invalid number: %s\n", arg)
			return
		}
	}

	// Resolve session name to actual UUID if needed.
	actualID, found, err := resolveSessionID(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving session '%s': %v\n", sessionID, err)
		return
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", sessionID)
		return
	}

	c, err := client.GetProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create client: %v\n", err)
		return
	}

	params := &protocol.SessionHistoryParams{
		Limit: intPtr(n),
	}

	rsp, err := c.SessionHistory(context.Background(), actualID, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching history: %v\n", err)
		return
	}

	parsed, err := protocol.ParseSessionHistoryResponse(rsp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing history response: %v\n", err)
		return
	}

	turns := parsed.GetJSON200()

	if jsonFlag {
		data, _ := json.MarshalIndent(*turns, "", "  ")
		fmt.Printf("%s\n", string(data))
		return
	}

	fmt.Printf("\n--- Last %d messages ---\n", n)
	for i, t := range *turns {
		role := ""
		if t.Role != nil {
			role = string(*t.Role)
		}
		content := client.StrVal(t.Content)
		ts := ""
		if t.Timestamp != nil {
			ts = t.Timestamp.Format(time.RFC3339)
		}
		if ts != "" {
			fmt.Printf("[%s] %s: %s\n", ts, role, content)
		} else {
			fmt.Printf("%s: %s\n", role, content)
		}
		if i < len(*turns)-1 {
			fmt.Println("---")
		}
	}
	fmt.Printf("---\n")
}

// listEmergentTasks lists tasks for the given session.
func listEmergentTasks(sessionID string) {
	c, err := client.GetProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create client: %v\n", err)
		return
	}

	rsp, err := c.ListTasks(context.Background(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching tasks: %v\n", err)
		return
	}

	parsed, err := protocol.ParseListTasksResponse(rsp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing tasks response: %v\n", err)
		return
	}

	tasks := parsed.GetJSON200()
	fmt.Println("Emergent scheduled tasks:")
	for _, t := range *tasks {
		fmt.Printf("  Task %s\n", t.Id.String())
	}
}
