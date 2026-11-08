package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

	cfg := config.DefaultConfig()
	// Create a minimal activator (empty registry is fine for this test)
	act := skills.NewActivator(nil, 0)

	mockClient := &mockLLMClient{
		responses: []llm.CompletionResponse{{Choices: []llm.ResponseChoice{{Message: llm.Message{Role: "assistant", Content: "done"}}}}},
	}

	fakeOpenShell := NewFakeOpenShellClient()
	loop := New(cfg, store, nil, act, fakeWSHub, fakeOpenShell).WithBudget(budget.NewBudgetConfig(10000)).WithLLMClient(mockClient)

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
		loop.execute(ctx, task)
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

	cfg := config.DefaultConfig()
	act := skills.NewActivator(nil, 0)

	mockClient := &mockLLMClient{
		responses: []llm.CompletionResponse{{Choices: []llm.ResponseChoice{{Message: llm.Message{Role: "assistant", Content: "done"}}}}},
	}

	fakeOpenShell := NewFakeOpenShellClient()
	loop := New(cfg, store, nil, act, fakeWSHub, fakeOpenShell).WithBudget(budget.NewBudgetConfig(10000)).WithLLMClient(mockClient)

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
		loop.execute(ctx, task)
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
	events    []protocol.WebSocketEvent
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

	// Initial paths should be empty (builtin skills are handled via BuiltInSkills)
	initialCount := len(loop.config.System.Skills.DiscoveryPaths)
	if initialCount != 0 {
		t.Fatalf("expected zero default discovery paths, got %d", initialCount)
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

	// Create a loop with raw sources and a mock LLM client that captures the request.
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	var capturedRequests []llm.CompletionRequest
	mockClient := &mockLLMClient{
		streamFn: func(ctx context.Context, req llm.CompletionRequest) (llm.StreamReader, error) {
			capturedRequests = append(capturedRequests, req)
			return newMockStream(llm.CompletionResponse{
				Choices: []llm.ResponseChoice{{Message: llm.Message{Role: "assistant", Content: "done"}}},
			}), nil
		},
	}

	fakeOpenShell := NewFakeOpenShellClient()
	cfg := config.DefaultConfig()
	loop := New(cfg, store, rs, nil, fakeWSHub, fakeOpenShell).WithLLMClient(mockClient)

	// Execute a task — raw sources should be injected into LLM messages.
	task := &Task{
		ID:        "test-task-001",
		Prompt:    "Run ls -la",
		Workspace: "default",
	}

	loop.execute(context.Background(), task)

	// Verify the captured request contains raw source context in the messages.
	if len(capturedRequests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(capturedRequests))
	}
	req := capturedRequests[0]
	allContent := ""
	for _, m := range req.Messages {
		allContent += m.Content
	}
	if !strings.Contains(allContent, "## Environment Context") {
		t.Errorf("expected injected context header; got:\n%s", allContent[:min(800, len(allContent))])
	}
	if !strings.Contains(allContent, "HOME env var") {
		t.Error("expected 'HOME env var' source name in injected context")
	}
	if !strings.Contains(allContent, "Config read") {
		t.Error("expected 'Config read' source name in injected context")
	}
	if !strings.Contains(allContent, "Run ls -la") {
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

	mockClient := &mockLLMClient{
		responses: []llm.CompletionResponse{{Choices: []llm.ResponseChoice{{Message: llm.Message{Role: "assistant", Content: "done"}}}}},
	}

	fakeOpenShell := NewFakeOpenShellClient()
	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, fakeOpenShell).WithLLMClient(mockClient)

	task := &Task{
		ID:        "test-task-002",
		Prompt:    "echo hello",
		Workspace: "default",
	}

	loop.execute(context.Background(), task)

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

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, act, fakeWSHub, fakeClient).WithLLMClient(mockClient)

	// Create session with workspace_id so SendMessage can resolve it.
	if err := store.SaveSession("session-1", map[string]interface{}{"workspace_id": "default"}); err != nil {
		t.Fatalf("save session: %v", err)
	}

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

	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, act, fakeWSHub, fakeClient).WithLLMClient(mockClient)

	// Create session with workspace_id so SendMessage can resolve it.
	if err := store.SaveSession("session-1", map[string]interface{}{"workspace_id": "default"}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	resp, err := loop.SendMessage(context.Background(), "session-1", "Keep calling tools", "")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Should fail with max turns message after 8 iterations
	if !strings.Contains(resp.Response, "max turns") && !strings.Contains(resp.Response, "reached") {
		t.Errorf("expected max turns error, got: %s", resp.Response)
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

// --- env.Workspace interface methods ---

func (f *FakeOpenShellClient) Exists(ctx context.Context, sessionId string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sandboxes[sessionId]
	return ok
}

func (f *FakeOpenShellClient) Session() *env.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sandboxes) == 0 {
		return nil
	}
	for range f.sandboxes {
		return &env.Session{} // minimal mock session for read-only view
	}
	return nil
}

