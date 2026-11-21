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
		sessID, err := CreateNewSession(workspaceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return sessionID, true
		}
		return sessID, true
	case "history":
		showHistory(sessionID, args)
	case "sessions":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: /sessions list | /sessions show <session-id>")
			return sessionID, true
		}
		switch args[0] {
		case "list":
			_ = handleSessionList(args[1:])
		case "show":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: /sessions show <session-id>")
				return sessionID, true
			}
			if err := handleSessionShow(args[1]); err != nil {
				fmt.Fprintf(os.Stderr, "Error showing session: %v\n", err)
			}
		default:
			fmt.Fprintf(os.Stderr, "Unknown sessions subcommand: %s. Use 'list' or 'show'.\n", args[0])
			return sessionID, true
		}
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
	case "compress":
		aggressive := false
		if len(args) > 0 && args[0] == "--aggressive" {
			aggressive = true
		}
		if err := handleSessionCompress(sessionID, aggressive); err != nil {
			fmt.Fprintf(os.Stderr, "Error compressing session: %v\n", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown slash-command: /%s\n", cmd)
		return sessionID, true
	}
	return sessionID, true
}
