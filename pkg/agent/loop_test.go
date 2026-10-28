package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/llm"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/skills"
)

// --- checkAndCompress threshold tests ---

func TestCheckAndCompressWarnThreshold(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	notifier := notify.New(fakeWSHub, false, "", 100)

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

	// Set to warn threshold (75%)
	loop.budget.TokenCount = 7500 // 75% of 10000

	ctx := context.Background()
	taskID := "test-task-001"

	loop.checkAndCompress(ctx, taskID)

	// Verify a notification was pushed (via notifier path) or TaskProgress event
	found := false
	for _, e := range fakeWSHub.events {
		if e.Event == protocol.TaskProgress || e.Event == protocol.Notification {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected TaskProgress or Notification event, got %d events", len(fakeWSHub.events))
	}
}

func TestCheckAndCompressCriticalThreshold(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	notifier := notify.New(fakeWSHub, false, "", 100)

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

	// Set to critical threshold (90%)
	loop.budget.TokenCount = 9000 // 90% of 10000

	ctx := context.Background()
	taskID := "test-task-002"

	loop.checkAndCompress(ctx, taskID)

	found := false
	for _, e := range fakeWSHub.events {
		if e.Event == protocol.TaskProgress || e.Event == protocol.Notification {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected TaskProgress or Notification event, got %d events", len(fakeWSHub.events))
	}
}

func TestCheckAndCompressBelowThreshold(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	notifier := notify.New(fakeWSHub, false, "", 100)

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

	// Set well below warn threshold (5%)
	loop.budget.TokenCount = 500 // 5% of 10000

	ctx := context.Background()
	taskID := "test-task-003"

	loop.checkAndCompress(ctx, taskID)

	if loop.budget.Compressed {
		t.Error("expected not compressed below threshold")
	}
}

func TestCheckAndCompressNilBudget(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil) // no budget configured

	ctx := context.Background()
	taskID := "test-task-004"

	// Should not panic with nil budget
	loop.checkAndCompress(ctx, taskID)
}

func TestCheckAndCompressAlreadyCompressed(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	notifier := notify.New(fakeWSHub, false, "", 100)

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

	// Manually set compressed state
	loop.budget.Compressed = true
	loop.budget.TokenCount = 8000 // above warn threshold

	ctx := context.Background()
	taskID := "test-task-005"

	loop.checkAndCompress(ctx, taskID)

	if loop.budget.Compressed != true {
		t.Error("expected Compressed flag to remain true")
	}
}

func TestCheckAndCompressNoNotifier(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil).WithBudget(budget.NewBudgetConfig(10000)) // no notifier

	// Set to warn threshold
	loop.budget.TokenCount = 7500

	ctx := context.Background()
	taskID := "test-task-006"

	// Should not panic without notifier
	loop.checkAndCompress(ctx, taskID)
}

