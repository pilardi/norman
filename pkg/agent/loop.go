// Package agent implements the core agent loop that resolves skills,
// executes tasks via the workspace, and reports progress.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/llm"
	"github.com/norman/norman/pkg/mcp"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
)

// Task represents an in-flight task (emergent or manual) with its execution state.
type Task struct {
	ID           string
	Prompt       string
	Status       protocol.TaskStatus
	Workspace    string
	SessionId    string
	Output       []byte
	Error        string
	ProviderName string // LLM provider name used for this task (empty = workspace default)
	RetryCount   int
	CreatedAt    time.Time
	RichOutput   *protocol.RichOutput      // structured metadata about the final output
	events       []protocol.WebSocketEvent // granular event log for ?type=events REST access
}

// Loop is the main agent loop managing session messages and emergent/manual task execution.
type Loop struct {
	mu            sync.Mutex
	tasks         map[string]*Task   // taskID -> Task state
	store         *memory.Store      // persistent state
	rawSources    *memory.RawSources // raw environment snapshots for context injection
	activator     *skills.Activator
	config        *config.Config
	Ws            env.Workspace                 // single workspace abstraction replacing openShell/openShellClient/workspaceManager
	llmClient     llm.LLM                       // LLM API client (optional, nil = shell-only mode)
	providerCache map[string]*llm.ClientAdapter // cached providers keyed by name
	wsHub         WSHubPublisher
	notifier      *notify.Notifier
	resultsCh     chan scheduler.TaskResult // results from scheduler (nil if not wired)
	budget        *budget.SessionState      // token budget tracker (nil if disabled)
	workspaceDef  *env.WorkspaceDefinition  // workspace identity for system prompt injection
	mcpClient     *mcp.Client               // MCP client for remote tool discovery/invocation (optional)
	systemPrompt  string                    // immutable system prompt (e.g. agent identity/facts) injected as first "system" role message
	debug         bool                      // if true, log detailed message contents before sending to LLM
	processing    bool                      // whether the loop is currently processing (for graceful shutdown)
}

// WorkspaceConfig returns the merged (system + workspace overrides) config for a given workspace.
func (l *Loop) WorkspaceConfig(workspaceID string) *config.WorkspaceConfig {
	if l.config == nil {
		return &config.WorkspaceConfig{BaseDir: "", Model: "", MaxTokens: 0, Temperature: 0}
	}
	return l.config.MergeWorkspaceConfig(workspaceID, nil)
}

// Model returns the resolved model name for a workspace (system default or override).
func (l *Loop) Model(workspaceID string) string {
	ws := l.WorkspaceConfig(workspaceID)
	return ws.Model
}

// Temperature returns the resolved temperature for a workspace.
func (l *Loop) Temperature(workspaceID string) float64 {
	ws := l.WorkspaceConfig(workspaceID)
	return ws.Temperature
}

// MaxTokens returns the resolved max tokens for a workspace.
func (l *Loop) MaxTokens(workspaceID string) int {
	ws := l.WorkspaceConfig(workspaceID)
	return ws.MaxTokens
}

// SetWorkspaceDefinition assigns the workspace identity to the loop's system prompt context.
func (l *Loop) SetWorkspaceDefinition(def *env.WorkspaceDefinition) {
	l.workspaceDef = def
}

// GetWorkspaceDefinition returns the assigned workspace definition, or nil if not set.
func (l *Loop) GetWorkspaceDefinition() *env.WorkspaceDefinition {
	return l.workspaceDef
}

// ActivateWithSession activates a skill with dependency install inside the given workspace.
// Falls back to local venv if session is nil. Deploys skill files into the workspace first.
func (l *Loop) ActivateWithSession(name string, session *env.Session) error {
	if l.activator != nil {
		// Deploy skill files into workspace before activation
		if session != nil {
			_, err := l.activator.DeployToWorkspace(name, session)
			if err != nil {
				log.Printf("[loop] deploy to workspace for '%s': %v", name, err)
				// Non-fatal — continue with activation if files are already present
			}
		}

		_, err := l.activator.Activate(name, session)
		return err
	}
	return nil
}

// Activator returns the agent's skills activator (for scheduler callback wiring).
func (l *Loop) Activator() *skills.Activator {
	return l.activator
}

// ActivateSkill directly activates a skill by name. This is used by scheduled/deferred tasks
// to activate their associated skill before execution (P4: task→skill mapping).
func (l *Loop) ActivateSkill(name string, session *env.Session) error {
	if l.activator == nil {
		return fmt.Errorf("activator not initialized")
	}
	_, err := l.activator.Activate(name, session) // nil = local venv fallback
	if err != nil {
		log.Printf("[loop] activate skill '%s': %v", name, err)
		return err
	}
	log.Printf("[loop] activated skill '%s' for task execution", name)
	return nil
}

// resolveWorkspace resolves a workspace ID from a session ID.
// Reads workspace_id directly from the session's persisted data.
// If not set (empty string), returns "" — no fallback derivation needed.
func (l *Loop) resolveWorkspace(sessionID string) string {
	if l.store != nil {
		data, err := l.store.LoadSession(sessionID)
		if err == nil {
			if wid, ok := data["workspace_id"].(string); ok && wid != "" {
				return wid
			}
		}
	}

	return ""
}

// GetSession returns a session for the given workspace ID.
// Returns NotFound error if the session doesn't exist — no auto-creation.
func (l *Loop) GetSession(workspaceID string, sessionId string) (*env.Session, error) {
	session, err := l.Ws.Get(context.Background(), sessionId)
	if err != nil {
		return nil, fmt.Errorf("session %s not found for workspace %s: %w", sessionId[:8], workspaceID, err)
	}

	log.Printf("[loop] using existing session '%s' for workspace '%s'", sessionId[:8], workspaceID)
	return session, nil
}

// WSHubPublisher sends WebSocket push events.
type WSHubPublisher interface {
	BroadcastEvent(event protocol.WebSocketEvent)
	BroadcastSession(sessionID string, event protocol.WebSocketEvent)
	BroadcastGranular(event protocol.WebSocketEventEvent, taskID, sessionID, message string)
}

// New creates a new agent loop with the given dependencies.
// The workspace parameter implements env.Workspace and encapsulates all
// OpenShell interaction — the loop no longer knows about OpenShell internals.
func New(cfg *config.Config, store *memory.Store, rawSources *memory.RawSources, activator *skills.Activator, wsHub WSHubPublisher, ws env.Workspace) *Loop {
	return &Loop{
		tasks:         make(map[string]*Task),
		store:         store,
		rawSources:    rawSources,
		activator:     activator,
		config:        cfg,
		Ws:            ws,
		wsHub:         wsHub,
		budget:        nil, // disabled by default; set via SetBudget()
		providerCache: make(map[string]*llm.ClientAdapter),
	}
}

// WithNotifier injects a notification notifier into the loop. Returns the modified Loop.
func (l *Loop) WithNotifier(n *notify.Notifier) *Loop {
	l.notifier = n
	return l
}

// WithMCPClient injects an MCP client into the loop for remote tool discovery/invocation.
// The client must be connected to an SSE endpoint before the loop starts processing.
func (l *Loop) WithMCPClient(c *mcp.Client) *Loop {
	l.mcpClient = c
	return l
}

// WithResultsChannel injects a scheduler results channel into the loop for notification delivery.
func (l *Loop) WithResultsChannel(ch chan scheduler.TaskResult) *Loop {
	l.resultsCh = ch
	return l
}

// WithBudget configures the loop with a budget tracker. Returns the modified Loop.
func (l *Loop) WithBudget(cfg *budget.BudgetConfig) *Loop {
	l.budget = budget.NewSessionState(cfg)
	return l
}

// WithLLMClient injects an LLM API client into the loop for multi-turn conversation processing.
func (l *Loop) WithLLMClient(client llm.LLM) *Loop {
	l.llmClient = client
	return l
}

