// Package client provides shared session creation logic for both REPL and TUI clients.
package client

import (
	"context"
	"fmt"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/protocol"
)

// CreateNewSession creates a new session for the given workspace and returns its ID.
func CreateNewSession(workspaceName string) string {
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
		fmt.Fprintf(nil, "Failed to create client: %v\n", err)
		return ""
	}

	// Resolve workspace name to ID.
	wsRsp, err := client.ListWorkspaces(context.Background(), nil)
	if err != nil {
		fmt.Fprintf(nil, "Failed to list workspaces: %v\n", err)
		return ""
	}
	parsedWS, err := protocol.ParseListWorkspacesResponse(wsRsp)
	if err != nil {
		fmt.Fprintf(nil, "Error parsing workspaces: %v\n", err)
		return ""
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
		fmt.Fprintf(nil, "Workspace '%s' not found. Run 'norman workspaces list'.\n", workspaceName)
		return ""
	}

	ct := protocol.Cli
	body := protocol.SessionCreateRequest{
		WorkspaceId: foundID,
		ClientType:  &ct,
	}

	sessRsp, err := client.CreateSession(context.Background(), body)
	if err != nil {
		fmt.Fprintf(nil, "Failed to create session: %v\n", err)
		return ""
	}
	parsedSess, err := protocol.ParseCreateSessionResponse(sessRsp)
	if err != nil {
		fmt.Fprintf(nil, "Error parsing session response: %v\n", err)
		return ""
	}

	sess := parsedSess.GetJSON201()
	if sess == nil {
		bodyStr := string(parsedSess.GetBody())
		if bodyStr == "" {
			bodyStr = parsedSess.Status()
		}
		fmt.Fprintf(nil, "Session creation failed: %s\n", bodyStr)
		return ""
	}

	fmt.Printf("New session '%s' created.\n", strVal(sess.Name))
	return sess.Id.String()
}

// strVal returns the dereferenced value of s, or empty string if nil.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
