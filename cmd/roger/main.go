// Package main implements the `roger` CLI — a general-purpose client for Norman's API.
//
// Roger is named after Roger I of Sicily and the phrase "Roger that."
// It provides a lightweight, workspace-aware interface to Norman's notification
// and status APIs, designed to be invoked from scripts and scheduled tasks.
package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ContextFile is the workspace context file injected at sandbox creation.
type ContextFile struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceName  string `json:"workspace_name"`
	MTLSCertPath   string `json:"mtls_cert_path,omitempty"`
	MTLSKeyPath    string `json:"mtls_key_path,omitempty"`
}

// NotificationRequest mirrors the API schema for creating a notification.
type NotificationRequest struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"` // "info", "warn", "error"
	TaskID   string `json:"task_id,omitempty"`
}

// NotificationResponse is the API response for a created notification.
type NotificationResponse struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "notify":
		handleNotify(args)
	case "ping":
		handlePing(args)
	case "status":
		handleStatus(args)
	case "log":
		handleLog(args)
	case "--help", "-h", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "roger: unknown command %q\n", command)
		fmt.Fprintln(os.Stderr, "Available commands: notify, ping, status, log")
		os.Exit(1)
	}
}

// printUsage outputs a brief help message to stderr.
func printUsage() {
	fmt.Fprint(os.Stderr, `roger — Norman workspace client

Usage:
  roger <command> [flags]

Commands:
  notify    Push a notification to the connected user
  ping      Send a health-check event to the current session
  status    Query workspace state (JSON on stdout)
  log       Tail recent events for the current session

Session targeting:
  roger is invoked inside an OpenShell sandbox where the agent injects
  SESSION_ID=<uuid> into the command environment. Commands that target
  a specific session use this value; if not set, they fall back to "*"
  (broadcast to all sessions in the workspace).

Flags:
  --help, -h   Show this help message
`)
}

// readContext loads and validates the context file.
func readContext() (*ContextFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	ctxPath := filepath.Join(home, ".config", "norman", "context.json")

	data, err := os.ReadFile(ctxPath)
	if err != nil {
		return nil, fmt.Errorf("context file not found at %s", ctxPath)
	}

	var ctx ContextFile
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("invalid context file: %w", err)
	}

	return &ctx, nil
}

// buildClient creates an HTTP client with optional mTLS config.
func buildClient(ctx *ContextFile) *http.Client {
	client := &http.Client{}

	if ctx.MTLSCertPath != "" && ctx.MTLSKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(ctx.MTLSCertPath, ctx.MTLSKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "roger: warning: failed to load mTLS cert: %v\n", err)
		} else {
			client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{
					Certificates: []tls.Certificate{cert},
				},
			}
		}
	}

	return client
}

// apiURL constructs the full API URL for a given path.
// Server URL is read from NORMAN_SERVER_URL env var (not from context).
func apiURL(ctx *ContextFile, path string) string {
	base := strings.TrimRight(os.Getenv("NORMAN_SERVER_URL"), "/")
	if base == "" {
		base = "http://localhost:8080" // fallback for local dev
	}
	return base + path
}

// outputJSON prints structured JSON to stdout.
func outputJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "roger: failed to encode JSON: %v\n", err)
	}
}

// outputText prints a human-readable message to stderr.
func outputText(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
}

// handleNotify processes the "notify" subcommand.
func handleNotify(args []string) {
	var title, body, severity string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--title":
			i++
			if i < len(args) {
				title = args[i]
			}
		case "--body":
			i++
			if i < len(args) {
				body = args[i]
			}
		case "--severity":
			i++
			if i < len(args) {
				severity = args[i]
			}
		case "--task-id":
			i++
			if i < len(args) {
				// ignored for now — stored for future use
			}
		}
	}

	if title == "" {
		outputText("error: --title is required")
		os.Exit(1)
	}

	ctx, err := readContext()
	if err != nil {
		outputText("config error: %v", err)
		os.Exit(2)
	}

	req := NotificationRequest{
		Title:    title,
		Body:     body,
		Severity: severity,
	}

	client := buildClient(ctx)
	jsonData, _ := json.Marshal(req)

	resp, err := client.Post(apiURL(ctx, "/api/notifications"), "application/json", strings.NewReader(string(jsonData)))
	if err != nil {
		outputText("API error: %v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var apiResp NotificationResponse
	json.NewDecoder(resp.Body).Decode(&apiResp)

	outputJSON(apiResp)
	outputText("✓ notification sent (id=%d, severity=%s)", apiResp.ID, apiResp.Severity)
}

// handlePing sends a lightweight health-check event.
func handlePing(args []string) {
	ctx, err := readContext()
	if err != nil {
		outputText("config error: %v", err)
		os.Exit(2)
	}

	client := buildClient(ctx)

	// POST to /api/sessions/{id}/events with a "ping" event type
	sessionID := os.Getenv("SESSION_ID")
	if sessionID == "" {
		outputText("warning: SESSION_ID not set, broadcasting to all sessions")
		sessionID = "*"
	}

	event := map[string]string{
		"event":   "ping",
		"message": "roger health check",
	}
	jsonData, _ := json.Marshal(event)

	resp, err := client.Post(
		apiURL(ctx, "/api/sessions/"+url.PathEscape(sessionID)+"/events"),
		"application/json",
		strings.NewReader(string(jsonData)),
	)
	if err != nil {
		outputText("API error: %v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	outputText("✓ ping sent to session %s", sessionID)
}

// handleStatus queries workspace state.
func handleStatus(args []string) {
	ctx, err := readContext()
	if err != nil {
		outputText("config error: %v", err)
		os.Exit(2)
	}

	client := buildClient(ctx)

	resp, err := client.Get(apiURL(ctx, "/api/workspaces/"+url.PathEscape(ctx.WorkspaceID)))
	if err != nil {
		outputText("API error: %v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var workspace map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&workspace)

	outputJSON(workspace)
	outputText("✓ workspace %s is active", ctx.WorkspaceName)
}

// handleLog tails recent events.
func handleLog(args []string) {
	ctx, err := readContext()
	if err != nil {
		outputText("config error: %v", err)
		os.Exit(2)
	}

	lines := 20 // default
	for i := 0; i < len(args); i++ {
		if args[i] == "--lines" && i+1 < len(args) {
			lines, _ = fmt.Sscanf(args[i+1], "%d", &lines)
			i++
		}
	}

	sessionID := os.Getenv("SESSION_ID")
	if sessionID == "" {
		outputText("warning: SESSION_ID not set, showing global events")
		sessionID = "*"
	}

	client := buildClient(ctx)

	resp, err := client.Get(
		apiURL(ctx, "/api/sessions/"+url.PathEscape(sessionID)+"/events?lines="+fmt.Sprintf("%d", lines)),
	)
	if err != nil {
		outputText("API error: %v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var events []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&events)

	outputJSON(events)
	outputText("✓ showed %d events for session %s", len(events), sessionID)
}