// getProvider returns the ClientAdapter for a given provider name.
// It caches created clients to avoid recreating them on each task execution.
// If the requested name matches the loop's default llmClient, it returns that directly.
func (l *Loop) getProvider(name string) (*llm.ClientAdapter, error) {
	// If no explicit provider requested or no client available, fall back to default.
	if name == "" || l.llmClient == nil {
		// Try to unwrap as ClientAdapter; if it's a different implementation (e.g., mock in tests),
		// return nil to signal "use default" — the caller handles this.
		if ca, ok := l.llmClient.(*llm.ClientAdapter); ok {
			return ca, nil
		}
		return nil, nil
	}

	// Check cache first.
	if cached, ok := l.providerCache[name]; ok {
		return cached, nil
	}

	// Create new provider and cache it.
	client, err := llm.GetProviderByName(l.config.System, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider %q: %w", name, err)
	}

	l.providerCache[name] = client
	return client, nil
}

// logf logs with a [llm][{provider}] prefix using the current LLM client's provider name.
func (l *Loop) logf(format string, args ...any) {
	if l.llmClient == nil {
		log.Printf("[llm] "+format, args...)
		return
	}
	name := l.llmClient.ProviderInfo().Name
	if name == "" {
		name = "unknown"
	}
	log.Printf("[llm][%s] "+format, append([]any{name}, args...)...)
}

// WithDebug enables verbose debug logging of messages sent to the LLM.
func (l *Loop) WithDebug(on bool) *Loop {
	l.debug = on
	return l
}

// WithSystemPrompt sets the immutable system prompt injected as the first "system" role message.
func (l *Loop) WithSystemPrompt(s string) *Loop {
	l.systemPrompt = s
	return l
}

// Submit enqueues a task and begins execution.
func (l *Loop) Submit(ctx context.Context, prompt string, workspaceID string, sessionID string) (*Task, error) {
	taskID := uuid.New().String()

	// Enforce per-session task cap (configurable via max_tasks_per_session).
	if l.config != nil && l.config.System.MaxTasksPerSession > 0 {
		l.mu.Lock()
		count := 0
		for _, t := range l.tasks {
			if t.SessionId == sessionID {
				count++
			}
		}
		if count >= l.config.System.MaxTasksPerSession {
			l.mu.Unlock()
			// Evict the oldest task for this session before adding the new one.
			if err := l.evictOldestTask(sessionID); err != nil {
				log.Printf("agent: failed to evict oldest task for session %s: %v", sessionID, err)
			}
		} else {
			l.mu.Unlock()
		}
	}

	task := &Task{
		ID:        taskID,
		Prompt:    prompt,
		Status:    protocol.TaskStatusPending,
		Workspace: workspaceID,
		SessionId: sessionID,
		CreatedAt: time.Now(),
	}

	l.mu.Lock()
	l.tasks[taskID] = task
	l.mu.Unlock()

	// Persist initial state
	if err := l.store.SaveTask(taskID, taskToMap(task)); err != nil {
		log.Printf("agent: failed to persist task %s: %v", taskID, err)
	}

	// Push progress event (session-aware)
	l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &taskID,
		Message: strPtr(fmt.Sprintf("Task %s submitted", taskID)),
		Step:    intPtr(0),
	})

	go l.execute(ctx, task)
	return task, nil
}

// GetTask returns the current state of a task.
func (l *Loop) GetTask(taskID string) (*Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	task, ok := l.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	return task, nil
}

// GetLogs returns the log lines for a workspace/session by session ID.
func (l *Loop) GetLogs(ctx context.Context, sessionId string) ([]string, error) {
	return l.Ws.GetLogs(ctx, sessionId)
}

// AgentResponse represents the structured response from processing a chat message.
type AgentResponse struct {
	SessionID   string   `json:"session_id"`
	Response    string   `json:"response,omitempty"`
	ToolsUsed   []string `json:"tools_used,omitempty"`
	TaskCreated bool     `json:"task_created"`
	TaskID      string   `json:"task_id,omitempty"`
}

// SendMessage processes a chat message and returns a structured response.
// This is the only public entry point for processing messages in the agent loop.
func (l *Loop) SendMessage(ctx context.Context, sessionID string, message string, providerName string) (*AgentResponse, error) {
	taskID := uuid.New().String()

	// Resolve workspace from session ID.
	workspaceID := l.resolveWorkspace(sessionID)

	// If the session has no workspace_id, fail immediately with a clear error.
	if workspaceID == "" {
		task := &Task{
			ID:           taskID,
			Prompt:       message,
			Status:       protocol.TaskStatusFailed,
			SessionId:    sessionID,
			ProviderName: providerName,
			CreatedAt:    time.Now(),
			Workspace:    "",
		}
		task.Error = "session has no workspace_id attached"
		l.mu.Lock()
		l.tasks[taskID] = task
		l.mu.Unlock()
		l.pushCompletion(task)
		return &AgentResponse{SessionID: sessionID, Response: task.Error}, nil
	}

	task := &Task{
		ID:           taskID,
		Prompt:       message,
		Status:       protocol.TaskStatusPending,
		SessionId:    sessionID,
		ProviderName: providerName,
		CreatedAt:    time.Now(),
		Workspace:    workspaceID,
	}

	l.mu.Lock()
	l.tasks[taskID] = task
	l.mu.Unlock()

	// Process the message synchronously via execute (blocks until completion).
	l.execute(ctx, task)

	// Build response.
	resp := &AgentResponse{
		SessionID: sessionID,
		Response:  string(task.Output),
	}

	if task.Error != "" {
		resp.Response = fmt.Sprintf("Error: %s", task.Error)
	}

	return resp, nil
}

// CancelTask marks a task for cancellation.
func (l *Loop) CancelTask(taskID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	task, ok := l.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}

	if task.Status == protocol.TaskStatusRunning || task.Status == protocol.TaskStatusPending {
		task.Status = protocol.TaskStatusCancelled
		return l.store.SaveTask(taskID, taskToMap(task))
	}
	return fmt.Errorf("task %s is not cancellable (status: %s)", taskID, task.Status)
}

// evictOldestTask removes the oldest task from the in-memory map for a given session
// and deletes its persisted JSON file. Used by Submit() to enforce the per-session task cap.
func (l *Loop) evictOldestTask(sessionID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Delegate disk deletion to the store; remove from in-memory map after.
	if err := l.store.DeleteOldestTaskBySession(sessionID); err != nil {
		return err
	}

	// Remove matching task from in-memory state (agent owns this cache).
	for id, task := range l.tasks {
		if task.SessionId == sessionID {
			delete(l.tasks, id)
			break
		}
	}

	return nil
}

// DeleteTask permanently removes a task from memory and persists the deletion.
// Unlike CancelTask (which marks status=cancelled), this fully removes the task entry.
func (l *Loop) DeleteTask(taskID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	_, ok := l.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}

	delete(l.tasks, taskID)
	return l.store.DeleteTask(taskID)
}

// CreateTask creates an emergent task programmatically from the agent's planning loop.
// Unlike Submit (which is called by the server API layer), this is the agent-side
// method for creating tasks during conversation processing. It accepts optional config
// overrides (e.g., "priority", "timeout", "workspace_id") and returns the completed task.
func (l *Loop) CreateTask(ctx context.Context, prompt string, config map[string]string) (*Task, error) {
	taskID := uuid.New().String()

	// Resolve workspace from config or default to empty string.
	workspaceID := ""
	if ws, ok := config["workspace_id"]; ok && ws != "" {
		workspaceID = ws
	}

	// Warn if no workspace was resolved (the task will still be created but will fail at execution).
	if workspaceID == "" {
		log.Printf("[loop] WARNING: CreateTask called without workspace_id — task will fail during workspace execution")
	}

	// Parse priority from config (default: "normal").
	var priority protocol.TaskCreateRequestPriority
	switch strings.ToLower(config["priority"]) {
	case "low":
		priority = protocol.TaskCreateRequestPriorityLow
	case "high":
		priority = protocol.TaskCreateRequestPriorityHigh
	default:
		priority = protocol.TaskCreateRequestPriorityNormal
	}

	task := &Task{
		ID:        taskID,
		Prompt:    prompt,
		Status:    protocol.TaskStatusPending,
		Workspace: workspaceID,
		SessionId: "", // emergent tasks are not bound to a specific session
		CreatedAt: time.Now(),
	}

	// Store priority as metadata for the task.
	if err := l.store.SaveTask(taskID, map[string]interface{}{
		"id":         taskID,
		"prompt":     prompt,
		"status":     string(task.Status),
		"workspace":  workspaceID,
		"priority":   string(priority),
		"created_at": task.CreatedAt.Format(time.RFC3339),
	}); err != nil {
		log.Printf("agent: failed to persist task %s: %v", taskID, err)
	}

	// Broadcast progress event (no session-specific filtering for emergent tasks).
	l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &taskID,
		Message: strPtr(fmt.Sprintf("Emergent task %s created", taskID[:8])),
		Step:    intPtr(0),
	})

	// Execute synchronously (blocks until completion).
	l.execute(ctx, task)

	return task, nil
}