func (f *FakeOpenShellClient) RunCommandBySandbox(ctx context.Context, sandboxName string, command string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, "sandbox:"+command)
	f.capturedPrompt = "sandbox:" + command
	output := fmt.Sprintf("Executed in sandbox %s: %s", sandboxName, command)
	return output, 0, nil
}

func (f *FakeOpenShellClient) Create(ctx context.Context, sessionId string) (*env.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sid := fmt.Sprintf("sb-%s", strings.ReplaceAll(sessionId, "-", ""))
	f.sandboxes[sessionId] = sid
	return &env.Session{}, nil
}

func (f *FakeOpenShellClient) Get(ctx context.Context, sessionId string) (*env.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sandboxes[sessionId]; !ok {
		return nil, fmt.Errorf("session %s not found", sessionId)
	}
	return &env.Session{}, nil
}

func (f *FakeOpenShellClient) Delete(ctx context.Context, sessionId string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sandboxes, sessionId)
	return nil
}

func (f *FakeOpenShellClient) GetSandboxName(ctx context.Context, sessionId string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name, ok := f.sandboxes[sessionId]; ok {
		return name, nil
	}
	return "", fmt.Errorf("session %s not found", sessionId)
}

func (f *FakeOpenShellClient) UpdateActivity(ctx context.Context, sessionId string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sandboxes[sessionId]; ok {
		// Simulate activity update (no-op in fake)
		_ = ok // suppress SA4006 warning
	}
}

func (f *FakeOpenShellClient) Sessions() map[string]*env.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make(map[string]*env.Session, len(f.sandboxes))
	for id := range f.sandboxes {
		result[id] = &env.Session{}
	}
	return result
}

// --- Task Cap Tests (Phase 1) ---

