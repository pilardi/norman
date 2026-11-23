// Package env defines the Workspace interface — the single public abstraction
// for session and workspace lifecycle management used by the agent loop and server.
package env

import (
	"context"
	"time"
)

// Workspace is the interface that abstracts workspace lifecycle management.
// It is the only public entry point for session and workspace operations; implementation details
// (sandboxes, providers, context files) are internal to pkg/env.
//
// Design rationale:
//   - Get() is non-creating — it fails if the session doesn't exist. This enforces
//     the principle that only user-requested actions create new workspaces.
//   - Delete() removes a workspace and its associated sandbox cleanly.
//   - All sandbox lifecycle, definition management, and idle cleanup operations are exposed
//     through this interface to ensure proper encapsulation.
type Workspace interface {
	// Session management
	// GetSession retrieves an existing session by ID. Returns NotFound error if non-existent.
	GetSession(ctx context.Context, sessionId string) (*Session, error)

	// DeleteSession removes a session.
	DeleteSession(ctx context.Context, sessionId string) error

	// GetSessionWorkspace returns the workspace name for a given session ID.
	GetSessionWorkspace(sessionId string) (string, error)

	// RunCommand executes a command in the session's underlying environment.
	// Returns combined stdout+stderr output, exit code, and any error.
	RunCommand(ctx context.Context, sessionId string, command string) (string, int, error)

	// GetLogs returns log lines for the session.
	GetLogs(ctx context.Context, sessionId string) ([]string, error)

	// UpdateActivity updates the last activity timestamp for a session by its ID.
	UpdateActivity(ctx context.Context, sessionId string)

	// DeleteSessionsByWorkspace deletes all sessions associated with a given workspace name.
	DeleteSessionsByWorkspace(workspaceName string) error

	// Workspace environment lifecycle
	// CreateWorkspace creates a persistent OpenShell environment for the given workspace name.
	// No session is created — sessions are created when a user opens a new session.
	// Returns the environment identifier on success.
	CreateWorkspace(ctx context.Context, workspace string, sessionId string) (string, error)

	// StopWorkspace stops a running workspace environment (preserves it for later start).
	StopWorkspace(ctx context.Context, workspace string) error

	// StartWorkspace starts a stopped workspace environment. Returns error if environment doesn't exist.
	StartWorkspace(ctx context.Context, workspace string) error

	// DeleteWorkspaceResources deletes the workspace's underlying environment resources.
	DeleteWorkspaceResources(ctx context.Context, workspace string) error

	// Definition management
	// SetDefinition assigns a workspace definition to the manager's cache.
	SetDefinition(workspace string, def *WorkspaceDefinition)

	// GetDefinition returns the cached workspace definition, or nil if not set.
	GetDefinition(workspace string) *WorkspaceDefinition

	// Idle management
	// GetIdleSince returns the time when the workspace was last active, or nil if never used.
	GetIdleSince(workspace string) *time.Time

	// IsIdle checks if a workspace has been idle for longer than the given threshold.
	IsIdle(workspace string, threshold time.Duration) bool

	// StartIdleCleanupLoop starts a background goroutine that periodically checks
	// for idle workspaces and stops them after the given timeout.
	StartIdleCleanupLoop(timeout time.Duration, stopCh chan struct{})
}