// execute runs the agent loop for a single task.
func (l *Loop) execute(ctx context.Context, task *Task) {
	defer func() {
		l.mu.Lock()
		if task.Status == protocol.TaskStatusRunning || task.Status == protocol.TaskStatusPending {
			task.Status = protocol.TaskStatusFailed
			task.Error = "execution cancelled or context expired"
		}
		l.mu.Unlock()

		if err := l.store.SaveTask(task.ID, taskToMap(task)); err != nil {
			log.Printf("agent: failed to persist final state for task %s: %v", task.ID, err)
		}
	}()

	// Validate workspace — tasks without a workspace cannot execute in the workspace.
	if task.Workspace == "" {
		task.Status = protocol.TaskStatusFailed
		task.Error = "no workspace resolved; cannot execute task without a workspace context"
		l.pushCompletion(task)
		return
	}

	// No workspace configured — coding error, must not recover.
	if l.Ws == nil {
		log.Printf("[loop] no workspace configured; task execution cannot proceed")
		task.Status = protocol.TaskStatusFailed
		task.Error = "no workspace configured; task execution cannot proceed"
		l.pushCompletion(task)
		return
	}

	// Budget threshold check at planning cycle start (before skill resolution).
	// This ensures early detection of budget breaches and triggers summarization
	// when WarnThreshold is hit, not just after API calls.
	if l.budget != nil {
		l.checkAndCompress(ctx, task.ID)
	}

	// Phase 1: Skill resolution and activation (session-aware)
	l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Resolving skills..."),
		Step:    intPtr(1),
	})

	skillsToActivate := l.resolveSkills(task.Prompt)
	if l.activator != nil {
		for _, name := range skillsToActivate {
			_, err := l.activator.Activate(name, nil) // nil = local venv fallback
			if err != nil {
				log.Printf("agent: failed to activate skill %s: %v", name, err)
			}
		}

		activeSkills := l.activator.Active()
		l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			TaskId:  &task.ID,
			Message: strPtr(fmt.Sprintf("Activated skills: %s", strings.Join(activeSkills, ", "))),
			Step:    intPtr(2),
		})
	} else {
		l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			TaskId:  &task.ID,
			Message: strPtr("Activated skills: (none)"),
			Step:    intPtr(2),
		})
	}

	// Phase 2: Execute — multi-turn agent loop with LLM using existing workspace.
	l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Executing task..."),
		Step:    intPtr(3),
	})

	// Run LLM-based multi-turn loop against the existing workspace.
	output, err := l.executeWithLLM(ctx, task)
	if err != nil {
		task.Status = protocol.TaskStatusFailed
		task.Error = err.Error()
		return
	}
	task.Output = output

	l.pushCompletion(task)

	// Final budget check after task completion
	if l.budget != nil {
		l.checkAndCompress(ctx, task.ID)
	}
}

