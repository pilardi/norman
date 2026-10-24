// Package env provides OpenShell session management for Norman workspaces.
package env

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

// WorkspaceManager manages workspace-to-sandbox mappings with context file injection.
type WorkspaceManager struct {
	client      *Client
	serverURL   string
	sessions    map[string]*Session // workspace name -> session
	definitions map[string]*WorkspaceDefinition // workspace name -> definition (lazy-loaded)
}

const (
	maxRetries = 3
	baseBackoff     = 1 * time.Second
	maxBackoff      = 8 * time.Second
)

// WorkspaceDefinition describes the identity and purpose of a workspace.
// It is injected into the LLM's system prompt so the agent understands
// what domain it is operating in, rather than just seeing a file tree.
type WorkspaceDefinition struct {
	Name    string   `json:"name"`
	Purpose string   `json:"purpose"` // High-level description of what this workspace addresses
	Scope   []string `json:"scope,omitempty"` // Subsystems/repos/services covered
	Tags    []string `json:"tags,omitempty"`  // Keywords for skill activation matching
}

// ToMap converts a WorkspaceDefinition to a map[string]interface{} for JSON persistence.
func (w *WorkspaceDefinition) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"name":    w.Name,
		"purpose": w.Purpose,
	}
	if len(w.Scope) > 0 {
		scope := make([]interface{}, len(w.Scope))
		for i, s := range w.Scope {
			scope[i] = s
		}
		m["scope"] = scope
	}
	if len(w.Tags) > 0 {
		tags := make([]interface{}, len(w.Tags))
		for i, t := range w.Tags {
			tags[i] = t
		}
		m["tags"] = tags
	}
	return m
}

// FromMap reconstructs a WorkspaceDefinition from a persisted map.
func WorkspaceDefinitionFromMap(m map[string]interface{}) *WorkspaceDefinition {
	w := &WorkspaceDefinition{}
	if name, ok := m["name"].(string); ok {
		w.Name = name
	}
	if purpose, ok := m["purpose"].(string); ok {
		w.Purpose = purpose
	}
	if scope, ok := m["scope"].([]interface{}); ok {
		for _, v := range scope {
			if s, ok := v.(string); ok {
				w.Scope = append(w.Scope, s)
			}
		}
	}
	if tags, ok := m["tags"].([]interface{}); ok {
		for _, v := range tags {
			if t, ok := v.(string); ok {
				w.Tags = append(w.Tags, t)
			}
		}
	}
	return w
}

// NewWorkspaceManager creates a new workspace manager bound to an OpenShell client.
func NewWorkspaceManager(client *Client, serverURL string) *WorkspaceManager {
	return &WorkspaceManager{
		client:      client,
		serverURL:   serverURL,
		sessions:    make(map[string]*Session),
		definitions: make(map[string]*WorkspaceDefinition),
	}
}

// CreateWorkspace creates a persistent OpenShell sandbox for the given workspace name.
// The mTLS paths are injected into the context file; server URL comes from
// the NORMAN_SERVER_URL environment variable available inside the sandbox.
// No session is created — sessions are created when a user opens a new session.
func (m *WorkspaceManager) CreateWorkspace(ctx context.Context, workspace string, sessionId string) (*v1.Sandbox, error) {
	certPath, keyPath := m.client.GetMTLSPaths()
	ctxFile := &ContextFile{
		WorkspaceID:    workspace,
		WorkspaceName:  workspace,
		MTLSCertPath:   certPath,
		MTLSKeyPath:    keyPath,
	}
	sb, err := m.client.CreateSandbox(ctx, defaultProvider, workspace, ctxFile, sessionId)
	if err != nil {
		return nil, fmt.Errorf("create workspace %s: %w", workspace, err)
	}

	// Wait for readiness with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	_, err = m.client.WaitReadyWithTimeout(waitCtx, defaultProvider, sb.Name, 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("wait ready workspace %s: %w", workspace, err)
	}

	log.Printf("[env] Created workspace '%s' → sandbox '%s'", workspace, sb.Name)

	return sb, nil
}

