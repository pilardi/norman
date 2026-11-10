// Package env defines the Workspace interface — the single public abstraction
// for session lifecycle management used by the agent loop.
package env

import (
	"context"
)

// Workspace is the interface that abstracts workspace lifecycle management.
// It is the only public entry point for session operations; implementation details
// (sandboxes, providers, context files) are internal to pkg/env.
//
// Design rationale:
//   - Get() is non-creating — it fails if the session doesn't exist. This enforces
//     the principle that only user-requested actions create new workspaces.
//   - Delete() removes a workspace and its associated sandbox cleanly.
type Workspace interface {
	// Get retrieves an existing session by ID. Returns NotFound error if non-existent.
	Get(ctx context.Context, sessionId string) (*Session, error)

	// Delete removes a workspace and its associated sandbox.
	Delete(ctx context.Context, sessionId string) error

	// SessionWorkspace returns the workspace name for a given session ID.
	SessionWorkspace(sessionId string) (string, error)

	// RunCommand executes a command in the session's underlying environment.
	// Returns combined stdout+stderr output, exit code, and any error.
	RunCommand(ctx context.Context, sessionId string, command string) (string, int, error)

	// GetLogs returns log lines for the session.
	GetLogs(ctx context.Context, sessionId string) ([]string, error)

	// UpdateActivity updates the last activity timestamp for a session.
	UpdateActivity(ctx context.Context, sessionId string)

	// Sessions returns all registered sessions keyed by session ID.
	Sessions() map[string]*Session
}
