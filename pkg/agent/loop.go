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

	"github.com/google/uuid"
	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
)

// Task represents an in-flight task with its execution state.
type Task struct {
	ID        string
	Prompt    string
	Status    protocol.TaskStatus
	Workspace string
	Output    []byte
	Error     string
	RetryCount int
	CreatedAt time.Time
}

// Loop is the main agent loop managing task execution.
type Loop struct {
	mu          sync.Mutex
	tasks       map[string]*Task // taskID -> Task state
	store       *memory.Store    // persistent state
	rawSources  *memory.RawSources // raw environment snapshots for context injection
	activator   *skills.Activator
	config      *config.Config
	openShell   OpenShellClient
	wsHub       WSHubPublisher
	notifier    *notify.Notifier
	resultsCh   chan scheduler.TaskResult // results from scheduler (nil if not wired)
	maxRetries  int
	budget      *budget.SessionState // token budget tracker (nil if disabled)
}

// WorkspaceConfig returns the merged (system + workspace overrides) config for a given workspace.
func (l *Loop) WorkspaceConfig(workspaceID string) *config.WorkspaceConfig {
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

// OpenShellClient abstracts the OpenShell SDK for testing.
type OpenShellClient interface {
	CreateSandbox(ctx context.Context, workspace, name string) (string, error)
	WaitReady(ctx context.Context, workspace, name string, timeout time.Duration) error
	RunCommand(ctx context.Context, sandboxID, command string) (string, int, error)
	GetLogs(ctx context.Context, sandboxID string) ([]string, error)
	DeleteSandbox(ctx context.Context, workspace, name string) error
	ListSandboxes(ctx context.Context, workspace string) ([]string, error)
}

// WSHubPublisher sends WebSocket push events.
type WSHubPublisher interface {
	BroadcastEvent(event protocol.WebSocketEvent)
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

// Submit enqueues a task and begins execution.
func (l *Loop) Submit(ctx context.Context, prompt string, workspaceID string) (*Task, error) {
	taskID := uuid.New().String()

	task := &Task{
		ID:        taskID,
		Prompt:    prompt,
		Status:    protocol.TaskStatusPending,
		Workspace: workspaceID,
		CreatedAt: time.Now(),
	}

	l.mu.Lock()
	l.tasks[taskID] = task
	l.mu.Unlock()

	// Persist initial state
	if err := l.store.SaveTask(taskID, taskToMap(task)); err != nil {
		log.Printf("agent: failed to persist task %s: %v", taskID, err)
	}

	// Push progress event
	l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
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

	// Phase 1: Skill resolution and activation
	l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
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
		l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			TaskId:  &task.ID,
			Message: strPtr(fmt.Sprintf("Activated skills: %s", strings.Join(activeSkills, ", "))),
			Step:    intPtr(2),
		})
	} else {
		l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			TaskId:  &task.ID,
			Message: strPtr("Activated skills: (none)"),
			Step:    intPtr(2),
		})
	}

	// Phase 2: Sandbox creation
	l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Creating sandbox..."),
		Step:    intPtr(3),
	})

	sandboxName := fmt.Sprintf("norman-task-%s", task.ID[:8])
	sandboxID, err := l.openShell.CreateSandbox(ctx, task.Workspace, sandboxName)
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

	// Phase 3: Wait for ready
	l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Waiting for sandbox ready..."),
		Step:    intPtr(4),
	})

	if err := l.openShell.WaitReady(ctx, task.Workspace, sandboxName, 60*time.Second); err != nil {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("sandbox not ready: %v", err)
		l.pushCompletion(task)
		return
	}

	// Phase 4: Execute — inject raw sources context into prompt
	l.mu.Lock()
	task.Status = protocol.TaskStatusRunning
	l.mu.Unlock()

	l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &task.ID,
		Message: strPtr("Executing task..."),
		Step:    intPtr(5),
	})

	// Inject raw source context into the prompt if available.
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

	output, exitCode, err := l.openShell.RunCommand(ctx, sandboxID, prompt)
	if err != nil {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("execution failed (exit %d): %v", exitCode, err)
	} else if exitCode != 0 {
		task.Status = protocol.TaskStatusFailed
		task.Error = fmt.Sprintf("non-zero exit code: %d", exitCode)
	} else {
		task.Output = []byte(output)
		task.Status = protocol.TaskStatusCompleted
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

	// Broadcast WebSocket notification
	l.wsHub.BroadcastEvent(protocol.WebSocketEvent{
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

	l.wsHub.BroadcastEvent(completionEvent)

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
	f.sandboxes[workspace+"."+name] = sid
	return sid, nil
}

func (f *FakeOpenShellClient) WaitReady(ctx context.Context, workspace, name string, timeout time.Duration) error {
	// Simulate readiness after a short delay
	select {
	case <-time.After(100 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *FakeOpenShellClient) RunCommand(ctx context.Context, sandboxID, command string) (string, int, error) {
	f.mu.Lock()
	f.commands = append(f.commands, command)
	f.capturedPrompt = command
	f.mu.Unlock()

	// Simulate output
	output := fmt.Sprintf("Executed: %s\nOutput from sandbox %s", command, sandboxID)
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
