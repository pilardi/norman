// Package main implements the Norman CLI client.
package main

import (
	"fmt"
	"os"

	"github.com/norman/norman/pkg/config"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	// Check for -v/--verbose flag anywhere in args (before or after command)
	verbose = isVerbose()

	// Parse -p/--provider NAME from args (can appear before the command).
	// We check os.Args[1:] to handle cases like "norman -p unsloth sessions open".
	_, provider := parseProviderFlag(os.Args[1:])
	if provider != "" {
		selectedProvider = provider
	}

	switch command {
	case "config":
		handleConfig(args)
	case "workspaces":
		handleWorkspaces(args)
	case "skills":
		handleSkills(args)
	case "sessions":
		handleSessions(args)
	case "open", "chat":
		handleChat(args[1:])
	case "providers":
		handleProviders(args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(config.ClientHelpText())
}

// verbose controls whether debug output is printed to stderr.
var verbose bool

// selectedProvider overrides the default LLM provider for a session.
var selectedProvider string

// isVerbose checks if verbose/debug mode was requested.
func isVerbose() bool {
	for _, arg := range os.Args[1:] {
		if arg == "-v" || arg == "--verbose" {
			return true
		}
	}
	return false
}

// parseProviderFlag extracts -p/--provider NAME from args and returns remaining args.
func parseProviderFlag(args []string) ([]string, string) {
	for i := 0; i < len(args)-1; i++ {
		if (args[i] == "-p" || args[i] == "--provider") && i+1 < len(args) {
			provider := args[i+1]
			// Remove the flag and its value from args
			remaining := make([]string, 0, len(args)-2)
			remaining = append(remaining, args[:i]...)
			remaining = append(remaining, args[i+2:]...)
			return remaining, provider
		}
	}
	return args, ""
}

// serverURL returns the configured server base URL.
// Priority: NORMAN_SERVER_URL > NORMAN_HOST/NORMAN_PORT env vars > config.yaml defaults > hardcoded fallback.
// If NORMAN_SERVER_URL is set, it takes full precedence (no HOST/PORT needed).
func serverURL() string {
	// Primary: use explicit server URL if provided
	serverURL := os.Getenv("NORMAN_SERVER_URL")
	if serverURL != "" {
		return serverURL
	}

	// Fallback: derive from HOST + PORT env vars or config.yaml
	host := os.Getenv("NORMAN_HOST")
	portStr := os.Getenv("NORMAN_PORT")

	if host == "" || portStr == "" {
		// Try loading local config as fallback
		cfg, err := config.LoadDefault()
		if err == nil && cfg != nil {
			host = cfg.System.Host
			portStr = fmt.Sprintf("%d", cfg.System.ServerPort)
		}
	}

	if host == "" {
		host = "127.0.0.1"
	}
	if portStr == "" {
		portStr = "8080"
	}

	return fmt.Sprintf("http://%s:%s", host, portStr)
}

// logRequest prints debug info for a request when verbose mode is enabled.
func logRequest(method, url string) {
	fmt.Fprintf(os.Stderr, "[debug] %s %s\n", method, url)
}

// shortID returns the first 8 characters of an ID, or the full ID if shorter.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
