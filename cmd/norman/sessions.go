package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/norman/norman/pkg/protocol"
)

// shortID returns a truncated UUID representation (first 8 chars).
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// handleSessionList lists active sessions using the generated protocol client.
func handleSessionList(args []string) error {
	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.ListSessions(context.Background())
	if err != nil {
		return fmt.Errorf("error fetching sessions: %w", err)
	}

	parsed, err := protocol.ParseListSessionsResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing sessions response: %w", err)
	}

	if parsed.JSON200 == nil || len(*parsed.JSON200) == 0 {
		fmt.Println("No active sessions.")
		return nil
	}

	sessions := *parsed.JSON200
	fmt.Println("Active sessions:")
	for _, s := range sessions {
		name := "(unknown)"
		if s.Name != nil {
			name = *s.Name
		}
		status := "unknown"
		if s.SessionStatus != nil {
			status = string(*s.SessionStatus)
		}
		fmt.Printf("  %-20s status=%s\n", name, status)
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

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.ArchiveSessionByID(context.Background(), actualID)
	if err != nil {
		return fmt.Errorf("archive failed for session %s: %w", shortID(actualID), err)
	}

	_, err = protocol.ParseArchiveSessionByIDResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing archive response: %w", err)
	}

	fmt.Printf("Session '%s' archived successfully.\n", shortID(actualID))
	return nil
}

// listSessions lists all active sessions via GET /api/sessions.
func listSessions() {
	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create API client: %v\n", err)
		return
	}

	resp, err := client.ListSessions(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
		return
	}

	parsed, err := protocol.ParseListSessionsResponse(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing sessions response: %v\n", err)
		return
	}

	if parsed.JSON200 == nil || len(*parsed.JSON200) == 0 {
		fmt.Println("--- Active Sessions ---\n(No active sessions)\n---")
		return
	}

	sessions := *parsed.JSON200
	fmt.Println("--- Active Sessions ---")
	for _, s := range sessions {
		name := "(unknown)"
		if s.Name != nil {
			name = *s.Name
		}
		status := "unknown"
		if s.SessionStatus != nil {
			status = string(*s.SessionStatus)
		}
		fmt.Printf("  %-20s status=%s\n", name, status)
	}
	fmt.Println("---")
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

	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create client: %v\n", err)
		return
	}

	params := &protocol.SessionHistoryParams{
		Limit: intPtr(n),
	}

	rsp, err := client.SessionHistory(context.Background(), actualID, params)
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
		content := strVal(t.Content)
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
