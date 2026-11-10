package main

import (
	"fmt"
	"os"
	"strings"
)

// parseSlashCommand checks if a line is a slash-command and returns the command + args.
// Returns (command, args, isSlash) — only slash-commands trigger local actions.
func parseSlashCommand(line string) (string, []string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return "", nil, false
	}
	parts := strings.Fields(line[1:]) // strip leading '/' and split
	if len(parts) == 0 {
		return "", nil, false
	}
	return parts[0], parts[1:], true
}

// dispatchSlashCommand routes a parsed slash-command to its handler.
// Returns (newSessionID, handled). If handled is false, the caller should
// treat the line as a normal message. If newSessionID differs from the input,
// the session has been switched or a new one created.
func dispatchSlashCommand(cmd string, args []string, sessionID string, workspaceName string) (string, bool) {
	switch cmd {
	case "help":
		fmt.Fprintln(os.Stderr, chatHelpText)
	case "list", "tasks":
		listEmergentTasks(sessionID)
	case "close":
		fmt.Println("Closing session...")
	case "new":
		fmt.Printf("Creating new session for workspace '%s'...\n", workspaceName)
		return CreateNewSession(workspaceName), true
	case "history":
		showHistory(sessionID, args)
	case "sessions":
		_ = handleSessionList([]string{})
	case "switch":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: /switch <session-id>")
			return sessionID, true
		}
		resolvedID, found, err := resolveSessionID(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving session '%s': %v\n", args[0], err)
			return sessionID, true
		}
		if !found {
			fmt.Fprintf(os.Stderr, "Error: session '%s' not found.\n", args[0])
			return sessionID, true
		}
		fmt.Printf("Switched to session '%s'\n", args[0])
		return resolvedID, true
	case "archive":
		id := args[0]
		if len(args) == 0 {
			// Auto-archive current session.
			id = sessionID
		}
		if err := handleSessionArchive(id); err != nil {
			fmt.Fprintf(os.Stderr, "Error archiving session: %v\n", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown slash-command: /%s\n", cmd)
		return sessionID, true
	}
	return sessionID, true
}
