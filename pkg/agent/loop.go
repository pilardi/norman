// Package agent implements the core agent loop that resolves skills,
// executes tasks via OpenShell sandboxes, and reports progress.
package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/google/uuid"
	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/llm"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
)

// Task represents an in-flight task (emergent or manual) with its execution state.
type Task struct {
	ID        string
	Prompt    string
	Status    protocol.TaskStatus
	Workspace string
	SessionId string
	Output    []byte
	Error     string
	RetryCount int
	CreatedAt time.Time
}

// Loop is the main agent loop managing session messages and emergent/manual task execution.
type Loop struct {
	mu          sync.Mutex
	tasks       map[string]*Task // taskID -> Task state
	store       *memory.Store    // persistent state
	rawSources  *memory.RawSources // raw environment snapshots for context injection
	activator   *skills.Activator
	config      *config.Config
	openShell   OpenShellClient
	llmClient   llm.LLM           // LLM API client (optional, nil = shell-only mode)
	wsHub       WSHubPublisher
	notifier    *notify.Notifier
	resultsCh   chan scheduler.TaskResult // results from scheduler (nil if not wired)
	maxRetries  int
	budget      *budget.SessionState // token budget tracker (nil if disabled)
	workspaceDef *env.WorkspaceDefinition // workspace identity for system prompt injection
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

// OpenShellClient abstracts the OpenShell SDK for testing.
type OpenShellClient interface {
	CreateSandbox(ctx context.Context, workspace, name string) (string, error)
	WaitReady(ctx context.Context, workspace, name string, timeout time.Duration) (*v1.Sandbox, error)
	GetSandbox(ctx context.Context, provider, name string) (*v1.Sandbox, error)
	RunCommand(ctx context.Context, sandboxName string, command string) (string, int, error)
	GetLogs(ctx context.Context, sandboxID string) ([]string, error)
	DeleteSandbox(ctx context.Context, workspace, name string) error
	ListSandboxes(ctx context.Context, workspace string) ([]string, error)
	KeepAlive(ctx context.Context, sandboxName string) error
}

// WSHubPublisher sends WebSocket push events.
type WSHubPublisher interface {
	BroadcastEvent(event protocol.WebSocketEvent)
	BroadcastSession(sessionID string, event protocol.WebSocketEvent)
}

// New creates a new agent loop with the given dependencies.
func New(cfg *config.Config, store *memory.Store, rawSources *memory.RawSources, activator *skills.Activator, wsHub WSHubPublisher, client OpenShellClient) *Loop {
	return &Loop{
		tasks:      make(map[string]*Task),
		store:      store,
		rawSources: rawSources,
		activator:  activator,
		config:     cfg,
		openShell:  client,
		wsHub:      wsHub,
		maxRetries: 3,
		budget:     nil, // disabled by default; set via SetBudget()
	}
}

// WithNotifier injects a notification notifier into the loop. Returns the modified Loop.
func (l *Loop) WithNotifier(n *notify.Notifier) *Loop {
	l.notifier = n
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

// Submit enqueues a task and begins execution.
func (l *Loop) Submit(ctx context.Context, prompt string, workspaceID string, sessionID string) (*Task, error) {
	taskID := uuid.New().String()

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

// GetLogs returns the log lines for a sandbox/session by ID.
func (l *Loop) GetLogs(ctx context.Context, sandboxID string) ([]string, error) {
	return l.openShell.GetLogs(ctx, sandboxID)
}

// AgentResponse represents the structured response from processing a chat message.
type AgentResponse struct {
	SessionID   string   `json:"session_id"`
	Response    string   `json:"response,omitempty"`
	ToolsUsed   []string `json:"tools_used,omitempty"`
	TaskCreated bool     `json:"task_created"`
	TaskID      string   `json:"task_id,omitempty"`
}

// SendMessage processes a chat message synchronously and returns a structured response.
// This is the primary interface for the chat-first agent model.
func (l *Loop) SendMessage(ctx context.Context, sessionID string, message string) (*AgentResponse, error) {
	taskID := uuid.New().String()

	task := &Task{
		ID:        taskID,
		Prompt:    message,
		Status:    protocol.TaskStatusPending,
		SessionId: sessionID,
		CreatedAt: time.Now(),
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

// SendMessageWithTask processes a chat message and optionally creates an emergent task.
// If createTask is true, the agent will also invoke CreateTask internally to spawn
// a background task after processing the message. The response includes task_created
// and task_id when applicable.
func (l *Loop) SendMessageWithTask(ctx context.Context, sessionID string, message string, createTask bool) (*AgentResponse, error) {
	// First, process the message normally.
	resp, err := l.SendMessage(ctx, sessionID, message)
	if err != nil {
		return nil, err
	}

	// If task creation is requested, create an emergent task.
	if createTask && resp.Response != "" {
		task, err := l.CreateTask(ctx, message, map[string]string{
			"priority": "normal",
		})
		if err != nil {
			log.Printf("agent: failed to create emergent task: %v", err)
			return resp, nil // non-fatal; return response without task info
		}

		resp.TaskCreated = true
		resp.TaskID = task.ID
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
		"id":          taskID,
		"prompt":      prompt,
		"status":      string(task.Status),
		"workspace":   workspaceID,
		"priority":    string(priority),
		"created_at":  task.CreatedAt.Format(time.RFC3339),
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
			_, err := l.activator.Activate(name)
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

	// Phase 2: Sandbox creation (session-aware)
	l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Creating sandbox..."),
		Step:    intPtr(3),
	})

	sandboxName := fmt.Sprintf("norman-task-%s", task.ID[:7])
	_, err := l.openShell.CreateSandbox(ctx, task.Workspace, sandboxName)
	if err != nil {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("sandbox creation failed: %v", err)
		log.Printf("agent: sandbox creation failed: %v", err)
		l.pushCompletion(task)
		return
	}

	// Budget check after sandbox provisioning (post-provisioning re-check)
	if l.budget != nil {
		l.checkAndCompress(ctx, task.ID)
	}

	// Phase 3: Wait for ready (session-aware)
	l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Waiting for sandbox ready..."),
		Step:    intPtr(4),
	})

	// Wait for sandbox to be ready.
	var waitErr error
	_, waitErr = l.openShell.WaitReady(ctx, task.Workspace, sandboxName, 60*time.Second)
	if waitErr != nil {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("sandbox not ready: %v", waitErr)
		l.pushCompletion(task)
		return
	}

	// Final sanity check: re-fetch sandbox state right before execution.
	// This catches the case where the gateway auto-deletes sandboxes (idle timeout)
	// between WaitReady and RunCommand.
	var getErr error
	_, getErr = l.openShell.GetSandbox(ctx, "default", sandboxName)
	if getErr != nil {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("sandbox not found before execution: %v", getErr)
		l.pushCompletion(task)
		return
	}

	// Phase 4: Execute — multi-turn agent loop with LLM if available.
	l.mu.Lock()
	task.Status = protocol.TaskStatusRunning
	l.mu.Unlock()

	l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Executing task..."),
		Step:    intPtr(5),
	})

	// Try LLM-based multi-turn loop first; fall back to shell-only if no client.
	if l.llmClient != nil {
		task.Output = l.executeWithLLM(ctx, task, sandboxName)
	} else {
		// Legacy shell-only path: inject raw sources into prompt and run as a single command.
		prompt := task.Prompt
		if l.rawSources != nil {
			sources, err := l.rawSources.ListAll()
			if err != nil {
				log.Printf("agent: failed to read raw sources for task %s: %v", task.ID, err)
			} else if len(sources) > 0 {
				var ctxParts []string
				ctxParts = append(ctxParts, "## Raw Environment Context")
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
				prompt = strings.Join(ctxParts, "\n") + "\n\n---\n\n" + task.Prompt
			}
		}

		output, exitCode, err := l.openShell.RunCommand(ctx, sandboxName, prompt)
		if err != nil {
			task.Status = protocol.TaskStatusFailed
			task.Error = fmt.Sprintf("execution failed (exit %d): %v", exitCode, err)
		} else if exitCode != 0 {
			task.Status = protocol.TaskStatusFailed
			task.Error = fmt.Sprintf("non-zero exit code: %d", exitCode)
		} else {
			task.Output = []byte(output)
			task.Status = protocol.TaskStatusCompleted

			// Persist the assistant turn for shell-only path too (item #5 companion)
			turnMap := map[string]interface{}{
				"role":    "assistant",
				"content": string(task.Output),
			}
			if err := l.store.PersistTurn(task.ID, turnMap); err != nil {
				log.Printf("agent: failed to persist shell-only turn for task %s: %v", task.ID, err)
			}

			// Also persist the tool call result (the RunCommand itself is a "tool" call)
			if err := l.store.PersistToolCallResult(task.ID, "shell_command", prompt, string(output), exitCode); err != nil {
				log.Printf("agent: failed to persist shell command result for task %s: %v", task.ID, err)
			}
		}
	}

	l.pushCompletion(task)

	// Final budget check after task completion
	if l.budget != nil {
		l.checkAndCompress(ctx, task.ID)
	}

	// Cleanup sandbox
	go func() {
		if delErr := l.openShell.DeleteSandbox(context.Background(), task.Workspace, sandboxName); delErr != nil {
			log.Printf("agent: failed to delete sandbox %s: %v", sandboxName, delErr)
		}
	}()
}

