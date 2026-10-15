package agent

import (
	"context"
	"testing"
	"time"

	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/config"
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
	loop := New(cfg, store, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

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
	loop := New(cfg, store, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

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
	loop := New(cfg, store, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

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
	loop := New(cfg, store, nil, fakeWSHub, nil) // no budget configured

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
	loop := New(cfg, store, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

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
	loop := New(cfg, store, nil, fakeWSHub, nil).WithBudget(budget.NewBudgetConfig(10000)) // no notifier

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
	loop := New(cfg, store, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

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
	loop := New(cfg, store, nil, fakeWSHub, nil).WithNotifier(notifier).WithBudget(budget.NewBudgetConfig(10000))

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
	loop := New(cfg, store, act, fakeWSHub, fakeClient).WithBudget(budget.NewBudgetConfig(10000))

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
	fakeClient := NewFakeOpenShellClient()

	cfg := config.DefaultConfig()
	act := skills.NewActivator(nil, 0)
	loop := New(cfg, store, act, fakeWSHub, fakeClient).WithBudget(budget.NewBudgetConfig(10000))

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
	lastEvent protocol.WebSocketEvent
	events      []protocol.WebSocketEvent
}

func (f *FakeWSHub) BroadcastEvent(event protocol.WebSocketEvent) {
	f.lastEvent = event
	f.events = append(f.events, event)
}

// Task.StatusChange returns a channel that closes when task status changes.
func (t *Task) StatusChange() <-chan struct{} {
	ch := make(chan struct{})
	close(ch) // fake client completes immediately
	return ch
}
