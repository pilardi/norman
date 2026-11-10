// Package env defines the Workspace interface that encapsulates all sandbox
// lifecycle operations. This is the single abstraction the agent loop depends on,
// replacing the previous pattern of three separate OpenShell references (openShell,
// openShellClient, workspaceManager).
package env

import (
	"context"
)

// Workspace is the interface that abstracts workspace lifecycle management.
// It encapsulates all operations related to creating, retrieving, checking existence,
// and deleting workspaces backed by OpenShell sandboxes.
//
// Design rationale:
//   - Get() is non-creating — it fails if the session doesn't exist. This enforces
//     the principle that only user-requested actions create new workspaces.
//   - Exists() provides a read-only check without side effects (no sandbox state changes).
//   - Delete() removes a workspace and its associated sandbox cleanly.
//   - RunCommand() executes a command string in the active sandbox, returning output/exit code.
//   - GetLogs() returns log lines for the session's sandbox.
type Workspace interface {
	// Get retrieves an existing session by ID. Returns NotFound error if non-existent.
	Get(ctx context.Context, sessionId string) (*Session, error)

	// Exists checks whether a workspace has been created without side effects.
	Exists(ctx context.Context, sessionId string) bool

	// Delete removes a workspace and its associated sandbox.
	Delete(ctx context.Context, sessionId string) error

	// SessionWorkspace returns the workspace name for a given session ID.
	SessionWorkspace(sessionId string) (string, error)

	// RunCommand executes a command string in the active sandbox.
	// Returns combined stdout+stderr output, exit code, and any error.
	RunCommand(ctx context.Context, sessionId string, command string) (string, int, error)

	// GetLogs returns log lines for the session's sandbox.
	GetLogs(ctx context.Context, sessionId string) ([]string, error)

	// UpdateActivity updates the last activity timestamp for a session.
	UpdateActivity(ctx context.Context, sessionId string)

	// Sessions returns all registered sessions keyed by session ID.
	Sessions() map[string]*Session
}