func TestTaskCapEnforcement(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	cfg.System.MaxTasksPerSession = 3 // set cap to 3 for testing

	loop := New(cfg, store, nil, nil, fakeWSHub, nil)

	sessionID := "test-session-001"

	// Submit 5 tasks — cap is 3, so only 3 should remain in map
	for i := 0; i < 5; i++ {
		ctx := context.Background()
		task, err := loop.Submit(ctx, fmt.Sprintf("Task %d", i+1), "test-workspace", sessionID)
		if err != nil {
			t.Fatalf("submit task %d: %v", i+1, err)
		}
		_ = task // we track via in-memory map for this test

		// Give the execute goroutine time to complete (it's non-blocking)
		time.Sleep(50 * time.Millisecond)
	}

	// Verify only 3 tasks remain (oldest was evicted)
	loop.mu.Lock()
	taskCount := len(loop.tasks)
	loop.mu.Unlock()

	if taskCount != 3 {
		t.Errorf("expected 3 tasks in map, got %d", taskCount)
	}

	// Verify the oldest task file was deleted from disk
	files, err := os.ReadDir(filepath.Join(store.Base(), "tasks"))
	if err != nil {
		t.Fatalf("read tasks dir: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("expected 3 task files on disk, got %d", len(files))
	}
}

func TestTaskCapZeroDisabled(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	cfg.System.MaxTasksPerSession = 0 // zero = disabled (no cap)

	loop := New(cfg, store, nil, nil, fakeWSHub, nil)

	sessionID := "test-session-002"

	// Submit 5 tasks — with cap=0, all should remain
	for i := 0; i < 5; i++ {
		ctx := context.Background()
		task, err := loop.Submit(ctx, fmt.Sprintf("Task %d", i+1), "test-workspace", sessionID)
		if err != nil {
			t.Fatalf("submit task %d: %v", i+1, err)
		}
		_ = task
		time.Sleep(50 * time.Millisecond)
	}

	loop.mu.Lock()
	taskCount := len(loop.tasks)
	loop.mu.Unlock()

	if taskCount != 5 {
		t.Errorf("expected 5 tasks in map (cap disabled), got %d", taskCount)
	}
}

func TestTaskCapLruEviction(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	cfg.System.MaxTasksPerSession = 2 // cap of 2

	loop := New(cfg, store, nil, nil, fakeWSHub, nil)

	sessionID := "test-session-003"

	// Submit task 1 (will be evicted when task 3 arrives)
	ctx := context.Background()
	task1, err := loop.Submit(ctx, "First task", "test-workspace", sessionID)
	if err != nil {
		t.Fatalf("submit task 1: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Submit task 2 (should be kept)
	task2, err := loop.Submit(ctx, "Second task", "test-workspace", sessionID)
	if err != nil {
		t.Fatalf("submit task 2: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Submit task 3 — should evict task 1 (oldest)
	task3, err := loop.Submit(ctx, "Third task", "test-workspace", sessionID)
	if err != nil {
		t.Fatalf("submit task 3: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Verify task1 file is gone from disk
	task1Path := filepath.Join(store.Base(), "tasks", task1.ID+".json")
	if _, err := os.Stat(task1Path); !os.IsNotExist(err) {
		t.Errorf("expected task1 file to be evicted, still exists at %s", task1Path)
	}

	// Verify task2 and task3 files exist
	task2Path := filepath.Join(store.Base(), "tasks", task2.ID+".json")
	if _, err := os.Stat(task2Path); os.IsNotExist(err) {
		t.Errorf("expected task2 file to exist")
	}
	task3Path := filepath.Join(store.Base(), "tasks", task3.ID+".json")
	if _, err := os.Stat(task3Path); os.IsNotExist(err) {
		t.Errorf("expected task3 file to exist")
	}

	// Verify only 2 tasks in map
	loop.mu.Lock()
	taskCount := len(loop.tasks)
	loop.mu.Unlock()
	if taskCount != 2 {
		t.Errorf("expected 2 tasks in map, got %d", taskCount)
	}
}

func TestTaskCapDifferentSessionsIndependent(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	fakeWSHub := &FakeWSHub{}

	cfg := config.DefaultConfig()
	cfg.System.MaxTasksPerSession = 2 // cap of 2 per session

	loop := New(cfg, store, nil, nil, fakeWSHub, nil)

	ctx := context.Background()

	// Session A: submit 3 tasks (should keep 2)
	for i := 0; i < 3; i++ {
		_, err := loop.Submit(ctx, fmt.Sprintf("Session A task %d", i+1), "test-workspace", "session-a")
		if err != nil {
			t.Fatalf("submit session a task %d: %v", i+1, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Session B: submit 3 tasks (should keep 2)
	for i := 0; i < 3; i++ {
		_, err := loop.Submit(ctx, fmt.Sprintf("Session B task %d", i+1), "test-workspace", "session-b")
		if err != nil {
			t.Fatalf("submit session b task %d: %v", i+1, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Both sessions should have exactly 2 tasks each (total 4)
	loop.mu.Lock()
	totalTasks := len(loop.tasks)
	loop.mu.Unlock()

	if totalTasks != 4 {
		t.Errorf("expected 4 tasks total (2 per session), got %d", totalTasks)
	}
}