// executeWithLLM runs a multi-turn agent loop using the LLM client.
// It builds context from session history + raw sources + skills, calls the LLM,
// parses tool calls if present, executes them in the sandbox, and loops back.
func (l *Loop) executeWithLLM(ctx context.Context, task *Task, sandboxName string) []byte {
	// Build conversation messages from session history + raw sources + skills
	messages, _, _ := l.buildMessages(task)

	// Multi-turn loop: max 8 iterations to prevent infinite loops
	maxTurns := 8
	for turn := 0; turn < maxTurns; turn++ {
		l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			TaskId:  &task.ID,
			Message: strPtr(fmt.Sprintf("LLM turn %d/%d", turn+1, maxTurns)),
			Step:    intPtr(6 + turn),
		})

		// Build tools from active skills and pass to LLM
		tools := l.buildTools()

		// Use streaming: yield chunks in real-time via WebSocket, accumulate full text.
		stream, err := l.llmClient.Stream(ctx, llm.CompletionRequest{
			Messages:    messages,
			Tools:       tools,
			Model:       l.Model(task.Workspace),
			Temperature:   l.Temperature(task.Workspace),
			MaxTokens:   l.MaxTokens(task.Workspace),
		})
		if err != nil {
			log.Printf("agent: LLM stream failed at turn %d: %v", turn, err)
			task.Status = protocol.TaskStatusFailed
			task.Error = fmt.Sprintf("LLM API error: %v", err)
			return []byte(task.Error)
		}

		var fullText strings.Builder
		var usage llm.Usage
		done := false

		for !done {
			chunk, err := stream.Next()
			if err != nil {
				log.Printf("agent: stream read error at turn %d: %v", turn, err)
				task.Status = protocol.TaskStatusFailed
				task.Error = fmt.Sprintf("streaming failed at turn %d: %v", turn, err)
				stream.Close()
				return []byte(task.Error)
			}
			if chunk.FinishReason == "[done]" {
				done = true
				continue
			}

			fullText.WriteString(chunk.Delta)

			// Track token usage if available
			if chunk.Usage != nil {
				usage.PromptTokens += chunk.Usage.PromptTokens
				usage.CompletionTokens += chunk.Usage.CompletionTokens
				usage.TotalTokens += chunk.Usage.TotalTokens
			}

			// Broadcast chunk to client in real-time (lower perceived latency)
			chunkMsg := chunk.Delta
			l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
				Event:   protocol.TaskProgress,
				TaskId:  &task.ID,
				Message: &chunkMsg,
				Step:    intPtr(6 + turn),
			})
		}

		stream.Close()

		responseText := fullText.String()

		// Track token usage for budget
		if l.budget != nil {
			l.budget.TokenCount += usage.TotalTokens
			log.Printf("agent: turn %d used %d tokens (total session: %d)", turn+1, usage.TotalTokens, l.budget.TokenCount)
		}

		// Parse tool calls from the accumulated response text
		toolCalls := llm.ParseToolCalls(llm.Message{Role: "assistant", Content: responseText})

		if len(toolCalls) > 0 {
			// Execute tool calls in the sandbox (item #6: enhanced WS events)
			for _, tc := range toolCalls {
				// Pre-execution broadcast with tool detail
				l.wsHub.BroadcastSession(task.SessionId, protocol.WebSocketEvent{
					Event:   protocol.TaskProgress,
					TaskId:  &task.ID,
					Message: strPtr(fmt.Sprintf("Executing tool: %s (args: %.60s...)", tc.Name, string(tc.Arguments))),
					Step:    intPtr(7 + turn),
				})

				output, exitCode, execErr := l.openShell.RunCommand(ctx, sandboxName, string(tc.Arguments))
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
			if err := l.openShell.KeepAlive(ctx, sandboxName); err != nil {
				log.Printf("agent: keepalive failed for sandbox %s: %v", sandboxName, err)
			}

			continue // Loop back for another turn
		}

		// No tool calls — this is the final text response
		task.Output = []byte(responseText)
		task.Status = protocol.TaskStatusCompleted

		// Persist the final assistant turn (item #5)
		turnMap := map[string]interface{}{
			"role":    "assistant",
			"content": string(task.Output),
		}
		if err := l.store.PersistTurn(task.ID, turnMap); err != nil {
			log.Printf("agent: failed to persist final turn for task %s: %v", task.ID, err)
		}

		return task.Output
	}

	// Max turns reached without a clean completion
	if len(task.Output) == 0 {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("reached max turns (%d) without completing", maxTurns)
	}
	return task.Output
}

