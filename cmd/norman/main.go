// Package main implements the Norman CLI client.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/scheduler"
)

// serverURL returns the configured server base URL.
// Priority: explicit HOST:PORT > config.yaml defaults > hardcoded fallback.
func serverURL() string {
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

// httpGet performs a GET request to the server and returns JSON bytes.
func httpGet(path string) ([]byte, error) {
	url := serverURL() + path
	if verbose {
		logRequest("GET", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// httpPost performs a POST request with JSON body to the server (no body return).
func httpPost(path string, body interface{}) error {
	url := serverURL() + path
	if verbose {
		logRequest("POST", url)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// httpPostJSON performs a POST request with JSON body and returns the response body.
func httpPostJSON(path string, body interface{}) ([]byte, error) {
	url := serverURL() + path
	if verbose {
		logRequest("POST", url)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}

// httpDelete performs a DELETE request to the server.
func httpDelete(path string) error {
	url := serverURL() + path
	if verbose {
		logRequest("DELETE", url)
	}
	req, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// verbose controls whether debug output is printed to stderr.
var verbose bool

// isVerbose checks if verbose/debug mode was requested.
func isVerbose() bool {
	for _, arg := range os.Args[1:] {
		if arg == "-v" || arg == "--verbose" {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	// Check for -v/--verbose flag anywhere in args (before or after command)
	verbose = isVerbose()

	switch command {
	case "config":
		handleConfig(args)
	case "create":
		handleCreate(args)
	case "list":
		handleList(args)
	case "skills":
		handleSkills(args)
	case "sessions":
		handleSessions(args)
	case "open", "chat":
		handleChat(args[1:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Norman CLI — Session-centric chat agent

Usage: norman [-v|--verbose] <command> [args...]

Flags:
  -v, --verbose      Enable verbose/debug output (request details)

Commands:
  create <name>        Create a new workspace directory
  list                 List available workspaces and their sessions
  config               Show or edit current configuration
  skills list          List installed skills
  skills install <id>  Install a skill from registry
  skills remove <id>   Remove an installed skill
  sessions list                        List active chat sessions
  sessions archive <session_id>        Archive a session's transcript via POST /api/sessions/{id}
  sessions open <workspace_name> [<session_id>]       Open an interactive REPL chat session

Use "norman help <command>" for detailed help on a specific command.`)
}

// handleConfig shows or edits the current config.
func handleConfig(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`config — Show or edit configuration

Usage: norman config [--set key=value | --json]

Options:
  --set     Set a config value (e.g., --set server_port=9090)
  --json    Output as machine-readable JSON

Config is managed by the server at ~/.config/norman/config.yaml.
Secrets are stored as ${secret:...} placeholders and resolved by OpenShell.`)
		return
	}

	if len(args) > 0 && args[0] == "--set" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: norman config --set key=value")
			os.Exit(1)
		}
		parts := strings.SplitN(args[1], "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Invalid format: %s (expected key=value)\n", args[1])
			os.Exit(1)
		}
		key, val := parts[0], parts[1]

		// Build partial update map
		update := make(map[string]interface{})
		switch key {
		case "default_model":
			update["default_model"] = val
		case "server_port":
			var port int
			if n, err := fmt.Sscanf(val, "%d", &port); n != 1 || err != nil {
				fmt.Fprintf(os.Stderr, "Invalid server_port value: %s\n", val)
				os.Exit(1)
			}
			update["server_port"] = port
		case "max_tokens":
			var t int
			if n, err := fmt.Sscanf(val, "%d", &t); n != 1 || err != nil {
				fmt.Fprintf(os.Stderr, "Invalid max_tokens value: %s\n", val)
				os.Exit(1)
			}
			update["max_tokens"] = t
		case "temperature":
			var t float64
			if n, err := fmt.Sscanf(val, "%f", &t); n != 1 || err != nil {
				fmt.Fprintf(os.Stderr, "Invalid temperature value: %s\n", val)
				os.Exit(1)
			}
			update["temperature"] = t
		case "logging.verbose":
			// Verbose is a client-side toggle (-v/--verbose); not shared with server.
			fmt.Printf("Verbose mode: %s (client-local, not persisted to server)\n", val)
			return
		case "openshell_address":
			update["openshell_address"] = val
		default:
			fmt.Fprintf(os.Stderr, "Unknown config key: %s\n", key)
			os.Exit(1)
		}

		// POST update to server
		if err := putConfig(update); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated %s → %s\n", key, val)
		return
	}

	if len(args) > 0 && args[0] == "--json" {
		cfg, resolvedGateway, err := getConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching config from server: %v\n", err)
			os.Exit(1)
		}

		// Output snake_case for consistency with CLI display.
		output := map[string]interface{}{
			"default_model":      cfg.DefaultModel,
			"server_port":        cfg.ServerPort,
			"host":               cfg.Host,
			"max_active_skills":  cfg.Skills.MaxActive,
			"bell_enabled":       cfg.Notify.BellEnabled,
			"rate_limit_per_min": cfg.Notify.RateLimitPerMin,
			"openshell_address":  resolvedGateway,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return
	}

	// Default: show current config (human-readable)
	cfg, resolvedGateway, err := getConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching config from server: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("# Current Configuration (System)")
	fmt.Printf("  default_model:    %s\n", cfg.DefaultModel)
	fmt.Printf("  server_port:      %d\n", cfg.ServerPort)
	fmt.Printf("  host:             %s\n", cfg.Host)
	fmt.Printf("  max_active_skills: %d\n", cfg.Skills.MaxActive)
	fmt.Printf("  bell_enabled:     %v\n", cfg.Notify.BellEnabled)
	fmt.Printf("  rate_limit_per_min: %d\n", cfg.Notify.RateLimitPerMin)
	// Show the resolved gateway address (from server-side resolution, not local config).
	if resolvedGateway != "" {
		fmt.Printf("  openshell_address: %s\n", resolvedGateway)
	} else if cfg.OpenShell.Address != "" {
		fmt.Printf("  openshell_address: %s\n", cfg.OpenShell.Address)
	}

	// Note: workspace overrides are managed server-side; client cannot read them here.
}

// getConfig fetches the current system config from the server.
func getConfig() (*config.SystemConfig, string, error) {
	resp, err := http.Get(serverURL() + "/api/config")
	if err != nil {
		return nil, "", fmt.Errorf("GET /api/config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("GET /api/config returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		System    *config.SystemConfig `json:"system"`
		OpenShell string               `json:"openshell"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decode config response: %w", err)
	}

	return result.System, result.OpenShell, nil
}

// putConfig sends a partial system config update to the server.
func putConfig(updates map[string]interface{}) error {
	body, err := json.Marshal(updates)
	if err != nil {
		return fmt.Errorf("marshal updates: %w", err)
	}

	req, err := http.NewRequest("PUT", serverURL()+"/api/config", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT /api/config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT /api/config returned %d: %s", resp.StatusCode, string(data))
	}

	return nil
}

// handleCreate delegates workspace creation to the agent server.
func handleCreate(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: norman create <name>")
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

// handleList fetches workspace info from the server.
func handleList(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`list — List available workspaces and sessions

Usage: norman list [--json]

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
		fmt.Printf("  %-20s %s\n", name, path)
	}
}

// handleSkills handles skills subcommands.
func handleSkills(args []string) {
	if len(args) < 1 {
		fmt.Println(`skills — Manage installed skills

Usage: norman skills <subcommand> [args...]

Subcommands:
  list       Show installed skills
  install <path>  Install a skill from a directory path
  remove <name>   Remove an installed skill by name`)
		return
	}

	sub := args[0]
	switch sub {
	case "list":
		listSkills()
	case "install":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: norman skills install <path>\n")
			os.Exit(1)
		}
		installSkill(args[1])
	case "remove":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: norman skills remove <name>\n")
			os.Exit(1)
		}
		removeSkill(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown skills subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// listSkills fetches installed skills from the server.
func listSkills() {
	body, err := httpGet("/api/skills")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching skills: %v\n", err)
		os.Exit(1)
	}

	var skills []map[string]interface{}
	if err := json.Unmarshal(body, &skills); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing skills response: %v\n", err)
		os.Exit(1)
	}

	if len(skills) == 0 {
		fmt.Println("No skills discovered.")
		return
	}

	fmt.Printf("Discovered %d skill(s):\n\n", len(skills))
	for _, s := range skills {
		name, _ := s["name"].(string)
		desc, _ := s["description"].(string)
		path, _ := s["path"].(string)
		tools, _ := s["tools"].([]interface{})

		fmt.Printf("  📦 %s\n", name)
		fmt.Printf("     %s\n", desc)
		if len(tools) > 0 {
			toolNames := make([]string, len(tools))
			for i, t := range tools {
				if m, ok := t.(map[string]interface{}); ok {
					if n, ok := m["name"].(string); ok {
						toolNames[i] = n
					}
				}
			}
			fmt.Printf("     Tools: %s\n", strings.Join(toolNames, ", "))
		}
		fmt.Printf("     Path: %s\n", path)
		fmt.Println()
	}
}

// installSkill delegates to the server's /api/skills/install endpoint.
func installSkill(sourcePath string) {
	if err := httpPost("/api/skills/install", map[string]string{"name": sourcePath}); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing skill: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Requesting server to install skill from '%s'\n", sourcePath)
}

// removeSkill delegates to the server's /api/skills/:name endpoint.
func removeSkill(name string) {
	if err := httpDelete("/api/skills/" + url.PathEscape(name)); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing skill: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Requesting server to remove skill '%s'\n", name)
}

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

	url := serverURL() + "/api/sessions/" + url.PathEscape(actualID)
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
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "HTTP %d archiving session %s: %s\n", resp.StatusCode, shortID(actualID), string(body))
		os.Exit(1)
	}
	fmt.Printf("Session '%s' archived successfully.\n", shortID(actualID))
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// handleTaskAdd — removed (manual task creation no longer part of client API).
// handleTaskList  — removed.
// handleTaskRemove— removed.
// handleTaskRun   — removed.

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

// handleChat implements the interactive REPL chat mode.
// Usage: norman sessions open <workspace_name> [<session_id>]
func handleChat(args []string) {
	workspaceName := ""
	sessionID := ""

	if len(args) == 0 {
		printChatHelp()
		os.Exit(1)
	}

	// Parse args: first is workspace name, second optional is session ID.
	for i := 0; i < len(args); i++ {
		if args[i] == "-h" || args[i] == "--help" {
			printChatHelp()
			return
		} else if workspaceName == "" {
			workspaceName = args[i]
		} else if sessionID == "" {
			var found bool
			sessionID, found = resolveSessionID(args[i])
			if !found {
				fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", args[i])
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Too many arguments. Usage: norman sessions open <workspace_name> [<session_id>]\n")
			printChatHelp()
			os.Exit(1)
		}
	}

	if workspaceName == "" {
		fmt.Fprintln(os.Stderr, "Error: workspace name is required")
		printChatHelp()
		os.Exit(1)
	}

	// If only workspace provided (no session ID), resolve/create one.
	if workspaceName != "" && sessionID == "" {
		fmt.Printf("Resolving workspace '%s'...\n", workspaceName)
		workspacesBody, err := httpGet("/api/workspaces")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching workspaces: %v\n", err)
			os.Exit(1)
		}
		var workspaces []map[string]interface{}
		if err := json.Unmarshal(workspacesBody, &workspaces); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing workspaces response: %v\n", err)
			os.Exit(1)
		}
		foundID := ""
		for _, w := range workspaces {
			name, _ := w["name"].(string)
			if name == workspaceName {
				idStr, _ := w["id"].(string)
				foundID = idStr
				break
			}
		}
		if foundID == "" {
			fmt.Fprintf(os.Stderr, "Workspace '%s' not found. Run 'norman list'.\n", workspaceName)
			os.Exit(1)
		}

		// Check if an active session already exists for this workspace.
		sessionsBody, err := httpGet("/api/sessions")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
			os.Exit(1)
		}
		var sessions []map[string]interface{}
		if err := json.Unmarshal(sessionsBody, &sessions); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing sessions response: %v\n", err)
			os.Exit(1)
		}
		for _, s := range sessions {
			sid, _ := s["id"].(string)
			status, _ := s["status"].(string)
			if status == "active" {
				sessionID = sid
				fmt.Printf("Reusing existing active session '%s' for workspace '%s'\n", sid, workspaceName)
				break
			}
		}

		// No active session — create one.
		if sessionID == "" {
			fmt.Printf("Creating new session for workspace '%s'...\n", workspaceName)
			sessBody := map[string]string{"workspace_id": foundID, "client_type": "cli"}
			sessJSON, _ := json.Marshal(sessBody)
			sessReq, _ := http.NewRequestWithContext(context.Background(), "POST", serverURL()+"/api/sessions", bytes.NewReader(sessJSON))
			sessReq.Header.Set("Content-Type", "application/json")
			sessResp, err := (&http.Client{Timeout: 10 * time.Second}).Do(sessReq)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create session: %v\n", err)
				os.Exit(1)
			}
			defer sessResp.Body.Close()
			if sessResp.StatusCode >= 400 {
				bodyBytes, _ := io.ReadAll(sessResp.Body)
				fmt.Fprintf(os.Stderr, "Session create failed (HTTP %d): %s\n", sessResp.StatusCode, string(bodyBytes))
				os.Exit(1)
			}
			var sess map[string]interface{}
			if err := json.NewDecoder(sessResp.Body).Decode(&sess); err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing session response: %v\n", err)
				os.Exit(1)
			}
			sessionID = sess["id"].(string)
			fmt.Printf("Created session '%s' for workspace '%s'\n", sessionID, workspaceName)
		}
	}

	// Enter REPL loop.
	fmt.Println("\n=== Norman Chat Session ===")
	fmt.Printf("Session: %s | Workspace resolved\n", sessionID)
	fmt.Println("Type a message or use slash-commands (/help, /tasks, /close, /new, /history [N], /sessions, /switch <id>, /archive <id>)")

	// Track turns for transcript persistence.
	var turns []map[string]interface{}
	transcriptPath := filepath.Join(os.ExpandEnv("$HOME"), ".norman", "sessions", sessionID+".md")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		cmd, cmdArgs, isSlash := parseSlashCommand(line)

		if isSlash {
			switch cmd {
			case "help":
				printChatHelp()
			case "list", "tasks":
				listEmergentTasks(sessionID)
			case "close":
				fmt.Println("Closing session... saving transcript.")
				// Archive the session on the server.
				archiveSession(sessionID)
				// Save full transcript to disk.
				if err := saveTranscriptLocally(transcriptPath, turns); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to save transcript: %v\n", err)
				} else {
					fmt.Printf("Transcript saved to %s\n", transcriptPath)
				}
				return
			case "new":
				// Start a new session (auto-create).
				fmt.Printf("Creating new session...\n")
				newSessBody := map[string]string{"client_type": "cli"}
				newSessJSON, _ := json.Marshal(newSessBody)
				newSessReq, _ := http.NewRequestWithContext(context.Background(), "POST", serverURL()+"/api/sessions", bytes.NewReader(newSessJSON))
				newSessReq.Header.Set("Content-Type", "application/json")
				newSessResp, err := (&http.Client{Timeout: 10 * time.Second}).Do(newSessReq)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to create new session: %v\n", err)
					continue
				}
				defer newSessResp.Body.Close()
				var newSess map[string]interface{}
				if err := json.NewDecoder(newSessResp.Body).Decode(&newSess); err != nil {
					fmt.Fprintf(os.Stderr, "Error parsing new session response: %v\n", err)
					continue
				}
				sessionID = newSess["id"].(string)
				fmt.Printf("New session '%s' created.\n", sessionID)
			case "history":
				showHistory(sessionID, cmdArgs)
			case "sessions":
				listSessions()
			case "switch":
				if len(cmdArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: /switch <session-id>")
					continue
				}
				sessionID = cmdArgs[0]
				fmt.Printf("Switched to session '%s'\n", sessionID[:8])
			case "archive":
				if len(cmdArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: /archive <session-id>")
					continue
				}
				archiveSession(cmdArgs[0])
			default:
				fmt.Fprintf(os.Stderr, "Unknown slash-command: /%s\n", cmd)
			}
			continue
		}

		// Normal message — send to agent.
		if strings.TrimSpace(line) == "" {
			continue
		}

		// POST to session messages endpoint (Phase 1).
		body := map[string]string{"message": line}
		jsonBody, err := json.Marshal(body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling request: %v\n", err)
			continue
		}

		req, err := http.NewRequestWithContext(context.Background(), "POST", serverURL()+"/api/sessions/"+url.PathEscape(sessionID)+"/messages", bytes.NewReader(jsonBody))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "POST /api/sessions/%s/messages failed: %v\n", sessionID, err)
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, string(bodyBytes))
			continue
		}

		// Parse and display response.
		var result map[string]interface{}
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&result); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
			continue
		}

		// Track turn for transcript persistence.
		responseText := ""
		if resp, ok := result["response"].(string); ok {
			responseText = resp
		} else if msg, ok := result["message"].(string); ok {
			responseText = msg
		}

		turns = append(turns, map[string]interface{}{
			"role":      "user",
			"content":   line,
			"timestamp": time.Now().Format(time.RFC3339),
		})
		if responseText != "" {
			turns = append(turns, map[string]interface{}{
				"role":      "assistant",
				"content":   responseText,
				"timestamp": time.Now().Format(time.RFC3339),
			})
		}

		// Display agent response (structured or raw).
		displayResponse(result)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Read error: %v\n", err)
		os.Exit(1)
	}
}

// printChatHelp displays help for the sessions open REPL.
func printChatHelp() {
	fmt.Println(`sessions open — Interactive REPL chat mode

Usage: norman sessions open <workspace_name> [<session_id>]

Slash-commands (prefix with /):
  /help             Show this help
  /tasks            List emergent scheduled tasks for current session
  /close            Close the current session (save transcript)
  /new              Start a new chat session
  /history [N]      Show last N messages of current session
  /sessions         List all active sessions
  /switch <id>      Switch to another session by ID
  /archive <id>     Archive a session's transcript by session ID

Examples:
  norman sessions open my-project
  norman sessions open my-project abc12345`)
}

// listEmergentTasks lists tasks for the given session.
func listEmergentTasks(sessionID string) {
	body, err := httpGet("/api/tasks")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching tasks: %v\n", err)
		return
	}
	fmt.Println("Emergent scheduled tasks:")
	fmt.Printf("%s\n", string(body))
}

// showHistory displays the last N messages of a session.
func showHistory(sessionID string, args []string) {
	n := 10 // default
	if len(args) > 0 {
		if num, err := fmt.Sscanf(args[0], "%d", &n); err != nil || num == 0 {
			fmt.Fprintf(os.Stderr, "Invalid number: %s\n", args[0])
			return
		}
	}

	// Resolve session name to actual UUID if needed.
	actualID, found := resolveSessionID(sessionID)
	if !found {
		fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", sessionID)
		return
	}

	body, err := httpGet("/api/sessions/" + url.PathEscape(actualID) + "/history?limit=" + fmt.Sprintf("%d", n))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching history: %v\n", err)
		return
	}
	fmt.Printf("\n--- Last %d messages ---\n%s\n---\n", n, string(body))
}

// resolveSessionID resolves a session name/short ID to the actual UUID.
func resolveSessionID(input string) (string, bool) {
	body, err := httpGet("/api/sessions")
	if err != nil {
		return input, false // fall back to original
	}
	var sessions []map[string]interface{}
	if err := json.Unmarshal(body, &sessions); err != nil {
		return input, false
	}
	for _, s := range sessions {
		name, _ := s["name"].(string)
		id, _ := s["id"].(string)
		if name == input || strings.HasPrefix(id[:8], input[:8]) {
			return id, true
		}
	}
	return input, false
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

// archiveSession archives a session's transcript via POST /api/sessions/{id}.
func archiveSession(sessionID string) {
	actualID, found := resolveSessionID(sessionID)
	if !found {
		fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", sessionID)
		return
	}

	url := serverURL() + "/api/sessions/" + url.PathEscape(actualID)
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating archive request: %v\n", err)
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Archive failed for session %s: %v\n", shortID(actualID), err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "HTTP %d archiving session %s: %s\n", resp.StatusCode, shortID(actualID), string(body))
		return
	}
	fmt.Printf("Session '%s' archived successfully.\n", shortID(actualID))
}

// displayResponse formats and prints the agent's response.
func displayResponse(result map[string]interface{}) {
	if resp, ok := result["response"].(string); ok {
		fmt.Println(resp)
		return
	}
	if msg, ok := result["message"].(string); ok {
		fmt.Println(msg)
		return
	}
	// Fallback: print as formatted JSON.
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

// expandHome resolves ~ in paths.
func expandHome(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// saveTranscriptLocally writes conversation turns as markdown to a local transcript file.
func saveTranscriptLocally(path string, turns []map[string]interface{}) error {
	var lines []string
	lines = append(lines, "# Session Transcript\n")
	lines = append(lines, fmt.Sprintf("**Session ID:** `%s`\n\n", filepath.Base(filepath.Dir(path))))
	lines = append(lines, fmt.Sprintf("**Created:** %s\n\n---\n\n", time.Now().Format(time.RFC3339)))

	for i, turn := range turns {
		role, _ := turn["role"].(string)
		content, _ := turn["content"].(string)
		ts, _ := turn["timestamp"].(string)

		lines = append(lines, fmt.Sprintf("## Turn %d: %s", i+1, role))
		if ts != "" {
			lines = append(lines, fmt.Sprintf("\n\n*%s*\n", ts))
		} else {
			lines = append(lines, "\n")
		}
		lines = append(lines, content+"\n")
		lines = append(lines, "\n---\n\n")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create transcript dir: %w", err)
	}

	tmp := path + ".tmp"
	content := strings.Join(lines, "")
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename transcript: %w", err)
	}

	return nil
}

// toInterfaceSlice converts a slice of TaskEntry to []interface{} for persistence.
func toInterfaceSlice(entries []*scheduler.TaskEntry) []interface{} {
	result := make([]interface{}, len(entries))
	for i, entry := range entries {
		result[i] = map[string]interface{}{
			"id":       entry.ID,
			"name":     entry.Name,
			"schedule": entry.Schedule,
			"command":  entry.Command,
			"active":   entry.Active,
		}
	}
	return result
}

// getConfigPath returns the standard config file path.
func getConfigPath() string {
	return filepath.Join(os.ExpandEnv("$HOME/.config/norman"), "config.yaml")
}