// executeWithLLM runs a multi-turn agent loop using the LLM client.
// It builds context from session history + raw sources + skills, calls the LLM,
// parses tool calls if present, executes them in the workspace, and loops back.
func (l *Loop) executeWithLLM(ctx context.Context, task *Task) ([]byte, error) {
	// Build conversation messages from session history + raw sources + skills
	messages, _, _ := l.buildMessages(task)

	// Resolve model/temperature/max_tokens — use workspace defaults.
	model := l.Model(task.Workspace)
	temp := l.Temperature(task.Workspace)
	maxTokens := l.MaxTokens(task.Workspace)

	// Validate provider early — if explicitly requested but doesn't exist, fail hard.
	if task.ProviderName != "" {
		pc, err := l.config.System.ResolveProvider(task.ProviderName)
		if err != nil {
			return nil, fmt.Errorf("provider %q not found: %w", task.ProviderName, err)
		}
		// Provider exists — apply its settings.
		model = pc.Model
		temp = pc.Temperature
		maxTokens = pc.MaxTokens
	}

	// Resolve the LLM client for this task's provider (cached).
	// Falls back to the loop's default llmClient if no specific provider is set.
	var provider llm.LLM
	if task.ProviderName != "" {
		p, err := l.getProvider(task.ProviderName)
		if err != nil {
			return nil, fmt.Errorf("resolve provider for task %s: %w", task.ID, err)
		}
		provider = p
	}
	// If no specific provider resolved (empty name), fall back to the default client.
	if provider == nil && l.llmClient != nil {
		provider = l.llmClient
	}

	// Multi-turn loop: max l.config.System.MaxTurns iterations to prevent infinite loops.
	// task.Output accumulates both text responses and tool call results.
	var fullOutput strings.Builder
	for turn := 0; turn < l.config.System.MaxTurns; turn++ {
		l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			TaskId:  &task.ID,
			Message: strPtr(fmt.Sprintf("LLM turn %d/%d", turn+1, l.config.System.MaxTurns)),
			Step:    intPtr(6 + turn),
		})

		// Build tools from active skills and pass to LLM.
		tools := l.buildTools()

		// Debug: log what's being sent to the LLM.
		if l.debug {
			var logProvider string
			if provider != nil {
				logProvider = provider.ProviderInfo().Name
			}
			log.Printf("agent: DEBUG - sending %d messages to model (provider=%s, model=%s)", len(messages), logProvider, model)
			for i, m := range messages {
				preview := m.Content
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				log.Printf("agent: DEBUG - msg[%d] role=%q content=%q", i, m.Role, preview)
			}
		}

		// Use streaming: yield chunks in real-time via WebSocket, accumulate full text.
		stream, err := provider.Stream(ctx, llm.CompletionRequest{
			Messages:    messages,
			Tools:       tools,
			Model:       model,
			Temperature: temp,
			MaxTokens:   maxTokens,
		})
		if err != nil {
			log.Printf("agent: LLM stream failed at turn %d: %v", turn, err)
			task.Status = protocol.TaskStatusFailed
			task.Error = fmt.Sprintf("LLM API error: %v", err)
			return nil, fmt.Errorf("LLM API error at turn %d: %v", turn, err)
		}

		var fullText strings.Builder
		var usage llm.Usage
		done := false

		// Accumulate tool calls captured during streaming.
		// Arguments stream incrementally across chunks; we group by ID
		// and concatenate so the final JSON object is complete.
		callsByID := make(map[string]*llm.ToolCallAccum)
		var currentID string // persistent across chunks (not reset per iteration)

		for !done {
			chunk, err := stream.Next()
			if err != nil {
				log.Printf("agent: stream read error at turn %d: %v", turn, err)
				task.Status = protocol.TaskStatusFailed
				task.Error = fmt.Sprintf("streaming failed at turn %d: %v", turn, err)
				// Granular event: turn.failed (stored + pushed via scope=events WebSocket clients)
				l.emitStored(task.ID, protocol.WebSocketEvent{
					Event:     protocol.TurnFailed,
					TaskId:    &task.ID,
					SessionId: &task.SessionId,
					Message:   strPtr(fmt.Sprintf("stream error at turn %d: %v", turn, err)),
				})
				_ = stream.Close()
				return nil, fmt.Errorf("streaming failed at turn %d: %v", turn, err)
			}
			// Only stop reading at the [DONE] sentinel.
			// finish_reason="tool_calls"/"stop" signals the model is done generating,
			// but arguments stream incrementally as delta content — we must keep
			// consuming chunks until [DONE] to capture all argument fragments.
			if chunk.FinishReason == "[done]" {
				done = true
				continue
			}

			fullText.WriteString(chunk.Delta)

			// Accumulate tool calls captured from delta.tool_calls field.
			// Arguments stream incrementally across chunks; group by ID and concatenate.
			if len(chunk.ToolCalls) > 0 {
				for _, tc := range chunk.ToolCalls {
					if tc.ID != "" {
						currentID = tc.ID
						if _, ok := callsByID[currentID]; !ok {
							callsByID[currentID] = &llm.ToolCallAccum{Name: tc.Name}
						}
					}
					// Append args from ALL chunks (including continuation chunks with no ID)
					if len(tc.Arguments) > 0 && currentID != "" {
						callsByID[currentID].Args = append(callsByID[currentID].Args, tc.Arguments...)
					}
				}
			}

			// Track token usage if available
			if chunk.Usage != nil {
				usage.PromptTokens += chunk.Usage.PromptTokens
				usage.CompletionTokens += chunk.Usage.CompletionTokens
				usage.TotalTokens += chunk.Usage.TotalTokens
			}

			// Broadcast chunk to client in real-time (lower perceived latency).
			// Only non-empty deltas are sent; empty-string chunks come from
			// tool-call / role-assignment events that carry no content.
			if chunk.Delta != "" {
				chunkMsg := chunk.Delta
				l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
					Event:   protocol.TaskProgress,
					TaskId:  &task.ID,
					Message: &chunkMsg,
					Step:    intPtr(6 + turn),
				})
			}
		}

		_ = stream.Close()

		responseText := fullText.String()
		l.logf("accumulated response text: %q", responseText)

		// Track token usage for budget
		if l.budget != nil {
			l.budget.TokenCount += usage.TotalTokens
			log.Printf("agent: turn %d used %d tokens (total session: %d)", turn+1, usage.TotalTokens, l.budget.TokenCount)
		}

		// Build final tool calls by concatenating streamed arguments per ID.
		var toolCalls []llm.ToolCall
		for id, acc := range callsByID {
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:        id,
				Name:      acc.Name,
				Arguments: json.RawMessage(acc.Args),
			})
			l.logf("accumulated tool call args (id=%s): %q", id, string(acc.Args))
		}

		// Validate accumulated args; discard incomplete ones and fall back to text parsing.
		validCalls := []llm.ToolCall{}
		for _, tc := range toolCalls {
			var m map[string]interface{}
			if err := json.Unmarshal(tc.Arguments, &m); err != nil {
				log.Printf("agent: discarding incomplete tool call (id=%s): %v", tc.ID, err)
				continue // invalid JSON — fall back to text parsing below
			}
			validCalls = append(validCalls, tc)
		}
		if len(validCalls) == 0 {
			// If we accumulated tool calls but all failed validation, fail the task
			// instead of silently completing with empty output.
			if len(toolCalls) > 0 {
				task.Status = protocol.TaskStatusFailed
				task.Error = fmt.Sprintf("failed to parse %d tool call(s): arguments incomplete or invalid", len(toolCalls))
				l.pushCompletion(task)
				return task.Output, nil
			}
			toolCalls = llm.ParseToolCalls(llm.Message{Role: "assistant", Content: responseText})
		} else {
			toolCalls = validCalls
		}

		if len(toolCalls) > 0 {
			// Execute tool calls in the workspace (item #6: enhanced WS events)
			for _, tc := range toolCalls {
				// Pre-execution broadcast with tool detail
				l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
					Event:   protocol.TaskProgress,
					TaskId:  &task.ID,
					Message: strPtr(fmt.Sprintf("Executing tool: %s (args: %.60s...)", tc.Name, string(tc.Arguments))),
					Step:    intPtr(7 + turn),
				})

				// Granular event: item.started (stored + pushed via scope=events WebSocket clients)
				l.emitStored(task.ID, protocol.WebSocketEvent{
					Event:     protocol.ItemStarted,
					TaskId:    &task.ID,
					SessionId: &task.SessionId,
					Message:   strPtr(fmt.Sprintf("tool:%s", tc.Name)),
				})

				output, exitCode, execErr := l.dispatchToolCall(ctx, task.Workspace, tc.Name, string(tc.Arguments))
				if execErr != nil {
					log.Printf("agent: tool %s failed (exit %d): %v", tc.Name, exitCode, execErr)
				} else if exitCode != 0 {
					log.Printf("agent: tool %s returned non-zero exit code: %d", tc.Name, exitCode)
				}

				// Post-execution broadcast with result status (item #6)
				status := "success"
				if execErr != nil || exitCode != 0 {
					status = "failed"
				}
				l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
					Event:   protocol.TaskProgress,
					TaskId:  &task.ID,
					Message: strPtr(fmt.Sprintf("Tool '%s' completed: %s (exit %d)", tc.Name, status, exitCode)),
					Step:    intPtr(7 + turn),
				})

				// Granular event: item.completed (stored + pushed via scope=events WebSocket clients)
				l.emitStored(task.ID, protocol.WebSocketEvent{
					Event:     protocol.ItemCompleted,
					TaskId:    &task.ID,
					SessionId: &task.SessionId,
					Message:   strPtr(fmt.Sprintf("tool:%s status:%s", tc.Name, status)),
				})

				// Persist tool call result for replay/debugging (item #5 companion)
				if err := l.store.PersistToolCallResult(task.ID, tc.Name, string(tc.Arguments), output, exitCode); err != nil {
					log.Printf("agent: failed to persist tool call result for %s: %v", tc.Name, err)
				}

				// Add tool result as a message in the conversation
				messages = append(messages, llm.Message{
					Role:    "tool",
					Name:    tc.Name,
					ID:      tc.ID,
					Content: output,
				})
			}

			// Accumulate assistant text in task output
			fullOutput.WriteString(responseText)

			// Add assistant response to messages for next turn
			messages = append(messages, llm.Message{
				Role:    "assistant",
				Content: responseText,
			})

			// Persist the assistant turn (item #5)
			turnMap := map[string]interface{}{
				"role":    "assistant",
				"content": responseText,
			}
			if err := l.store.PersistTurn(task.ID, turnMap); err != nil {
				log.Printf("agent: failed to persist turn %d: %v", turn+1, err)
			}

			// Keepalive heartbeat between turns (item #7) — prevents idle-timeout deletion
			l.Ws.UpdateActivity(ctx, task.Workspace)

			continue // Loop back for another turn
		}

		// No tool calls — accumulate final text and return full output
		fullOutput.WriteString(responseText)
		task.Output = []byte(fullOutput.String())
		l.logf("setting task.Output to: %q", string(task.Output))
		task.Status = protocol.TaskStatusCompleted

		// Populate RichOutput metadata from accumulated output.
		if richOut := l.detectRichOutput(task.Output); richOut != nil {
			task.RichOutput = richOut
		}

		// Persist the final assistant turn (item #5)
		turnMap := map[string]interface{}{
			"role":    "assistant",
			"content": string(task.Output),
		}
		if err := l.store.PersistTurn(task.ID, turnMap); err != nil {
			log.Printf("agent: failed to persist final turn for task %s: %v", task.ID, err)
		}

		// Granular event: turn.completed (stored + pushed via scope=events WebSocket clients)
		l.emitStored(task.ID, protocol.WebSocketEvent{
			Event:     protocol.TurnCompleted,
			TaskId:    &task.ID,
			SessionId: &task.SessionId,
			Message:   strPtr(fmt.Sprintf("turn %d/%d completed with %d bytes output", turn+1, l.config.System.MaxTurns, len(task.Output))),
		})

		return task.Output, nil
	}

	// Max turns reached without a clean completion
	if len(task.Output) == 0 {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("reached max turns (%d) without completing", l.config.System.MaxTurns)
		// Granular event: turn.failed (stored + pushed via scope=events WebSocket clients)
		l.emitStored(task.ID, protocol.WebSocketEvent{
			Event:     protocol.TurnFailed,
			TaskId:    &task.ID,
			SessionId: &task.SessionId,
			Message:   strPtr(fmt.Sprintf("max turns (%d) reached without completion", l.config.System.MaxTurns)),
		})
	}
	return task.Output, nil
}

