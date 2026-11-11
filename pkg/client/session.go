// Package client provides shared session creation logic for both REPL and TUI clients.
package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/protocol"
)

// CreateNewSession creates a new session for the given workspace and returns its ID.
// Returns the session ID and an optional error message on failure.
func CreateNewSession(workspaceName string) (string, error) {
	cfg, err := config.LoadDefault()
	var url string
	if err != nil || cfg == nil || cfg.System == nil {
		url = fmt.Sprintf("http://%s:%d", config.EmbeddedDefaults().System.Host, config.EmbeddedDefaults().System.ServerPort)
	} else if cfg.System.ServerURL != "" {
		url = cfg.System.ServerURL
	} else {
		url = fmt.Sprintf("http://%s:%d", config.EmbeddedDefaults().System.Host, config.EmbeddedDefaults().System.ServerPort)
	}

	client, err := protocol.NewClient(url)
	if err != nil {
		return "", fmt.Errorf("failed to create client: %w", err)
	}

	// Resolve workspace name to ID.
	wsRsp, err := client.ListWorkspaces(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to list workspaces: %w", err)
	}
	parsedWS, err := protocol.ParseListWorkspacesResponse(wsRsp)
	if err != nil {
		return "", fmt.Errorf("error parsing workspaces: %w", err)
	}

	foundID := ""
	for _, w := range *parsedWS.GetJSON200() {
		name := strVal(w.Name)
		if name == workspaceName {
			foundID = w.Id.String()
			break
		}
	}
	if foundID == "" {
		return "", fmt.Errorf("workspace %q not found — run norman workspaces list", workspaceName)
	}

	ct := protocol.Cli
	body := protocol.SessionCreateRequest{
		WorkspaceId: foundID,
		ClientType:  &ct,
	}

	sessRsp, err := client.CreateSession(context.Background(), body)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	parsedSess, err := protocol.ParseCreateSessionResponse(sessRsp)
	if err != nil {
		return "", fmt.Errorf("error parsing session response: %w", err)
	}

	sess := parsedSess.GetJSON201()
	if sess == nil {
		bodyStr := string(parsedSess.GetBody())
		if bodyStr == "" {
			bodyStr = parsedSess.Status()
		}
		return "", fmt.Errorf("session creation failed: %s", bodyStr)
	}

	fmt.Printf("New session '%s' created.\n", strVal(sess.Name))
	return sess.Id.String(), nil
}

// strVal returns the dereferenced value of s, or empty string if nil.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// GetSession fetches session detail by ID. Returns (*protocol.SessionDetail, error).
func GetSession(sessionID string) (*protocol.SessionDetail, error) {
	cfg, err := config.LoadDefault()
	var urlStr string
	if err != nil || cfg == nil || cfg.System == nil {
		urlStr = fmt.Sprintf("http://%s:%d", config.EmbeddedDefaults().System.Host, config.EmbeddedDefaults().System.ServerPort)
	} else if cfg.System.ServerURL != "" {
		urlStr = cfg.System.ServerURL
	} else {
		urlStr = fmt.Sprintf("http://%s:%d", config.EmbeddedDefaults().System.Host, config.EmbeddedDefaults().System.ServerPort)
	}

	client, err := protocol.NewClientWithResponses(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Escape the session ID for use in URL path.
	escapedID := url.PathEscape(sessionID)
	rsp, err := client.GetSessionWithResponse(context.Background(), escapedID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch session: %w", err)
	}

	detail := rsp.GetJSON200()
	if detail == nil {
		bodyStr := string(rsp.GetBody())
		if bodyStr == "" {
			bodyStr = rsp.Status()
		}
		return nil, fmt.Errorf("session '%s' not found (status %d): %s", sessionID, rsp.StatusCode(), strings.TrimSpace(bodyStr))
	}

	return detail, nil
}

// GetVersion returns the server version info. Returns (*protocol.ServerVersion, error).
func GetVersion() (*protocol.ServerVersion, error) {
	cfg, err := config.LoadDefault()
	var urlStr string
	if err != nil || cfg == nil || cfg.System == nil {
		urlStr = fmt.Sprintf("http://%s:%d", config.EmbeddedDefaults().System.Host, config.EmbeddedDefaults().System.ServerPort)
	} else if cfg.System.ServerURL != "" {
		urlStr = cfg.System.ServerURL
	} else {
		urlStr = fmt.Sprintf("http://%s:%d", config.EmbeddedDefaults().System.Host, config.EmbeddedDefaults().System.ServerPort)
	}

	client, err := protocol.NewClientWithResponses(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	rsp, err := client.GetVersionWithResponse(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch version: %w", err)
	}

	v := rsp.GetJSON200()
	if v == nil {
		return nil, fmt.Errorf("no version info returned")
	}

	return v, nil
}