// buildSystemPrompt assembles the system prompt for the LLM.
func (l *Loop) buildSystemPrompt(workspaceID string) string {
	var parts []string

	// 1. Workspace identity (if defined) — injected first so agent grounds its reasoning
	if l.workspaceDef != nil {
		parts = append(parts, l.workspaceDef.AsSystemPrompt())
	}

	// 2. Base system instructions
	parts = append(parts, `You are Norman, a helpful AI assistant operating inside an OpenShell sandbox environment. You help users work on their projects by reading files, running commands, and reporting results.

Rules:
- Always respond in the user's language.
- Be concise but thorough. If you need more information, ask a clarifying question.
- When using tools (shell commands), execute them one at a time and wait for results before proceeding.
- If a tool fails, explain what went wrong and try an alternative approach.
- Never assume file contents without reading them first.
- When done with a task, provide a clear summary of what you accomplished.`)

	// 3. Add workspace-specific system prompt if configured (e.g., custom instructions)
	ws := l.WorkspaceConfig(workspaceID)
	if ws.SystemPrompt != "" {
		parts = append(parts, "\n\nWorkspace-specific instructions:\n"+ws.SystemPrompt)
	}

	return strings.Join(parts, "\n")
}

// buildTools converts active skills into llm.Tool definitions for the LLM API.
func (l *Loop) buildTools() []llm.Tool {
	var tools []llm.Tool

	if l.activator == nil {
		return tools
	}

	for _, entry := range l.activator.Registry() {
		// Only include activated skills as available tools
		if !contains(l.activator.Active(), entry.Name) {
			continue
		}

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

	return tools
}

// buildMessages assembles conversation messages from session history, raw sources, and skills.
func (l *Loop) buildMessages(task *Task) ([]llm.Message, int, int) {
	var messages []llm.Message

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
		wsParts = append(wsParts, fmt.Sprintf("You are operating inside a **workspace** — a bounded project environment with a specific purpose."))
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
		sessMap, err := l.store.LoadSession(task.SessionId)
		if err == nil {
			timestamps := sessMap["turns"]
			turns, ok := timestamps.([]interface{})
			if ok {
				for _, t := range turns {
					turnMap, ok := t.(map[string]interface{})
					if !ok {
						continue
					}
					role, _ := turnMap["role"].(string)
					content, _ := turnMap["content"].(string)
					if role != "" && content != "" {
						messages = append(messages, llm.Message{
							Role:    role,
							Content: content,
						})
					}
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

	flags := l.budget.CheckThresholds()
	if flags == budget.ThresholdNone {
		return
	}

	log.Printf("agent: budget threshold triggered [%s] for task %s (used %.0f%%)",
		flags.String(), taskID, l.budget.UsedFraction()*100)

	// Idempotency: skip compression if already compressed; still log and notify.
	if l.budget.Compressed {
		log.Printf("agent: session already compressed for task %s — skipping re-compression", taskID[:8])
		l.pushThresholdNotification(taskID, flags)
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
	l.pushThresholdNotification(taskID, flags)

	// Run compression (aggressive if critical threshold hit)
	aggressive := flags.Is(budget.ThresholdCritical)
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
func (l *Loop) pushThresholdNotification(taskID string, flags budget.ThresholdFlags) {
	if l.notifier == nil {
		return
	}

	severity := "info"
	switch {
	case flags.Is(budget.ThresholdCritical):
		severity = "error"
	case flags.Is(budget.ThresholdWarn):
		severity = "warn"
	}

	n := notify.Notification{
		TaskID:    taskID,
		Title:     fmt.Sprintf("Budget threshold [%s]", flags.String()),
		Body:      fmt.Sprintf("Token usage at %.0f%% of budget (%d/%d tokens). Threshold triggered.",
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
			"role":      t.Role,
			"content":   t.Content,
			"tokens":    t.Tokens,
			"pinned":    t.Pinned,
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
		severity := "info"
		if task.Error != "" {
			severity = "error"
		} else if task.Status == protocol.TaskStatusCompleted {
			severity = "info"
		}

		n := notify.Notification{
			TaskID:    task.ID,
			Title:     fmt.Sprintf("Task %s", task.ID[:8]),
			Body:      string(task.Output),
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
				severity := "warn"
				n := notify.Notification{
					TaskID:    result.TaskID,
					Title:     fmt.Sprintf("Task %s", result.TaskID[:8]),
					Body:      result.Err.Error(),
					Severity:  severity,
					Timestamp: time.Now(),
				}
				if l.notifier != nil {
					l.notifier.Notify(n)
				}
			} else if result.Output != "" {
				n := notify.Notification{
					TaskID:    result.TaskID,
					Title:     fmt.Sprintf("Task %s", result.TaskID[:8]),
					Body:      result.Output,
					Severity:  "info",
					Timestamp: time.Now(),
				}
				if l.notifier != nil {
					l.notifier.Notify(n)
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
		"output":      string(t.Output),
		"error":       t.Error,
		"retry_count": t.RetryCount,
		"created_at":  t.CreatedAt.Format(time.RFC3339),
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
func intPtr(i int) *int        { return &i }

// contains checks if a string slice contains a value.
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// --- Fake OpenShellClient for testing ---

// FakeOpenShellClient is a mock implementation of OpenShellClient.
type FakeOpenShellClient struct {
	mu             sync.Mutex
	sandboxes      map[string]string // workspace.name -> sandboxID
	commands       []string          // queued commands to simulate
	logLines       []string          // simulated log output
	capturedPrompt string            // last prompt sent to RunCommand (for test verification)
}

// NewFakeOpenShellClient creates a fake client for testing.
func NewFakeOpenShellClient() *FakeOpenShellClient {
	return &FakeOpenShellClient{
		sandboxes: make(map[string]string),
		logLines:  []string{"sandbox initialized", "ready"},
	}
}

// CapturedPrompt returns the last prompt sent to RunCommand.
func (f *FakeOpenShellClient) CapturedPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capturedPrompt
}

func (f *FakeOpenShellClient) CreateSandbox(ctx context.Context, workspace, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sid := fmt.Sprintf("sb-%s", strings.ReplaceAll(name, "-", ""))
	f.sandboxes[name] = sid
	return sid, nil
}

func (f *FakeOpenShellClient) WaitReady(ctx context.Context, workspace, name string, timeout time.Duration) (*v1.Sandbox, error) {
	// Simulate readiness after a short delay
	select {
	case <-time.After(100 * time.Millisecond):
		return &v1.Sandbox{ID: fmt.Sprintf("sb-%s", strings.ReplaceAll(name, "-", ""))}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *FakeOpenShellClient) GetSandbox(ctx context.Context, provider, name string) (*v1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sid, ok := f.sandboxes[name]
	if !ok {
		return nil, fmt.Errorf("sandbox %s not found", name)
	}
	return &v1.Sandbox{ID: sid}, nil
}

func (f *FakeOpenShellClient) RunCommand(ctx context.Context, sandboxName, command string) (string, int, error) {
	f.mu.Lock()
	f.commands = append(f.commands, command)
	f.capturedPrompt = command
	f.mu.Unlock()

	// Simulate output
	output := fmt.Sprintf("Executed: %s\nOutput from sandbox %s", command, sandboxName)
	return output, 0, nil
}

func (f *FakeOpenShellClient) GetLogs(ctx context.Context, sandboxID string) ([]string, error) {
	return f.logLines, nil
}

func (f *FakeOpenShellClient) DeleteSandbox(ctx context.Context, workspace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sandboxes, workspace+"."+name)
	return nil
}

func (f *FakeOpenShellClient) ListSandboxes(ctx context.Context, workspace string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []string
	for key := range f.sandboxes {
		if strings.HasPrefix(key, workspace+".") {
			result = append(result, key)
		}
	}
	return result, nil
}

func (f *FakeOpenShellClient) KeepAlive(ctx context.Context, sandboxName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Simulate keepalive by refreshing the sandbox entry timestamp (no-op in fake)
	if _, ok := f.sandboxes[sandboxName]; !ok {
		return fmt.Errorf("sandbox %s not found", sandboxName)
	}
	return nil
}