// GetTaskEvents returns the granular event log for a task (used by ?type=events REST).
func (l *Loop) GetTaskEvents(taskID string) []protocol.WebSocketEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	task, ok := l.tasks[taskID]
	if !ok {
		return nil
	}
	return task.events
}

// buildTools converts active skills into llm.Tool definitions for the LLM API.
// Generic in-workspace tools (bash, read_file, write_file) and host-interaction
// tools (ask_user, notify) are always included; activated skills are appended.
// emitStored records a granular event in the task's event log and broadcasts to scope=events clients.
func (l *Loop) emitStored(taskID string, evt protocol.WebSocketEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if task, ok := l.tasks[taskID]; ok {
		task.events = append(task.events, evt)
	}
	l.wsHub.BroadcastGranular(evt.Event, taskID, *evt.SessionId, *evt.Message)
}

// detectRichOutput analyzes accumulated task output and returns a RichOutput struct
// populated with content type, render hint, and structured data if parseable as JSON.
func (l *Loop) detectRichOutput(output []byte) *protocol.RichOutput {
	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) == 0 {
		return nil
	}

	richOut := &protocol.RichOutput{}

	// Detect content type and render hint.
	contentType, renderHint := detectContentType(trimmed)
	richOut.ContentType = strPtr(contentType)
	richOut.RenderHint = &renderHint

	// If output is valid JSON, populate StructuredData.
	var jsonMap map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &jsonMap); err == nil {
		richOut.StructuredData = &jsonMap
	}

	return richOut
}

// detectContentType returns a high-level content type and matching render hint from raw text.
func detectContentType(s string) (contentType string, renderHint protocol.RichOutputRenderHint) {
	trimmed := strings.TrimSpace(s)

	// Check for JSON first (most specific structured format).
	if isJSON(trimmed) {
		return "json", protocol.Json
	}

	// Check for HTML.
	if hasHTMLTags(trimmed) {
		return "html", protocol.Html
	}

	// Check for markdown (headers, code blocks, lists).
	if hasMarkdown(trimmed) {
		return "markdown", protocol.Markdown
	}

	// Default to text.
	return "text", protocol.Text
}

func isJSON(s string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(s), &js) == nil
}

func hasHTMLTags(s string) bool {
	return strings.Contains(s, "<") && strings.Contains(s, ">")
}

func hasMarkdown(s string) bool {
	// Detect common markdown patterns: headings (#), code blocks (```), lists (-/1.), bold (**).
	for _, marker := range []string{"\n# ", "\n## ", "```", "\n- ", "\n1. ", "**"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func (l *Loop) buildTools() []llm.Tool {
	var tools []llm.Tool

	// Always-available generic tools (in-workspace I/O and command execution).
	tools = append(tools, llm.Tool{
		Name:        "bash",
		Description: "Execute a shell command inside the workspace environment. Returns combined stdout/stderr output.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The shell command to execute (e.g., 'ls -la /tmp')",
				},
			},
			"required": []string{"command"},
		},
	})

	tools = append(tools, llm.Tool{
		Name:        "read_file",
		Description: "Read the contents of a file inside the workspace environment. Returns the file content as a string.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Absolute or relative path to the file to read (e.g., '/tmp/data.txt')",
				},
			},
			"required": []string{"path"},
		},
	})

	tools = append(tools, llm.Tool{
		Name:        "write_file",
		Description: "Write content to a file inside the workspace environment. Creates parent directories if needed.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Absolute or relative path to the file to write (e.g., '/tmp/output.txt')",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
	})

	// Read-only in-workspace filesystem tools.
	tools = append(tools, llm.Tool{
		Name:        "list_dir",
		Description: "List the contents of a directory inside the workspace environment. Returns a detailed listing (equivalent to 'ls -la').",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Absolute or relative path to the directory to list (e.g., '/tmp/data')",
				},
			},
			"required": []string{"path"},
		},
	})

	tools = append(tools, llm.Tool{
		Name:        "find_files",
		Description: "Search for files matching a pattern inside the workspace environment. Returns matched file paths.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "Glob or filename pattern to search for (e.g., '*.log', 'config.*')",
				},
			},
			"required": []string{"pattern"},
		},
	})

	// Always-available host-interaction tools (bidirectional user communication).
	if l.notifier != nil {
		tools = append(tools, llm.Tool{
			Name:        "ask_user",
			Description: "Ask the connected user a question and wait for their typed response. Returns the user's answer as a string.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"question": map[string]interface{}{
						"type":        "string",
						"description": "The question to ask the user (e.g., 'Should I proceed with deletion?')",
					},
				},
				"required": []string{"question"},
			},
		})

		tools = append(tools, llm.Tool{
			Name:        "notify",
			Description: "Send a notification to the connected user via WebSocket/bell/Slack. Returns immediately.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Notification title (e.g., 'Build complete')",
					},
					"body": map[string]interface{}{
						"type":        "string",
						"description": "Notification body text",
					},
				},
				"required": []string{"title", "body"},
			},
		})
	}

	// Permission guard tool for destructive actions.
	if l.notifier != nil {
		tools = append(tools, llm.Tool{
			Name:        "request_permission",
			Description: "Ask the connected user to approve or deny a potentially destructive action. Returns 'approved' or 'denied'.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Description of the action to be performed (e.g., 'Delete all files in /tmp/cache')",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Optional scope/context for the action (e.g., 'workspace', 'host')",
					},
				},
				"required": []string{"description"},
			},
		})
	}

	// Remote MCP tools (discoverable at runtime).
	if l.mcpClient != nil {
		tools = append(tools, llm.Tool{
			Name:        "mcp_list_tools",
			Description: "List available tools from the connected MCP server. Returns a list of tool definitions with their names and schemas.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"endpoint": map[string]interface{}{
						"type":        "string",
						"description": "Optional MCP endpoint URL to query (if nil, uses the default configured endpoint)",
					},
				},
			},
		})

		tools = append(tools, llm.Tool{
			Name:        "mcp_call",
			Description: "Invoke a tool on the connected MCP server by name with typed arguments. Returns the tool's result.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the MCP tool to invoke (e.g., 'search_files')",
					},
					"arguments": map[string]interface{}{
						"type":        "object",
						"description": "Typed arguments to pass to the tool (schema depends on the target tool)",
					},
				},
				"required": []string{"name"},
			},
		})
	}

	if l.activator == nil {
		return tools
	}

	for _, entry := range l.activator.Registry() {
		// Only include activated skills as available tools
		if !contains(l.activator.Active(), entry.Name) {
			continue
		}

		// If the skill defines typed tool schemas, use them; otherwise fall back to generic {args}.
		if len(entry.Tools) > 0 {
			for _, td := range entry.Tools {
				params := map[string]interface{}{
					"type": "object",
				}
				if len(td.Parameters) > 0 {
					params["properties"] = td.Parameters
				} else {
					params["properties"] = map[string]interface{}{
						"args": map[string]interface{}{
							"type":        "object",
							"description": "Tool-specific arguments",
						},
					}
				}
				if len(td.Parameters) > 0 {
					// If parameters define a "required" field, propagate it
					if req, ok := td.Parameters["required"]; ok {
						if r, ok := req.([]interface{}); ok {
							params["required"] = r
						}
					}
				}
				tools = append(tools, llm.Tool{
					Name:        td.Name,
					Description: td.Description,
					Parameters:  params,
				})
			}
		} else {
			// Backward compatible: generic {args} for skills without explicit tool defs.
			tools = append(tools, llm.Tool{
				Name:        entry.Name,
				Description: entry.Description,
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"args": map[string]interface{}{
							"type":        "object",
							"description": "Tool-specific arguments",
						},
					},
					"required": []string{"args"},
				},
			})
		}
	}

	return tools
}