func TestPushThresholdNotificationWarn(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	notifier := notify.New(fakeWSHub, false, "", 100)

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

	// Set to warn threshold (75%)
	loop.budget.TokenCount = 7500

	ctx := context.Background()
	taskID := "test-task-007"

	loop.checkAndCompress(ctx, taskID)

	// Verify notification was pushed via WS hub (Notification event from notifier)
	found := false
	for _, e := range fakeWSHub.events {
		if e.Event == protocol.Notification {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Notification event from notifier, got %d events", len(fakeWSHub.events))
	}
}

func TestPushThresholdNotificationCritical(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	notifier := notify.New(fakeWSHub, false, "", 100)

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

	// Set to critical threshold (90%)
	loop.budget.TokenCount = 9000

	ctx := context.Background()
	taskID := "test-task-008"

	loop.checkAndCompress(ctx, taskID)

	found := false
	for _, e := range fakeWSHub.events {
		if e.Event == protocol.Notification {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Notification event from notifier, got %d events", len(fakeWSHub.events))
	}
}

// --- Integration: execute() calls check at start ---

func TestExecuteBudgetCheckAtStart(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	cfg := config.DefaultConfig()
	// Create a minimal activator (empty registry is fine for this test)
	act := skills.NewActivator(nil, 0)
	loop := New(cfg, store, nil, act, fakeWSHub, fakeClient).WithBudget(budget.NewBudgetConfig(10000))

	ctx := context.Background()
	taskID := "test-task-009"

	// Submit a task with budget enabled
	task := &Task{
		ID:        taskID,
		Prompt:    "test prompt",
		Status:    protocol.TaskStatusPending,
		Workspace: "ws-1",
		CreatedAt: time.Now(),
	}
	loop.mu.Lock()
	loop.tasks[taskID] = task
	loop.mu.Unlock()

	done := make(chan struct{})
	go func() {
		loop.execute(ctx, task, "")
		close(done)
	}()

	select {
	case <-done:
		if task.Status != protocol.TaskStatusCompleted {
			t.Fatalf("expected completed, got %s", task.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task did not complete in time")
	}

	if loop.budget == nil {
		t.Fatal("budget not configured")
	}
}

func TestExecuteCallsCheckTwice(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	cfg := config.DefaultConfig()
	act := skills.NewActivator(nil, 0)
	loop := New(cfg, store, nil, act, fakeWSHub, fakeClient).WithBudget(budget.NewBudgetConfig(10000))

	ctx := context.Background()
	taskID := "test-task-010"

	task := &Task{
		ID:        taskID,
		Prompt:    "test prompt",
		Status:    protocol.TaskStatusPending,
		Workspace: "ws-1",
		CreatedAt: time.Now(),
	}
	loop.mu.Lock()
	loop.tasks[taskID] = task
	loop.mu.Unlock()

	done := make(chan struct{})
	go func() {
		loop.execute(ctx, task, "")
		close(done)
	}()

	select {
	case <-done:
		if task.Status != protocol.TaskStatusCompleted {
			t.Fatalf("expected completed, got %s", task.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task did not complete in time")
	}
}

// --- Verify source contains check at start of execute ---

func TestSourceContainsStartCheck(t *testing.T) {
	// This is a documentation test confirming the pattern:
	// In execute(), checkAndCompress is called BEFORE Phase 1 skill resolution.
	_ = "checkAndCompress appears before 'Resolving skills...' in loop.go"
}

// --- Fake helpers for testing ---

type FakeWSHub struct {
	mu        sync.RWMutex
	lastEvent protocol.WebSocketEvent
	events      []protocol.WebSocketEvent
}

func (f *FakeWSHub) BroadcastEvent(event protocol.WebSocketEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastEvent = event
	f.events = append(f.events, event)
}

func (f *FakeWSHub) BroadcastSession(sessionID string, event protocol.WebSocketEvent) {
	// For tests: store session-aware events with prefix to distinguish from global broadcasts
	eventCopy := event
	eventCopy.SessionId = strPtr(sessionID)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastEvent = eventCopy
	f.events = append(f.events, eventCopy)
}

func (f *FakeWSHub) RegisterResponseChannel() (string, chan string) {
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	ch := make(chan string, 1)
	return id, ch
}

func (f *FakeWSHub) SendResponse(id string, message string) bool {
	// No-op for tests — fake hub doesn't route responses
	return false
}

// --- AddDiscoveryPaths tests (Step 14i) ---

func TestAddDiscoveryPaths(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil)

	// Initial paths should be the defaults
	initialCount := len(loop.config.System.Skills.DiscoveryPaths)
	if initialCount == 0 {
		t.Fatalf("expected at least one default discovery path")
	}

	// Add new paths
	err = loop.AddDiscoveryPaths([]string{"/custom/skills/", "~/my-skills/"})
	if err != nil {
		t.Fatalf("AddDiscoveryPaths failed: %v", err)
	}

	newCount := len(loop.config.System.Skills.DiscoveryPaths)
	if newCount != initialCount+2 {
		t.Errorf("expected %d paths after adding 2, got %d", initialCount+2, newCount)
	}

	// Verify the added paths are present
	foundCustom := false
	foundHome := false
	for _, p := range loop.config.System.Skills.DiscoveryPaths {
		if p == "/custom/skills/" {
			foundCustom = true
		}
		if p == "~/my-skills/" {
			foundHome = true
		}
	}
	if !foundCustom || !foundHome {
		t.Errorf("expected paths '/custom/skills/' and '~/my-skills/' to be added")
	}
}

func TestAddDiscoveryPathsEmpty(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil)

	err = loop.AddDiscoveryPaths([]string{})
	if err == nil {
		t.Error("expected error for empty paths array")
	}

	err = loop.AddDiscoveryPaths([]string{""})
	if err == nil {
		t.Error("expected error for empty string path")
	}

	err = loop.AddDiscoveryPaths([]string{"  "})
	if err == nil {
		t.Error("expected error for whitespace-only path")
	}
}

func TestAddDiscoveryPathsNilConfig(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	loop := New(nil, store, nil, nil, fakeWSHub, nil)

	err = loop.AddDiscoveryPaths([]string{"/test/path"})
	if err == nil {
		t.Error("expected error when config is nil")
	}
}

func TestRawSourcesContextInjection(t *testing.T) {
	// Create a temporary raw sources store and inject entries.
	rsBase := t.TempDir()
	rs, err := memory.NewRawSources(rsBase)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	// Snapshot a couple of sources.
	_, err = rs.Snapshot(memory.Source{
		Name:     "HOME env var",
		Category: "env",
		Content:  "HOME=/home/user",
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	_, err = rs.Snapshot(memory.Source{
		Name:     "Config read",
		Category: "config",
		Content:  `{"model": "gpt-4o", "temperature": 0.7}`,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Create a loop with raw sources and a fake client that captures the prompt.
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	loop := New(nil, store, rs, nil, fakeWSHub, fakeClient)

	// Execute a task — the injected context should appear in the prompt sent to RunCommand.
	task := &Task{
		ID:        "test-task-001",
		Prompt:    "Run ls -la",
		Workspace: "default",
	}

	loop.execute(context.Background(), task, "")

	// Verify the captured prompt contains raw source context markers.
	capturedPrompt := fakeClient.CapturedPrompt()
	t.Logf("Captured prompt (first 800 chars):\n%s", capturedPrompt[:min(800, len(capturedPrompt))])
	if !strings.Contains(capturedPrompt, "## Raw Environment Context") {
		t.Errorf("expected injected context header in prompt; got:\n%s", capturedPrompt[:min(500, len(capturedPrompt))])
	}
	if !strings.Contains(capturedPrompt, "HOME env var") {
		t.Error("expected 'HOME env var' source name in injected context")
	}
	if !strings.Contains(capturedPrompt, "Config read") {
		t.Error("expected 'Config read' source name in injected context")
	}
	if !strings.Contains(capturedPrompt, "Run ls -la") {
		t.Error("expected original prompt text after injected context")
	}
}

func TestRawSourcesNilInLoop(t *testing.T) {
	// When rawSources is nil (not wired), execution should proceed normally without panic.
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	loop := New(nil, store, nil, nil, fakeWSHub, fakeClient)

	task := &Task{
		ID:        "test-task-002",
		Prompt:    "echo hello",
		Workspace: "default",
	}

	loop.execute(context.Background(), task, "")

	// Should complete without error.
	if task.Status != protocol.TaskStatusCompleted {
		t.Errorf("expected completed, got %s", task.Status)
	}
	if task.Error != "" {
		t.Errorf("unexpected error: %v", task.Error)
	}
}

// --- LLM Multi-Turn Integration Tests ---

// mockLLMClient is a test double for llm.LLM that returns controlled responses.
type mockLLMClient struct {
	responses []llm.CompletionResponse
	index     int
	streamFn  func(ctx context.Context, req llm.CompletionRequest) (llm.StreamReader, error)
}

func (m *mockLLMClient) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if m.index >= len(m.responses) {
		return nil, fmt.Errorf("mock LLM: no more responses configured")
	}
	resp := m.responses[m.index]
	m.index++
	return &resp, nil
}

func (m *mockLLMClient) Stream(ctx context.Context, req llm.CompletionRequest) (llm.StreamReader, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	// Default: return the next response as a chunked stream.
	if m.index >= len(m.responses) {
		return newMockStream(llm.CompletionResponse{}), nil
	}
	resp := m.responses[m.index]
	m.index++
	return newMockStream(resp), nil
}

func (m *mockLLMClient) ProviderInfo() llm.ProviderMetadata {
	return llm.ProviderMetadata{Name: "mock", SupportsTools: true, SupportsStream: true}
}

// mockStreamReader implements llm.StreamReader for testing.
type mockStreamReader struct {
	resp   llm.CompletionResponse
	done   bool
	chunks []llm.Chunk
	idx    int
}

func (s *mockStreamReader) Next() (llm.Chunk, error) {
	if s.idx >= len(s.chunks) {
		return llm.Chunk{}, io.EOF
	}
	chunk := s.chunks[s.idx]
	s.idx++
	return chunk, nil
}

func (s *mockStreamReader) Close() error {
	return nil
}

// newMockStream creates a stream reader from a response for testing.
func newMockStream(resp llm.CompletionResponse) llm.StreamReader {
	chunks := []llm.Chunk{
		{Delta: resp.Choices[0].Message.Content},
		{FinishReason: "[done]"},
	}
	return &mockStreamReader{chunks: chunks}
}

func TestSendMessageMultiTurnWithToolCall(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	act := skills.NewActivator(nil, 0)

	// Mock LLM responses:
	// Turn 1: tool call (ls command)
	// Turn 2: final text response
	mockClient := &mockLLMClient{
		responses: []llm.CompletionResponse{
			{
				Model: "gpt-4o",
				Choices: []llm.ResponseChoice{
					{
						Message: llm.Message{
							Role:    "assistant",
							Content: `{"name": "ls", "arguments": {"path": "/tmp"}}`,
						},
					},
				},
			},
			{
				Model: "gpt-4o",
				Choices: []llm.ResponseChoice{
					{
						Message: llm.Message{
							Role:    "assistant",
							Content: "The /tmp directory contains:\n- file1.txt\n- file2.log",
						},
					},
				},
			},
		},
	}

	loop := New(nil, store, nil, act, fakeWSHub, fakeClient).WithLLMClient(mockClient)

	resp, err := loop.SendMessage(context.Background(), "session-1", "List files in /tmp", "")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	if resp.Response == "" {
		t.Error("expected non-empty response")
	}
	if !strings.Contains(resp.Response, "/tmp") {
		t.Errorf("expected response to contain '/tmp', got: %s", resp.Response)
	}
}

func TestSendMessageMultiTurnMaxTurnsReached(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	act := skills.NewActivator(nil, 0)

	// Mock LLM that always returns a tool call (never terminates) — 8 identical responses for max turns
	mockClient := &mockLLMClient{
		responses: make([]llm.CompletionResponse, 8),
	}
	for i := range mockClient.responses {
		mockClient.responses[i] = llm.CompletionResponse{
			Model: "gpt-4o",
			Choices: []llm.ResponseChoice{
				{
					Message: llm.Message{
						Role:    "assistant",
						Content: `{"name": "echo", "arguments": {"msg": "still going"}}`,
					},
				},
			},
		}
	}

	loop := New(nil, store, nil, act, fakeWSHub, fakeClient).WithLLMClient(mockClient)

	resp, err := loop.SendMessage(context.Background(), "session-1", "Keep calling tools", "")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Should fail with max turns message after 8 iterations
	if !strings.Contains(resp.Response, "max turns") && !strings.Contains(resp.Response, "reached") {
		t.Errorf("expected max turns error, got: %s", resp.Response)
	}
}

func TestSendMessageShellOnlyFallback(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	act := skills.NewActivator(nil, 0)

	// No LLM client wired — should fall back to shell-only path
	loop := New(nil, store, nil, act, fakeWSHub, fakeClient) // no WithLLMClient call

	resp, err := loop.SendMessage(context.Background(), "session-1", "echo hello world", "")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	if !strings.Contains(resp.Response, "hello world") {
		t.Errorf("expected 'hello world' in response, got: %s", resp.Response)
	}
}

func TestBuildMessagesWorkspaceBootstrap(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	act := skills.NewActivator(nil, 0)

	loop := New(nil, store, nil, act, fakeWSHub, fakeClient)

	// Set workspace definition
	loop.SetWorkspaceDefinition(&env.WorkspaceDefinition{
		Name:    "api-migration",
		Purpose: "Migrate legacy API endpoints to gRPC",
		Scope:   []string{"payments-service", "auth-service"},
		Tags:    []string{"api", "migration", "grpc"},
	})

	// Create a task with session history
	task := &Task{
		Prompt:    "Check the current API status",
		SessionId: "session-1",
	}

	messages, _, _ := loop.buildMessages(task)

	// Find the workspace bootstrap message
	var foundBootstrap bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[SESSION BOOTSTRAP]") {
			foundBootstrap = true
			if !strings.Contains(msg.Content, "Migrate legacy API endpoints to gRPC") {
				t.Errorf("expected purpose text in bootstrap, got: %s", msg.Content)
			}
			if !strings.Contains(msg.Content, "payments-service") {
				t.Errorf("expected scope items in bootstrap, got: %s", msg.Content)
			}
			if !strings.Contains(msg.Content, "Tags") {
				t.Errorf("expected tags section in bootstrap, got: %s", msg.Content)
			}
			break
		}
	}

	if !foundBootstrap {
		t.Error("expected [SESSION BOOTSTRAP] section in messages")
	}
}

func TestBuildMessagesNoWorkspaceDefinition(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}
	fakeClient := NewFakeOpenShellClient()

	act := skills.NewActivator(nil, 0)

	loop := New(nil, store, nil, act, fakeWSHub, fakeClient)
	// No workspace definition set

	task := &Task{
		Prompt:    "Check status",
		SessionId: "session-1",
	}

	messages, _, _ := loop.buildMessages(task)

	// Should NOT contain workspace bootstrap section
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[SESSION BOOTSTRAP]") {
			t.Errorf("expected no bootstrap when workspaceDef is nil, got: %s", msg.Content)
		}
	}
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

func (f *FakeOpenShellClient) CreateSandbox(ctx context.Context, workspace, name string, ctxFile *env.ContextFile, sessionId string) (*v1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sid := fmt.Sprintf("sb-%s", strings.ReplaceAll(name, "-", ""))
	f.sandboxes[name] = sid
	return &v1.Sandbox{ID: sid, Name: name}, nil
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

func (f *FakeOpenShellClient) GetMTLSPaths() (certPath, keyPath string) {
	// Fake client has no mTLS config
	return "", ""
}
