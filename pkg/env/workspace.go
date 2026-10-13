// Package env provides OpenShell session management for Norman workspaces.
package env

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	maxRetries = 3
	baseBackoff     = 1 * time.Second
	maxBackoff      = 8 * time.Second
)

// WorkspaceManager handles mapping workspace names to persistent OpenShell sessions.
type WorkspaceManager struct {
	client   *Client
	sessions map[string]*Session // workspace name -> session
}

// NewWorkspaceManager creates a new workspace manager bound to an OpenShell client.
func NewWorkspaceManager(client *Client) *WorkspaceManager {
	return &WorkspaceManager{
		client:   client,
		sessions: make(map[string]*Session),
	}
}

// CreateWorkspace creates a persistent sandbox for the given workspace name.
func (m *WorkspaceManager) CreateWorkspace(ctx context.Context, workspace string) (*Session, error) {
	sandbox, err := m.client.CreateSandbox(ctx, defaultProvider, workspace)
	if err != nil {
		return nil, fmt.Errorf("create workspace %s: %w", workspace, err)
	}

	// Wait for readiness with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	_, err = m.client.WaitReady(waitCtx, defaultProvider, sandbox.Name)
	if err != nil {
		return nil, fmt.Errorf("wait ready workspace %s: %w", workspace, err)
	}

	session := NewSession(m.client, defaultProvider, sandbox.Name)
	m.sessions[workspace] = session
	log.Printf("[env] Created workspace '%s' → sandbox '%s'", workspace, sandbox.Name)

	return session, nil
}

// GetSession returns an existing session for the workspace, or creates one if needed.
func (m *WorkspaceManager) GetSession(ctx context.Context, workspace string) (*Session, error) {
	// Check cache first
	if session, ok := m.sessions[workspace]; ok {
		return session, nil
	}

	// Try to recover a crashed sandbox
	session, err := m.recoverOrCreate(ctx, workspace)
	if err != nil {
		return nil, err
	}

	m.sessions[workspace] = session
	return session, nil
}

// recoverOrCreate attempts to find an existing sandbox by name, or creates a new one.
func (m *WorkspaceManager) recoverOrCreate(ctx context.Context, workspace string) (*Session, error) {
	// Try to get existing sandbox by name (may be crashed/stopped)
	sb, err := m.client.GetSandbox(ctx, defaultProvider, workspace)
	if err == nil && sb != nil {
		log.Printf("[env] Found existing sandbox '%s' for workspace", workspace)
		session := NewSession(m.client, defaultProvider, sb.Name)

		// Attempt recovery with retries
		for attempt := 0; attempt < maxRetries; attempt++ {
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err = m.client.WaitReady(waitCtx, defaultProvider, sb.Name)
			cancel()

			if err == nil {
				log.Printf("[env] Recovered sandbox '%s' (attempt %d)", workspace, attempt+1)
				return session, nil
			}

			backoff := time.Duration(attempt) * baseBackoff
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			log.Printf("[env] WaitReady failed for '%s' (attempt %d/%d), retrying in %v: %v",
				workspace, attempt+1, maxRetries, backoff, err)

			time.Sleep(backoff)
		}

		// All retries exhausted — create a new sandbox
		log.Printf("[env] All recovery attempts failed for '%s', creating new sandbox", workspace)
		return m.CreateWorkspace(ctx, workspace)
	}

	// No existing sandbox found — create a new one
	log.Printf("[env] No existing sandbox for '%s', creating new one", workspace)
	return m.CreateWorkspace(ctx, workspace)
}

// CloseAll shuts down all active sessions.
func (m *WorkspaceManager) CloseAll(ctx context.Context) {
	for name, session := range m.sessions {
		if _, err := session.Delete(ctx); err != nil {
			log.Printf("[env] Error deleting session '%s': %v", name, err)
		}
	}
	m.sessions = make(map[string]*Session)
}