// buildMessages assembles conversation messages from session history, raw sources, and skills.
func (l *Loop) buildMessages(task *Task) ([]llm.Message, int, int) {
	var messages []llm.Message

	// 0. System prompt (agent identity/facts — immutable at runtime)
	if l.systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: l.systemPrompt,
		})
	}

	// 1. Raw environment context (injected once)
	if l.rawSources != nil {
		sources, err := l.rawSources.ListAll()
		if err == nil && len(sources) > 0 {
			var ctxParts []string
			ctxParts = append(ctxParts, "## Environment Context")
			ctxParts = append(ctxParts, "")
			for _, s := range sources {
				ctxParts = append(ctxParts, fmt.Sprintf("### %s (%s)", s.Name, s.ID))
				ctxParts = append(ctxParts, fmt.Sprintf("_Created: %s | Category: %s_", s.CreatedAt.Format(time.RFC3339), s.Category))
				ctxParts = append(ctxParts, "")
				ctxParts = append(ctxParts, "```")
				ctxParts = append(ctxParts, s.Content)
				ctxParts = append(ctxParts, "```")
				ctxParts = append(ctxParts, "")
			}
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: strings.Join(ctxParts, "\n"),
			})
		}
	}

	// 2. Active skills context
	if l.activator != nil {
		activeSkills := l.activator.Active()
		if len(activeSkills) > 0 {
			var skillContexts []string
			for _, entry := range l.activator.Registry() {
				if contains(activeSkills, entry.Name) {
					skillContexts = append(skillContexts, fmt.Sprintf("### Skill: %s\n%s", entry.Name, entry.Description))
				}
			}
			if len(skillContexts) > 0 {
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: strings.Join(skillContexts, "\n\n"),
				})
			}
		}
	}

	// 2.5. Workspace identity bootstrap (grounding context)
	if l.workspaceDef != nil {
		var wsParts []string
		wsParts = append(wsParts, "[SESSION BOOTSTRAP]")
		wsParts = append(wsParts, "")
		wsParts = append(wsParts, "## Workspace Identity")
		wsParts = append(wsParts, "")
		wsParts = append(wsParts, "You are operating inside a **workspace** — a bounded project environment with a specific purpose.")
		wsParts = append(wsParts, "")
		wsParts = append(wsParts, "A workspace is defined by:")
		wsParts = append(wsParts, "- **Purpose**: "+l.workspaceDef.Purpose)
		if len(l.workspaceDef.Scope) > 0 {
			wsParts = append(wsParts, fmt.Sprintf("- **Scope**: %s", strings.Join(l.workspaceDef.Scope, ", ")))
		}
		if len(l.workspaceDef.Tags) > 0 {
			wsParts = append(wsParts, fmt.Sprintf("- **Tags**: %s", strings.Join(l.workspaceDef.Tags, ", ")))
		}
		wsParts = append(wsParts, "")
		wsParts = append(wsParts, "Reference this identity when creating wiki entries or evaluating decisions.")

		messages = append(messages, llm.Message{
			Role:    "user",
			Content: strings.Join(wsParts, "\n"),
		})
	}

	// 3. Session history (conversation turns)
	if task.SessionId != "" {
		turns, err := l.store.LoadSessionTurns(task.SessionId)
		if err == nil {
			for _, t := range turns {
				role, _ := t["role"].(string)
				content, _ := t["content"].(string)
				if role != "" && content != "" {
					messages = append(messages, llm.Message{
						Role:    role,
						Content: content,
					})
				}
			}
		}
	}

	// 4. Current user message (the task prompt)
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: task.Prompt,
	})

	return messages, budget.CountTokensFromTurns(nil), 0 // tokens estimated by caller if needed
}

// resolveSkills determines which skills should be activated for a given prompt.
func (l *Loop) resolveSkills(prompt string) []string {
	var result []string

	if l.activator == nil {
		return result
	}

	// Match on keywords from the prompt against skill names/descriptions
	keywords := extractKeywords(prompt)

	// For MVP: iterate over the full registry and match skills by keyword overlap
	for _, entry := range l.activator.Registry() {
		if skills.MatchSkill(entry, keywords) {
			result = append(result, entry.Name)
		}
	}

	// Workspace-aware activation: also match skills against workspace tags/purpose
	// This enables proactive skill activation grounded in the workspace's domain identity.
	if l.workspaceDef != nil && (len(l.workspaceDef.Tags) > 0 || len(l.workspaceDef.Scope) > 0) {
		wsCtx := &skills.WorkspaceContext{
			Name:    l.workspaceDef.Name,
			Purpose: l.workspaceDef.Purpose,
			Scope:   l.workspaceDef.Scope,
			Tags:    l.workspaceDef.Tags,
		}
		for _, entry := range l.activator.Registry() {
			if !contains(result, entry.Name) && skills.MatchSkillWithWorkspace(entry, keywords, wsCtx) {
				result = append(result, entry.Name)
			}
		}
	}

	return result
}

// checkAndCompress checks budget thresholds and triggers archival + compression if needed.
// It is idempotent: if already compressed, it skips re-compression but still logs.
func (l *Loop) checkAndCompress(ctx context.Context, taskID string) {
	if l.budget == nil {
		return
	}

	state := l.budget.CurrentState()
	if state == budget.BudgetNone {
		return
	}

	log.Printf("agent: budget threshold triggered [%s] for task %s (used %.0f%%)",
		state.String(), taskID, l.budget.UsedFraction()*100)

	// Idempotency: skip compression if already compressed; still log and notify.
	if l.budget.Compressed {
		log.Printf("agent: session already compressed for task %s — skipping re-compression", taskID[:8])
		l.pushThresholdNotification(taskID, state)
		return
	}

	// Archive full transcript before compression
	if err := l.store.ArchiveSession(taskID, turnsToInterface(l.budget.Turns)); err != nil {
		log.Printf("agent: failed to archive session %s: %v", taskID, err)
	}

	// Persist plan state before compression (preserves plan during truncation)
	if l.budget.PlanContent != "" {
		planID := l.budget.PlanID
		if planID == "" {
			planID = fmt.Sprintf("plan-%s", taskID[:8])
		}
		if err := l.store.SavePlan(planID, l.budget.PlanContent); err != nil {
			log.Printf("agent: failed to persist plan %s before compression: %v", planID, err)
		} else {
			log.Printf("agent: preserved plan %s for task %s (survives truncation)", planID, taskID[:8])
		}
	}

	// Broadcast WebSocket notification (session-aware)
	l.mu.Lock()
	task := l.tasks[taskID]
	sessionID := ""
	if task != nil {
		sessionID = task.SessionId
	}
	l.mu.Unlock()

	l.wsHub.BroadcastSession(sessionID, protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &taskID,
		Message: strPtr(fmt.Sprintf("Context budget at %.0f%% — compressing session", l.budget.UsedFraction()*100)),
	})

	// Push notification via notifier path (threshold event logging)
	l.pushThresholdNotification(taskID, state)

	// Run compression (aggressive if critical threshold hit)
	aggressive := state == budget.BudgetCritical
	compressor := budget.NewCompressor(nil, l.budget.Budget) // nil client = no-op in tests
	result, err := compressor.Summarize(l.budget, aggressive)
	if err != nil {
		log.Printf("agent: compression failed for task %s: %v", taskID, err)
	} else {
		log.Printf("agent: compressed %d turns → %d kept, saved ~%d tokens",
			result.CompressedCount, result.KeptCount, result.TokensSaved)
	}
}