// GetSession returns an existing session for the workspace, or creates one if needed.
func (m *WorkspaceManager) GetSession(ctx context.Context, workspace string, sessionId string) (*Session, error) {
	// Check cache first — sessions are always created with a valid sessionId.
	if session, ok := m.sessions[workspace]; ok {
		return session, nil
	}

	// Try to recover a crashed sandbox
	session, err := m.recoverOrCreate(ctx, workspace, sessionId)
	if err != nil {
		return nil, err
	}

	m.sessions[workspace] = session
	return session, nil
}

// recoverOrCreate attempts to find an existing sandbox by name, or creates a new one.
func (m *WorkspaceManager) recoverOrCreate(ctx context.Context, workspace string, sessionId string) (*Session, error) {
	var session *Session

	// Try to get existing sandbox by name (may be crashed/stopped)
	sb, err := m.client.GetSandbox(ctx, defaultProvider, workspace)
	if err == nil && sb != nil {
		log.Printf("[env] Found existing sandbox '%s' for workspace", workspace)
		session = NewSession(m.client, defaultProvider, sb.Name, sessionId)

		// Attempt recovery with retries
		for attempt := 0; attempt < maxRetries; attempt++ {
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err = m.client.WaitReadyWithTimeout(waitCtx, defaultProvider, sb.Name, 30*time.Second)
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
		sb, err = m.CreateWorkspace(ctx, workspace, sessionId)
		if err != nil {
			return nil, err
		}
		session = NewSession(m.client, defaultProvider, sb.Name, sessionId)
		m.sessions[workspace] = session
		return session, nil
	}

	// No existing sandbox found — create a new one
	log.Printf("[env] No existing sandbox for '%s', creating new one", workspace)
	sb, err = m.CreateWorkspace(ctx, workspace, sessionId)
	if err != nil {
		return nil, err
	}
	session = NewSession(m.client, defaultProvider, sb.Name, sessionId)
	m.sessions[workspace] = session
	return session, nil
}

// CloseAll shuts down all active sessions.
func (m *WorkspaceManager) CloseAll(ctx context.Context) {
	for name, session := range m.sessions {
		if err := session.Delete(ctx); err != nil {
			log.Printf("[env] Error deleting session '%s': %v", name, err)
		}
	}
	m.sessions = make(map[string]*Session)
	m.definitions = make(map[string]*WorkspaceDefinition)
}

// SetDefinition assigns a workspace definition to the manager's cache.
func (m *WorkspaceManager) SetDefinition(workspace string, def *WorkspaceDefinition) {
	if def != nil {
		m.definitions[workspace] = def
	} else {
		delete(m.definitions, workspace)
	}
}

// GetDefinition returns the cached workspace definition, or nil if not set.
func (m *WorkspaceManager) GetDefinition(workspace string) *WorkspaceDefinition {
	return m.definitions[workspace]
}

// DefinitionAsSystemPrompt converts a WorkspaceDefinition into a markdown block
// suitable for injection into the LLM's system prompt. Returns empty string if no definition exists.
func (w *WorkspaceDefinition) AsSystemPrompt() string {
	if w == nil || w.Name == "" {
		return ""
	}

	var parts []string
	parts = append(parts, "## Workspace Identity")
	parts = append(parts, "")
	parts = append(parts, fmt.Sprintf("**Name:** %s", w.Name))

	if w.Purpose != "" {
		parts = append(parts, fmt.Sprintf("**Purpose:** %s", w.Purpose))
	}

	if len(w.Scope) > 0 {
		scopes := strings.Join(w.Scope, ", ")
		parts = append(parts, fmt.Sprintf("**Scope:** %s", scopes))
	}

	if len(w.Tags) > 0 {
		tags := strings.Join(w.Tags, ", ")
		parts = append(parts, fmt.Sprintf("**Tags:** %s", tags))
	}

	parts = append(parts, "")
	parts = append(parts, "When creating wiki entries or making decisions:")
	parts = append(parts, "- Tag entries with `workspace: <name>` for scoping")
	parts = append(parts, "- Match skills against workspace tags for domain-relevant activation")
	parts = append(parts, "- Preserve workspace-specific decisions during compression; discard generic filler")

	return strings.Join(parts, "\n")
}
