package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// handleSessions manages sessions via CLI.
func handleSessions(args []string) {
	if len(args) == 0 {
		printSessionHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "list":
		handleSessionList(subargs)
	case "archive":
		handleSessionArchive(subargs)
	case "open":
		handleChat(args[1:])
	case "--help", "-h":
		printSessionHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown sessions subcommand: %s\n\n", subcommand)
		printSessionHelp()
		os.Exit(1)
	}
}

func printSessionHelp() {
	fmt.Println(`sessions — Manage chat sessions

Usage: norman sessions <subcommand> [args...]

Subcommands:
  open <workspace_name> [<session_id>]   Open an interactive REPL chat session
  list                          List active chat sessions
  archive <session_id>          Archive a session's transcript via POST /api/sessions/{id}`)
}

func handleSessionList(args []string) {
	if len(args) > 0 && (args[0] == "--json") {
		body, err := httpGet("/api/sessions")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(body))
		return
	}

	body, err := httpGet("/api/sessions")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
		os.Exit(1)
	}

	var sessions []map[string]interface{}
	if err := json.Unmarshal(body, &sessions); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing sessions response: %v\n", err)
		os.Exit(1)
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return
	}

	fmt.Println("Active sessions:")
	for _, s := range sessions {
		name, _ := s["name"].(string)
		status, _ := s["status"].(string)
		fmt.Printf("  %-20s status=%s\n", name, status)
	}
}

// handleSessionArchive archives a session's transcript via POST /api/sessions/{id}.
func handleSessionArchive(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Error: 'archive' requires a session_id argument")
		fmt.Fprintln(os.Stderr, "Usage: norman sessions archive <session_id>")
		os.Exit(1)
	}

	actualID, found := resolveSessionID(args[0])
	if !found {
		fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", args[0])
		os.Exit(1)
	}

	url := serverURL() + "/api/sessions/" + actualID
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating archive request: %v\n", err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Archive failed for session %s: %v\n", shortID(actualID), err)
		os.Exit(1)
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "HTTP %d archiving session %s: %s\n", resp.StatusCode, shortID(actualID), string(body))
		os.Exit(1)
	}
	fmt.Printf("Session '%s' archived successfully.\n", shortID(actualID))
}

// archiveSession archives a session's transcript via POST /api/sessions/{id}.
func archiveSession(sessionID string) {
	actualID, found := resolveSessionID(sessionID)
	if !found {
		fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", sessionID)
		return
	}

	url := serverURL() + "/api/sessions/" + actualID
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating archive request: %v\n", err)
		os.Exit(1)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Archive failed for session %s: %v\n", shortID(actualID), err)
		return
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "HTTP %d archiving session %s: %s\n", resp.StatusCode, shortID(actualID), string(body))
		return
	}
	fmt.Printf("Session '%s' archived successfully.\n", shortID(actualID))
}

// listSessions lists all active sessions via GET /api/sessions.
func listSessions() {
	body, err := httpGet("/api/sessions")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
		return
	}
	fmt.Printf("\n--- Active Sessions ---\n%s\n---\n", string(body))
}