// pushThresholdNotification sends a threshold event notification via the notifier path.
func (l *Loop) pushThresholdNotification(taskID string, state budget.BudgetState) {
	if l.notifier == nil {
		return
	}

	var severity notify.NotificationSeverity
	switch state {
	case budget.BudgetCritical:
		severity = notify.NotificationError
	case budget.BudgetWarn:
		severity = notify.NotificationWarn
	default:
		severity = notify.NotificationInfo
	}

	n := notify.Notification{
		TaskID: taskID,
		Title:  fmt.Sprintf("Budget threshold [%s]", state.String()),
		Body: fmt.Sprintf("Token usage at %.0f%% of budget (%d/%d tokens). Threshold triggered.",
			l.budget.UsedFraction()*100, l.budget.TokenCount, l.budget.Budget.MaxTokens),
		Severity:  severity,
		Timestamp: time.Now(),
	}

	if err := l.notifier.Notify(n); err != nil {
		log.Printf("agent: notifier failed for threshold event task %s: %v", taskID[:8], err)
	}
}

// turnsToInterface converts budget.Turn slices to []interface{} for archival.
func turnsToInterface(turns []budget.Turn) []interface{} {
	result := make([]interface{}, len(turns))
	for i, t := range turns {
		result[i] = map[string]interface{}{
			"role":       t.Role,
			"content":    t.Content,
			"tokens":     t.Tokens,
			"pinned":     t.Pinned,
			"created_at": t.CreatedAt.Format(time.RFC3339),
		}
	}
	return result
}

// pushCompletion sends completion/failure events via WebSocket and notifies.
func (l *Loop) pushCompletion(task *Task) {
	l.mu.Lock()
	defer l.mu.Unlock()

	completionEvent := protocol.WebSocketEvent{
		Event:   protocol.TaskComplete,
		TaskId:  &task.ID,
		Message: strPtr(fmt.Sprintf("Task %s completed with status: %s", task.ID[:8], task.Status)),
	}

	if task.Error != "" {
		completionEvent.Event = protocol.TaskFailed
		completionEvent.Message = strPtr(task.Error)
	}

	l.wsHub.BroadcastSession(task.SessionId, completionEvent)

	// Push notification via notifier (if configured)
	if l.notifier != nil {
		var severity notify.NotificationSeverity
		if task.Error != "" {
			severity = notify.NotificationError
		} else if task.Status == protocol.TaskStatusCompleted {
			severity = notify.NotificationInfo
		}

		n := notify.Notification{
			TaskID: task.ID,
			Title:  fmt.Sprintf("Task %s", task.ID[:8]),
			Body: func() string {
				if task.Error != "" {
					return task.Error
				}
				trimmed := strings.TrimSpace(string(task.Output))
				if trimmed == "" {
					return "completed"
				}
				return trimmed
			}(),
			Severity:  severity,
			Timestamp: time.Now(),
		}

		if err := l.notifier.Notify(n); err != nil {
			log.Printf("agent: notifier failed for task %s: %v", task.ID, err)
		}
	}

	// Also drain scheduler results channel (non-blocking) to push notifications
	if l.resultsCh != nil {
		select {
		case result := <-l.resultsCh:
			if result.Err != nil {
				n := notify.Notification{
					TaskID:    result.TaskID,
					Title:     fmt.Sprintf("Task %s", result.TaskID[:8]),
					Body:      result.Err.Error(),
					Severity:  notify.NotificationWarn,
					Timestamp: time.Now(),
				}
				if l.notifier != nil {
					_ = l.notifier.Notify(n)
				}
			} else if result.Output != "" {
				n := notify.Notification{
					TaskID:    result.TaskID,
					Title:     fmt.Sprintf("Task %s", result.TaskID[:8]),
					Body:      result.Output,
					Severity:  notify.NotificationInfo,
					Timestamp: time.Now(),
				}
				if l.notifier != nil {
					_ = l.notifier.Notify(n)
				}
			}
		default:
			// No pending results — non-blocking drain complete
		}
	}
}

// extractKeywords breaks a prompt into candidate keywords for skill matching.
func extractKeywords(prompt string) []string {
	words := strings.Fields(strings.ToLower(prompt))
	var keywords []string
	for _, w := range words {
		if len(w) > 2 { // skip very short tokens
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// taskToMap converts a Task to a map for persistence.
func taskToMap(t *Task) map[string]interface{} {
	m := map[string]interface{}{
		"id":          t.ID,
		"prompt":      t.Prompt,
		"status":      string(t.Status),
		"workspace":   t.Workspace,
		"session_id":  t.SessionId,
		"output":      string(t.Output),
		"error":       t.Error,
		"retry_count": t.RetryCount,
		"created_at":  t.CreatedAt.Format(time.RFC3339Nano),
	}
	if t.Status == protocol.TaskStatusCompleted {
		m["completed_at"] = time.Now().Format(time.RFC3339)
	}
	return m
}

// SavePlan persists a plan to the store and updates the in-memory budget tracker.
func (l *Loop) SavePlan(planID string, content string) error {
	if err := l.store.SavePlan(planID, content); err != nil {
		return fmt.Errorf("save plan: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.budget != nil {
		l.budget.PlanID = planID
		l.budget.PlanContent = content
	}

	return nil
}

// LoadPlan loads a plan from the store and updates the in-memory budget tracker.
func (l *Loop) LoadPlan(planID string) (string, error) {
	content, err := l.store.LoadPlan(planID)
	if err != nil {
		return "", fmt.Errorf("load plan: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.budget != nil {
		l.budget.PlanID = planID
		l.budget.PlanContent = content
	}

	return content, nil
}

// DeletePlan removes a plan from the store and clears the in-memory tracker.
func (l *Loop) DeletePlan(planID string) error {
	if err := l.store.DeletePlan(planID); err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.budget != nil && l.budget.PlanID == planID {
		l.budget.PlanID = ""
		l.budget.PlanContent = ""
	}

	return nil
}

// AddDiscoveryPaths appends new discovery paths to the config's skill discovery list.
// This is the server-driven mechanism for expanding where skills are scanned at runtime.
// Returns an error if any path is empty or invalid.
func (l *Loop) AddDiscoveryPaths(paths []string) error {
	if l.config == nil || l.config.System == nil {
		return fmt.Errorf("config not available")
	}

	if len(paths) == 0 {
		return fmt.Errorf("paths array must not be empty")
	}

	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			return fmt.Errorf("empty discovery path in input")
		}
	}

	l.config.System.Skills.DiscoveryPaths = append(l.config.System.Skills.DiscoveryPaths, paths...)
	return nil
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// contains checks if a string slice contains a value.
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// dispatchToolCall routes a parsed tool call to the appropriate handler.
// Generic tools (bash, read_file, write_file) and host-interaction tools
// (ask_user, notify) are handled with typed execution; unknown tool names
// fall back to raw RunCommand (for activated skill tools).
func (l *Loop) dispatchToolCall(ctx context.Context, workspaceName string, name string, argsRaw string) (string, int, error) {
	switch name {
	case "bash":
		return l.handleBash(ctx, workspaceName, argsRaw)
	case "read_file":
		return l.handleReadFile(ctx, workspaceName, argsRaw)
	case "write_file":
		return l.handleWriteFile(ctx, workspaceName, argsRaw)
	case "list_dir":
		return l.handleListDir(workspaceName, argsRaw)
	case "find_files":
		return l.handleFindFiles(workspaceName, argsRaw)
	case "ask_user":
		return l.handleAskUser(workspaceName, argsRaw)
	case "notify":
		return l.handleNotify(argsRaw)
	case "request_permission":
		return l.handleRequestPermission(workspaceName, argsRaw)
	case "file_diff":
		return l.handleFileDiff(ctx, workspaceName, argsRaw)
	case "mcp_list_tools":
		return l.handleMCPListTools(workspaceName, argsRaw)
	case "mcp_call":
		return l.handleMCPCall(workspaceName, argsRaw)
	default:
		// Fall back to raw RunCommand for activated skill tools
		output, exitCode, err := l.Ws.RunCommand(ctx, workspaceName, argsRaw)
		return output, exitCode, err
	}
}

// handleBash executes a shell command inside the workspace.
func (l *Loop) handleBash(ctx context.Context, workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse bash args: %w", err)
	}

	cmd, ok := args["command"].(string)
	if !ok || cmd == "" {
		return "", 0, fmt.Errorf("bash requires a 'command' field (string)")
	}

	output, exitCode, err := l.Ws.RunCommand(ctx, workspaceName, cmd)
	return output, exitCode, err
}

// handleReadFile reads a file inside the workspace and returns its content.
func (l *Loop) handleReadFile(ctx context.Context, workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse read_file args: %w", err)
	}

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", 0, fmt.Errorf("read_file requires a 'path' field (string)")
	}

	output, exitCode, err := l.Ws.RunCommand(ctx, workspaceName, "cat "+shellEscape(path))
	return output, exitCode, err
}

// handleWriteFile writes content to a file inside the workspace.
func (l *Loop) handleWriteFile(ctx context.Context, workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse write_file args: %w", err)
	}

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", 0, fmt.Errorf("write_file requires a 'path' field (string)")
	}

	content, ok := args["content"].(string)
	if !ok {
		return "", 0, fmt.Errorf("write_file requires a 'content' field (string)")
	}

	cmd := "printf '%s' '" + shellEscape(content) + "' > " + shellEscape(path)
	output, exitCode, err := l.Ws.RunCommand(ctx, workspaceName, cmd)
	return output, exitCode, err
}

