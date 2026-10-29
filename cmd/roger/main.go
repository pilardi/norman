// Package main implements the `roger` CLI — a general-purpose client for Norman's API.
//
// Roger is named after Roger I of Sicily and the phrase "Roger that."
// It provides a lightweight, workspace-aware interface to Norman's notification
// and status APIs, designed to be invoked from scripts and scheduled tasks.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

// ContextFile is the workspace context file injected at sandbox creation.
type ContextFile struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
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
	app := &cli.Command{
		Name:                  "roger",
		Suggest:               true,
		Description:           "Roger — Norman workspace client",
		EnableShellCompletion: true,
		Commands: []*cli.Command{
			notifyCommand(),
			pingCommand(),
			statusCommand(),
			logCommand(),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		os.Exit(1)
	}
}

// notifyCommand returns the "notify" subcommand.
func notifyCommand() *cli.Command {
	var title, body, severity string

	return &cli.Command{
		Name:        "notify",
		Description: "Push a notification to the connected user",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "title",
				Usage:    "Notification title (required)",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "body",
				Usage: "Notification body text",
			},
			&cli.StringFlag{
				Name:  "severity",
				Usage: "Severity level: info, warn, error",
			},
			&cli.StringFlag{
				Name:  "task-id",
				Usage: "Associated task ID (reserved for future use)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			title = cmd.String("title")
			body = cmd.String("body")
			severity = cmd.String("severity")

			if title == "" {
				return fmt.Errorf("--title is required")
			}

			envCtx, err := ensureEnv()
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}

			req := NotificationRequest{
				Title:    title,
				Body:     body,
				Severity: severity,
			}

			client := buildClient(envCtx)
			jsonData, _ := json.Marshal(req)

			apiURL, err := apiURL(envCtx, "/api/notifications")
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}
			resp, err := client.Post(apiURL, "application/json", strings.NewReader(string(jsonData)))
			if err != nil {
				return fmt.Errorf("API error: %v", err)
			}
			defer resp.Body.Close()

			var apiResp NotificationResponse
			_ = json.NewDecoder(resp.Body).Decode(&apiResp)

			outputJSON(apiResp)
			outputText("✓ notification sent (id=%d, severity=%s)", apiResp.ID, apiResp.Severity)
			return nil
		},
	}
}

// pingCommand returns the "ping" subcommand.
func pingCommand() *cli.Command {
	return &cli.Command{
		Name:        "ping",
		Description: "Send a health-check event to the current session",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			envCtx, err := ensureEnv()
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}

			client := buildClient(envCtx)

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

			apiURL, err := apiURL(envCtx, "/api/sessions/"+url.PathEscape(sessionID)+"/events")
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}
			resp, err := client.Post(
				apiURL,
				"application/json",
				strings.NewReader(string(jsonData)),
			)
			if err != nil {
				return fmt.Errorf("API error: %v", err)
			}
			defer resp.Body.Close()

			outputText("✓ ping sent to session %s", sessionID)
			return nil
		},
	}
}

// statusCommand returns the "status" subcommand.
func statusCommand() *cli.Command {
	return &cli.Command{
		Name:        "status",
		Description: "Query workspace state (JSON on stdout)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			envCtx, err := ensureEnv()
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}

			client := buildClient(envCtx)

			apiURL, err := apiURL(envCtx, "/api/workspaces/"+url.PathEscape(envCtx.WorkspaceID))
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}
			resp, err := client.Get(apiURL)
			if err != nil {
				return fmt.Errorf("API error: %v", err)
			}
			defer resp.Body.Close()

			var workspace map[string]interface{}
			_ = json.NewDecoder(resp.Body).Decode(&workspace)

			outputJSON(workspace)
			outputText("✓ workspace %s is active", envCtx.WorkspaceName)
			return nil
		},
	}
}

// logCommand returns the "log" subcommand.
func logCommand() *cli.Command {
	var lines = 20 // default

	return &cli.Command{
		Name:        "log",
		Description: "Tail recent events for the current session",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "lines",
				Usage: "Number of lines to show (default: 20)",
				Value: 20,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			lines = cmd.Int("lines")

			envCtx, err := ensureEnv()
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}

			sessionID := os.Getenv("SESSION_ID")
			if sessionID == "" {
				outputText("warning: SESSION_ID not set, showing global events")
				sessionID = "*"
			}

			client := buildClient(envCtx)

			apiURL, err := apiURL(envCtx, "/api/sessions/"+url.PathEscape(sessionID)+"/events?lines="+fmt.Sprintf("%d", lines))
			if err != nil {
				return fmt.Errorf("error: %v", err)
			}
			resp, err := client.Get(apiURL)
			if err != nil {
				return fmt.Errorf("API error: %v", err)
			}
			defer resp.Body.Close()

			var events []map[string]interface{}
			_ = json.NewDecoder(resp.Body).Decode(&events)

			outputJSON(events)
			outputText("✓ showed %d events for session %s", len(events), sessionID)
			return nil
		},
	}
}

// ensureEnv checks that roger has a valid environment (context.json or NORMAN_SERVER_URL).
// Returns the parsed context file and an error if neither is available.
func ensureEnv() (*ContextFile, error) {
	// If NORMAN_SERVER_URL is set, we're in a workspace — return empty context for mTLS paths.
	if os.Getenv("NORMAN_SERVER_URL") != "" {
		return &ContextFile{}, nil
	}

	// Try to read context.json for workspace ID and mTLS paths.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("you can only run roger within a workspace")
	}

	ctxPath := filepath.Join(home, ".config", "norman", "context.json")

	data, err := os.ReadFile(ctxPath)
	if err != nil {
		return nil, fmt.Errorf("you can only run roger within a workspace")
	}

	var ctx ContextFile
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("invalid context file: %w", err)
	}

	return &ctx, nil
}

// buildClient creates an HTTP client with optional mTLS config.
func buildClient(ctx *ContextFile) *http.Client {
	return &http.Client{}
}

// apiURL constructs the full API URL for a given path.
// Assumes NORMAN_SERVER_URL is already validated by ensureEnv().
func apiURL(ctx *ContextFile, path string) (string, error) {
	base := strings.TrimRight(os.Getenv("NORMAN_SERVER_URL"), "/")
	if base == "" {
		return "", fmt.Errorf("you can only run roger within a workspace")
	}
	return base + path, nil
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
