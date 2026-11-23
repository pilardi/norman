// Package env provides OpenShell session management for Norman workspaces.
package env

import (
	"context"
	"fmt"
	"strings"
	"time"

	v1types "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
)

// WorkspaceState represents the lifecycle state of a workspace session.
type WorkspaceState string

const (
	WorkspaceActive  WorkspaceState = "active"
	WorkspaceIdle    WorkspaceState = "idle"
	WorkspaceStopped WorkspaceState = "stopped"
)

// Valid checks if the workspace state is a valid value.
func (s WorkspaceState) Valid() bool {
	switch s {
	case WorkspaceActive, WorkspaceIdle, WorkspaceStopped:
		return true
	default:
		return false
	}
}

// Session represents a single OpenShell sandbox session with command execution.
type Session struct {
	client       *Client
	provider     string
	sandbox      string
	namespace    string
	sessionId    string // The session ID; set at creation, never the sandbox name
	workspace    string // The workspace this session belongs to
	lastActivity *time.Time
	state        WorkspaceState
}

// NewSession creates a new session bound to a specific sandbox and Norman API session.
func NewSession(client *Client, provider, sandbox, workspace, sessionId string) *Session {
	now := time.Now()
	return &Session{
		client:       client,
		provider:     provider,
		sandbox:      sandbox,
		namespace:    "default",
		sessionId:    sessionId,
		workspace:    workspace,
		lastActivity: &now,
		state:        WorkspaceActive,
	}
}

// MarkActive transitions the workspace state to active. Returns true if transition succeeded.
func (s *Session) MarkActive() bool {
	if s.state == WorkspaceIdle || s.state == WorkspaceStopped {
		s.state = WorkspaceActive
		return true
	}
	return false
}

// MarkIdle transitions the workspace state to idle. Returns true if transition succeeded.
func (s *Session) MarkIdle() bool {
	if s.state == WorkspaceActive {
		s.state = WorkspaceIdle
		return true
	}
	return false
}

// MarkStopped transitions the workspace state to stopped. Returns true if transition succeeded.
func (s *Session) MarkStopped() bool {
	if s.state == WorkspaceActive || s.state == WorkspaceIdle {
		s.state = WorkspaceStopped
		return true
	}
	return false
}

// State returns the current workspace state.
func (s *Session) State() WorkspaceState {
	return s.state
}

// SessionID returns the Norman API session UUID for this session.
// This is the user-facing identifier; it is never the sandbox name.
func (s *Session) SessionID() string {
	return s.sessionId
}

// Workspace returns the workspace this session belongs to.
func (s *Session) Workspace() string {
	return s.workspace
}

// LastActivity returns the timestamp of the last activity, or nil if never set.
func (s *Session) LastActivity() *time.Time {
	return s.lastActivity
}

// Exec runs a command inside the sandbox and returns combined output.
// SESSION_ID is automatically injected into the command's environment.
func (s *Session) Exec(ctx context.Context, cmd []string) (string, error) {
	opts := v1types.ExecOptions{
		Env: map[string]string{"SESSION_ID": s.SessionID()},
	}
	result, err := s.client.RunCommandWithOptions(ctx, s.provider, s.sandbox, cmd, opts)
	if err != nil {
		return "", fmt.Errorf("exec in sandbox %s: %w", s.sandbox, err)
	}

	var output strings.Builder
	output.WriteString(string(result.Stdout))
	if len(result.Stderr) > 0 {
		output.WriteString("\n")
		output.WriteString(string(result.Stderr))
	}
	return output.String(), nil
}

// delete removes the sandbox session (internal use only).
func (s *Session) delete(ctx context.Context) error {
	return s.client.DeleteSandbox(ctx, s.provider, s.sandbox)
}

// ReadFile reads the contents of a file inside the sandbox.
func (s *Session) ReadFile(ctx context.Context, path string) (string, error) {
	result, err := s.client.RunCommand(ctx, s.provider, s.sandbox, []string{"cat", path})
	if err != nil {
		return "", fmt.Errorf("read_file in sandbox %s: %w", s.sandbox, err)
	}

	var output strings.Builder
	output.WriteString(string(result.Stdout))
	if len(result.Stderr) > 0 {
		output.WriteString("\n")
		output.WriteString(string(result.Stderr))
	}
	return output.String(), nil
}

// WriteFile writes content to a file inside the sandbox.
func (s *Session) WriteFile(ctx context.Context, path string, content string) error {
	// Use printf to avoid issues with special characters in content.
	cmd := []string{"sh", "-c", fmt.Sprintf("printf '%%s' '%s' > %q", escapeShellArg(content), path)}
	_, err := s.client.RunCommand(ctx, s.provider, s.sandbox, cmd)
	if err != nil {
		return fmt.Errorf("write_file in sandbox %s: %w", s.sandbox, err)
	}
	return nil
}

// escapeShellArg wraps a string for safe inclusion in a single-quoted shell argument.
func escapeShellArg(s string) string {
	// Single-quote: everything inside is literal except single quotes themselves.
	// Escape ' by closing the quote, inserting an escaped single quote, and reopening.
	return strings.ReplaceAll(s, "'", "'\\''")
}