// handleAskUser asks the connected user a question and waits for their response.
// Returns the user's answer as a string (or an error if no response within timeout).
func (l *Loop) handleAskUser(workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse ask_user args: %w", err)
	}

	question, ok := args["question"].(string)
	if !ok || question == "" {
		return "", 0, fmt.Errorf("ask_user requires a 'question' field (string)")
	}

	if l.notifier == nil {
		return "", 0, fmt.Errorf("ask_user: notifier not configured")
	}

	// Use the blocking variant that waits for a user response via WebSocket.
	resp, err := l.notifier.AskUserBlocking(question)
	if err != nil {
		return "", 0, err
	}
	return resp, 0, nil
}

// handleNotify sends a notification to the connected user via all channels.
func (l *Loop) handleNotify(argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse notify args: %w", err)
	}

	title, ok := args["title"].(string)
	if !ok || title == "" {
		return "", 0, fmt.Errorf("notify requires a 'title' field (string)")
	}

	body, ok := args["body"].(string)
	if !ok {
		return "", 0, fmt.Errorf("notify requires a 'body' field (string)")
	}

	if l.notifier == nil {
		return "", 0, fmt.Errorf("notify: notifier not configured")
	}

	if err := l.notifier.Push(title, body); err != nil {
		return "", 0, err
	}

	return fmt.Sprintf("Notification sent: %s", title), 0, nil
}

// handleRequestPermission asks the user to approve or deny a destructive action.
// Returns "approved" or "denied" as a string response.
func (l *Loop) handleRequestPermission(workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse request_permission args: %w", err)
	}

	description, ok := args["description"].(string)
	if !ok || description == "" {
		return "", 0, fmt.Errorf("request_permission requires a 'description' field (string)")
	}

	scope, _ := args["scope"].(string) // optional

	if l.notifier == nil {
		return "", 0, fmt.Errorf("request_permission: notifier not configured")
	}

	// Use AskUserBlocking to wait for the user's approval decision.
	resp, err := l.notifier.AskUserBlocking(fmt.Sprintf(
		"Approve or deny this action?\nAction: %s\nScope: %s", description, scope))
	if err != nil {
		return "", 0, err
	}

	// Normalize the response to "approved" or "denied".
	resp = strings.ToLower(strings.TrimSpace(resp))
	switch resp {
	case "approve", "approved", "yes", "y":
		return fmt.Sprintf("Permission granted for: %s", description), 0, nil
	case "deny", "denied", "no", "n":
		return fmt.Sprintf("Permission denied for: %s", description), 0, nil
	default:
		return fmt.Sprintf("Unclear response '%s' — treating as denied", resp), 0, nil
	}
}

// handleFileDiff reads a file and returns its current content for comparison.
func (l *Loop) handleFileDiff(ctx context.Context, workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse file_diff args: %w", err)
	}

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", 0, fmt.Errorf("file_diff requires a 'path' field (string)")
	}

	// Read the file via cat command in the workspace.
	output, exitCode, err := l.Ws.RunCommand(ctx, workspaceName, "cat "+shellEscape(path))
	if err != nil {
		return "", exitCode, fmt.Errorf("file_diff read error: %w", err)
	}

	return fmt.Sprintf("# File: %s\n%s", path, output), 0, nil
}

// handleListDir lists the contents of a directory inside the workspace.
func (l *Loop) handleListDir(workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse list_dir args: %w", err)
	}

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", 0, fmt.Errorf("list_dir requires a 'path' field (string)")
	}

	cmd := "ls -la " + shellEscape(path)
	output, exitCode, err := l.Ws.RunCommand(context.Background(), workspaceName, cmd)
	return output, exitCode, err
}

// handleFindFiles searches for files matching a glob pattern inside the workspace.
func (l *Loop) handleFindFiles(workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse find_files args: %w", err)
	}

	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return "", 0, fmt.Errorf("find_files requires a 'pattern' field (string)")
	}

	cmd := "find . -name '" + pattern + "'"
	output, exitCode, err := l.Ws.RunCommand(context.Background(), workspaceName, cmd)
	return output, exitCode, err
}

// handleMCPListTools lists available tools from the connected MCP server.
func (l *Loop) handleMCPListTools(workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse mcp_list_tools args: %w", err)
	}

	if l.mcpClient == nil {
		return "", 0, fmt.Errorf("mcp_list_tools: MCP client not configured")
	}

	// Optional endpoint override (for multi-endpoint scenarios).
	// Currently unused in the MVP; kept for schema completeness.
	_ = args["endpoint"] // endpoint override (unused in MVP)

	tools, err := l.mcpClient.ListTools(context.Background())
	if err != nil {
		return "", 0, fmt.Errorf("mcp_list_tools: %w", err)
	}

	// Format as JSON for structured return.
	result, _ := json.Marshal(tools)
	return string(result), 0, nil
}

// handleMCPCall invokes a tool on the connected MCP server by name with typed arguments.
func (l *Loop) handleMCPCall(workspaceName string, argsRaw string) (string, int, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return "", 0, fmt.Errorf("parse mcp_call args: %w", err)
	}

	name, ok := args["name"].(string)
	if !ok || name == "" {
		return "", 0, fmt.Errorf("mcp_call requires a 'name' field (string)")
	}

	if l.mcpClient == nil {
		return "", 0, fmt.Errorf("mcp_call: MCP client not configured")
	}

	// Extract arguments (optional — some tools take no args).
	var toolArgs map[string]any
	if rawArgs, ok := args["arguments"].(map[string]interface{}); ok {
		toolArgs = make(map[string]any, len(rawArgs))
		for k, v := range rawArgs {
			toolArgs[k] = v
		}
	}

	result, err := l.mcpClient.CallTool(context.Background(), name, toolArgs)
	if err != nil {
		return "", 0, fmt.Errorf("mcp_call %q: %w", name, err)
	}

	// Format result as JSON.
	output, _ := json.Marshal(result)
	return string(output), 0, nil
}

// MarkActive signals that a workspace is currently in use (updates last activity).
func (l *Loop) MarkActive(workspace string) {
	l.mu.Lock()
	l.processing = true
	l.mu.Unlock()
	if l.workspaceDef != nil && l.workspaceDef.Name == workspace {
		log.Printf("[loop] Workspace '%s' marked active", workspace)
	}
}

// MarkIdle signals that a workspace has no active sessions (start idle timer).
func (l *Loop) MarkIdle(workspace string) {
	l.mu.Lock()
	l.processing = false
	l.mu.Unlock()
	if l.workspaceDef != nil && l.workspaceDef.Name == workspace {
		log.Printf("[loop] Workspace '%s' marked idle", workspace)
	}
}

// IsProcessing returns whether the loop is currently processing a task or session.
// Useful for graceful shutdown: callers can check this before blocking on long operations.
func (l *Loop) IsProcessing() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.processing
}

// shellEscape wraps a string for safe inclusion in a single-quoted shell argument.
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}
