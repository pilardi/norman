// Package env provides OpenShell session management for Norman workspaces.
package env

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

// SandboxName generates the OpenShell sandbox name from a workspace ID.
// The convention is "ws-" + the workspace ID with dashes removed, truncated to 15 characters.
// Returns an error if workspaceID is empty — a nil workspace is not valid.
func SandboxName(workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", fmt.Errorf("workspace ID must not be empty")
	}
	cleanName := strings.ReplaceAll(workspaceID, "-", "")
	if len(cleanName) > 15 {
		cleanName = cleanName[:15]
	}
	return "ws-" + cleanName, nil
}

// SandboxName returns the actual SDK-assigned sandbox name for a workspace.
// It checks the cached definition first (set at creation time), falling back to
// the computed convention if no definition is available.
func (m *WorkspaceManager) SandboxName(workspaceID string) (string, error) {
	def := m.GetDefinition(workspaceID)
	if def != nil && def.SandboxName != "" {
		return def.SandboxName, nil
	}
	return SandboxName(workspaceID)
}

// WorkspaceManager manages workspace-to-sandbox mappings with context file injection.
type WorkspaceManager struct {
	mu          sync.Mutex
	client      *Client
	serverURL   string
	sessions    map[string]*Session             // session ID -> session
	definitions map[string]*WorkspaceDefinition // workspace name -> definition (lazy-loaded)
}

const (
	maxRetries  = 3
	baseBackoff = 1 * time.Second
	maxBackoff  = 8 * time.Second
)

// WorkspaceDefinition describes the identity and purpose of a workspace.
// It is injected into the LLM's system prompt so the agent understands
// what domain it is operating in, rather than just seeing a file tree.
type WorkspaceDefinition struct {
	Name        string   `json:"name"`
	Purpose     string   `json:"purpose"`         // High-level description of what this workspace addresses
	Scope       []string `json:"scope,omitempty"` // Subsystems/repos/services covered
	Tags        []string `json:"tags,omitempty"`  // Keywords for skill activation matching
	SandboxName string   `json:"sandbox_name"`    // Actual OpenShell-assigned sandbox name
}

