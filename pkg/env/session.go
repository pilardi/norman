// Package env provides OpenShell session management for Norman workspaces.
package env

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	v1types "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
)

// Session represents a single OpenShell sandbox session with command execution.
type Session struct {
	client    *Client
	provider  string
	sandbox   string
	namespace string
	sessionID string // unique per-session ID, injected as SESSION_ID env var
}

// NewSession creates a new session bound to a specific sandbox.
// The sessionID is generated as a UUID and will be injected into every command execution.
func NewSession(client *Client, provider, sandbox string) *Session {
	return &Session{
		client:    client,
		provider:  provider,
		sandbox:   sandbox,
		namespace: "default",
		sessionID: uuid.New().String(),
	}
}

// NewSessionWithClient creates a session bound to a specific sandbox.
func NewSessionWithClient(client *Client, provider, sandbox string) *Session {
	return &Session{
		client:    client,
		provider:  provider,
		sandbox:   sandbox,
		namespace: "default",
		sessionID: uuid.New().String(),
	}
}

// SessionID returns the unique session identifier for this session.
func (s *Session) SessionID() string {
	return s.sessionID
}

// SandboxName returns the name of the underlying OpenShell sandbox.
func (s *Session) SandboxName() string {
	return s.sandbox
}

// Exec runs a command inside the sandbox and returns combined output.
// SESSION_ID is automatically injected into the command's environment.
func (s *Session) Exec(ctx context.Context, cmd []string) (string, error) {
	opts := v1types.ExecOptions{
		Env: map[string]string{"SESSION_ID": s.sessionID},
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

// Status returns the current status of the sandbox.
func (s *Session) Status(ctx context.Context) (*v1.Sandbox, error) {
	return s.client.GetSandbox(ctx, s.provider, s.sandbox)
}

// IsReady checks if the sandbox is currently running or ready.
func (s *Session) IsReady(ctx context.Context) bool {
	sb, err := s.Status(ctx)
	if err != nil {
		return false
	}
	return sb.Status.Phase == v1.SandboxReady || sb.Status.Phase == v1.SandboxStarting
}

// WaitReady waits for the session to become ready.
func (s *Session) WaitReady(ctx context.Context) (*v1.Sandbox, error) {
	return s.client.WaitReady(ctx, s.provider, s.sandbox)
}

// Delete removes the sandbox session.
func (s *Session) Delete(ctx context.Context) error {
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
