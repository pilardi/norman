// Package env provides OpenShell session management for Norman workspaces.
package env

import (
	"context"
	"fmt"
	"strings"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

// Session represents a single OpenShell sandbox session with command execution.
type Session struct {
	client    *Client
	provider  string
	sandbox   string
	namespace string
}

// NewSession creates a new session bound to a specific sandbox.
func NewSession(client *Client, provider, sandbox string) *Session {
	return &Session{
		client:    client,
		provider:  provider,
		sandbox:   sandbox,
		namespace: "default",
	}
}

// SandboxName returns the name of the underlying OpenShell sandbox.
func (s *Session) SandboxName() string {
	return s.sandbox
}

// Exec runs a command inside the sandbox and returns combined output.
func (s *Session) Exec(ctx context.Context, cmd []string) (string, error) {
	result, err := s.client.RunCommand(ctx, s.provider, s.sandbox, cmd)
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
func (s *Session) Delete(ctx context.Context) (*v1.DeletionResult, error) {
	return s.client.DeleteSandbox(ctx, s.provider, s.sandbox)
}