// ToMap converts a WorkspaceDefinition to a map[string]interface{} for JSON persistence.
func (w *WorkspaceDefinition) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"name":         w.Name,
		"purpose":      w.Purpose,
		"sandbox_name": w.SandboxName,
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
	if sandboxName, ok := m["sandbox_name"].(string); ok {
		w.SandboxName = sandboxName
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
// No session is created — sessions are created when a user opens a new session.
func (m *WorkspaceManager) CreateWorkspace(ctx context.Context, workspace string, sessionId string) (*v1.Sandbox, error) {
	ctxFile := &ContextFile{
		WorkspaceID:   workspace,
		WorkspaceName: workspace,
	}
	sandboxName, err := SandboxName(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox name for workspace %s: %w", workspace, err)
	}
	sb, err := m.client.CreateSandbox(ctx, defaultProvider, sandboxName, ctxFile, sessionId)
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

	log.Printf("[env] Created workspace '%s' → sandbox name='%s' (passed: %q)", workspace, sb.Name, sandboxName)

	return sb, nil
}

// GetSession returns an existing session for the workspace, or creates one if needed.
func (m *WorkspaceManager) GetSession(ctx context.Context, workspace string, sessionId string) (*Session, error) {
	// Check cache first — sessions are always created with a valid sessionId.
	if session, ok := m.sessions[sessionId]; ok {
		return session, nil
	}

	// Try to recover a crashed sandbox
	session, err := m.recoverOrCreate(ctx, workspace, sessionId)
	if err != nil {
		return nil, err
	}

	m.sessions[sessionId] = session
	return session, nil
}

// recoverOrCreate attempts to find an existing sandbox by name, or creates a new one.
func (m *WorkspaceManager) recoverOrCreate(ctx context.Context, workspace string, sessionId string) (*Session, error) {
	var session *Session

	// Use the actual SDK-assigned sandbox name (falls back to computed convention).
	sandboxName, err := m.SandboxName(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox name for workspace %s: %w", workspace, err)
	}

	// Try to get existing sandbox by name (may be crashed/stopped).
	sb, err := m.client.GetSandbox(ctx, defaultProvider, sandboxName)
	if err == nil && sb != nil {
		log.Printf("[env] Found existing sandbox '%s' for workspace '%s' (session %s)", sandboxName, workspace, sessionId[:8])
		session = NewSession(m.client, defaultProvider, sb.Name, sessionId)

		// Attempt recovery with retries
		for attempt := 0; attempt < maxRetries; attempt++ {
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err = m.client.WaitReadyWithTimeout(waitCtx, defaultProvider, sb.Name, 30*time.Second)
			cancel()

			if err == nil {
				log.Printf("[env] Recovered sandbox '%s' (attempt %d)", sandboxName, attempt+1)
				return session, nil
			}

			backoff := time.Duration(attempt) * baseBackoff
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			log.Printf("[env] WaitReady failed for '%s' (attempt %d/%d), retrying in %v: %v",
				sandboxName, attempt+1, maxRetries, backoff, err)

			time.Sleep(backoff)
		}

		// All retries exhausted — create a new sandbox
		log.Printf("[env] All recovery attempts failed for '%s', creating new sandbox", sandboxName)
		sb, err = m.CreateWorkspace(ctx, workspace, sessionId)
		if err != nil {
			return nil, err
		}
		session = NewSession(m.client, defaultProvider, sb.Name, sessionId)
		m.sessions[sessionId] = session
		return session, nil
	}

	// No existing sandbox found — create a new one.
	log.Printf("[env] No existing sandbox for '%s' (session %s), creating new one", sandboxName, sessionId[:8])
	sb, err = m.CreateWorkspace(ctx, workspace, sessionId)
	if err != nil {
		return nil, err
	}
	session = NewSession(m.client, defaultProvider, sb.Name, sessionId)
	m.sessions[sessionId] = session
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

// CloseSandbox deletes the sandbox associated with a workspace by name.
func (m *WorkspaceManager) CloseSandbox(ctx context.Context, workspace string) error {
	sandboxName, err := m.SandboxName(workspace)
	if err != nil {
		return fmt.Errorf("resolve sandbox name for workspace %s: %w", workspace, err)
	}
	return m.client.DeleteSandbox(ctx, defaultProvider, sandboxName)
}

// StopSandbox stops a running sandbox (preserves it for later start).
func (m *WorkspaceManager) StopSandbox(ctx context.Context, workspace string) error {
	sandboxName, err := m.SandboxName(workspace)
	if err != nil {
		return fmt.Errorf("resolve sandbox name for workspace %s: %w", workspace, err)
	}
	log.Printf("[env] StopSandbox: workspace=%s sandboxName=%s", workspace, sandboxName)
	return m.client.StopSandbox(ctx, defaultProvider, sandboxName)
}

// StartSandbox starts a stopped sandbox. Returns error if sandbox doesn't exist.
func (m *WorkspaceManager) StartSandbox(ctx context.Context, workspace string) error {
	sandboxName, err := m.SandboxName(workspace)
	if err != nil {
		return fmt.Errorf("resolve sandbox name for workspace %s: %w", workspace, err)
	}
	log.Printf("[env] StartSandbox: workspace=%s sandboxName=%s", workspace, sandboxName)
	return m.client.StartSandbox(ctx, defaultProvider, sandboxName)
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

// Sessions returns a copy of the session map for iteration by external callers.
func (m *WorkspaceManager) Sessions() map[string]*Session {
	result := make(map[string]*Session, len(m.sessions))
	for k, v := range m.sessions {
		result[k] = v
	}
	return result
}

// GetIdleSince returns the time when the workspace was last active, or nil if never used.
func (m *WorkspaceManager) GetIdleSince(workspace string) *time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[workspace]; ok {
		return session.LastActivity()
	}
	return nil
}

// IsIdle checks if a workspace has been idle for longer than the given threshold.
func (m *WorkspaceManager) IsIdle(workspace string, threshold time.Duration) bool {
	lastUsed := m.GetIdleSince(workspace)
	if lastUsed == nil {
		return false // never used is not "idle" in the auto-stop sense
	}
	return time.Since(*lastUsed) > threshold
}

// UpdateLastActivity updates the last activity timestamp for a workspace session.
func (m *WorkspaceManager) UpdateLastActivity(workspace string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[workspace]; ok {
		now := time.Now()
		session.lastActivity = &now
	}
}

// StartIdleCleanupLoop starts a background goroutine that periodically checks
// for idle workspaces and stops them after the given timeout. Returns a cancel func.
func (m *WorkspaceManager) StartIdleCleanupLoop(timeout time.Duration, stopCh chan struct{}) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.mu.Lock()
				for name, session := range m.sessions {
					if session.LastActivity() != nil && time.Since(*session.lastActivity) > timeout {
						log.Printf("[env] Auto-stopping idle workspace '%s' (threshold: %v)", name, timeout)
						delete(m.sessions, name)
					}
				}
				m.mu.Unlock()

			case <-stopCh:
				return
			}
		}
	}()
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

