// Package env defines the Workspace interface that encapsulates all sandbox
// lifecycle operations. This is the single abstraction the agent loop depends on,
// replacing the previous pattern of three separate OpenShell references (openShell,
// openShellClient, workspaceManager).
package env

import (
	"context"
)

// Workspace is the interface that abstracts workspace-to-sandbox lifecycle management.
// It encapsulates all operations related to creating, retrieving, checking existence,
// and deleting workspaces backed by OpenShell sandboxes.
//
// Design rationale:
//   - Get() is non-creating — it fails if the session doesn't exist. This enforces
//     the principle that only user-requested actions create new workspaces.
//   - Create() is the sole path for new workspace creation. No shortcuts or auto-creation.
//   - Exists() provides a read-only check without side effects (no sandbox state changes).
//   - Delete() removes a workspace and its associated sandbox cleanly.
//   - Session() returns the current session state as a read-only view.
type Workspace interface {
	// Create creates a new workspace-backed session. Returns an error if creation fails.
	// This is the ONLY path for creating new workspaces (user-requested action).
	Create(ctx context.Context, sessionId string) (*Session, error)

	// Get retrieves an existing session by ID. Returns NotFound error if non-existent.
	// Does NOT auto-create — caller must explicitly request creation via Create().
	Get(ctx context.Context, sessionId string) (*Session, error)

	// Exists checks whether a workspace has been created without side effects.
	Exists(ctx context.Context, sessionId string) bool

	// Delete removes a workspace and its associated sandbox.
	Delete(ctx context.Context, sessionId string) error

	// Session returns the current session state (read-only view).
	Session() *Session
}