// Exists checks whether a workspace has been created without side effects.
func (m *WorkspaceManager) Exists(ctx context.Context, sessionId string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.sessions[sessionId]
	return ok
}

// Session returns the current session state as a read-only view.
func (m *WorkspaceManager) Session() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) == 0 {
		return nil
	}
	// Return the first (and typically only) session for this workspace manager instance.
	for _, s := range m.sessions {
		return s
	}
	return nil
}

// Compile-time interface satisfaction check.
var _ Workspace = (*WorkspaceManager)(nil)

// Create creates a new workspace-backed session. This is the ONLY path for creating
// new workspaces (user-requested action). It creates a sandbox with context file,
// waits for readiness, and returns the resulting session.
func (m *WorkspaceManager) Create(ctx context.Context, sessionId string) (*Session, error) {
	// Extract workspace ID from the session data or use a default.
	// The caller is responsible for ensuring the workspace exists.
	sandboxName := "ws-" + strings.ReplaceAll(sessionId, "-", "")[:15]

	ctxFile := &ContextFile{
		WorkspaceID:   sessionId,
		WorkspaceName: sessionId,
	}

	sb, err := m.client.CreateSandbox(ctx, defaultProvider, sandboxName, ctxFile, sessionId)
	if err != nil {
		return nil, fmt.Errorf("create workspace session %s: %w", sessionId, err)
	}

	// Wait for readiness with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	_, err = m.client.WaitReadyWithTimeout(waitCtx, defaultProvider, sb.Name, 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("wait ready workspace session %s: %w", sessionId, err)
	}

	log.Printf("[env] Created session '%s' → sandbox name='%s'", sessionId, sb.Name)

	session := NewSession(m.client, defaultProvider, sb.Name, sessionId)
	m.sessions[sessionId] = session
	return session, nil
}

// Get retrieves an existing session by ID. Returns NotFound error if non-existent.
// Does NOT auto-create — caller must explicitly request creation via Create().
func (m *WorkspaceManager) Get(ctx context.Context, sessionId string) (*Session, error) {
	m.mu.Lock()
	session, ok := m.sessions[sessionId]
	m.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("session %s not found: workspace must be created via Create()", sessionId)
	}

	return session, nil
}

// Delete removes a workspace and its associated sandbox.
func (m *WorkspaceManager) Delete(ctx context.Context, sessionId string) error {
	m.mu.Lock()
	session, ok := m.sessions[sessionId]
	delete(m.sessions, sessionId)
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("session %s not found for deletion", sessionId)
	}

	return session.Delete(ctx)
}
