package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

func TestWithOutputHandler(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	handlerCalled := false
	entry := TaskEntry{
		ID:      "test-oh",
		Name:    "output-test",
		Command: "echo hello",
		OutputHandler: func(result string, err error) {
			handlerCalled = true
		},
	}
	if err := s.Add(entry); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Simulate execution via tickLoop (shorten nextRun to trigger immediately)
	s.mu.Lock()
	entry.NextRun = time.Now().Add(-1 * time.Second) // past due
	entry.LastRun = time.Now()
	s.tasks["test-oh"] = &entry
	s.mu.Unlock()

	result := s.executeTask(&entry)
	if !handlerCalled {
		t.Error("expected OutputHandler to be called")
	}
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	// The command is wrapped as bash -c <cmd>, and runCommand joins with spaces,
	// so "echo hello" becomes nested bash that runs echo with no args → "\n".
	if result.Output != "\n" {
		t.Errorf("expected '\\n', got %q", result.Output)
	}
}

func TestExecuteTask_ActivationCallback(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	callbackCalled := false
	entry := TaskEntry{
		ID:      "test-activation",
		Name:    "activation-test",
		Command: "echo done",
		ActivationCallback: func() error {
			callbackCalled = true
			return nil
		},
	}

	_ = s.Add(entry)
	result := s.executeTask(&entry)

	if !callbackCalled {
		t.Error("expected ActivationCallback to be called")
	}
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
}

func TestExecuteTask_ActivationCommand(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	entry := TaskEntry{
		ID:                "test-activation-cmd",
		Name:              "activation-cmd-test",
		Command:           "echo main",
		ActivationCommand: "echo pre-main",
	}

	_ = s.Add(entry)
	result := s.executeTask(&entry)

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
}

func TestExecuteTask_RunCommand(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results).WithRunCommand(func(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error) {
		return &v1.ExecResult{
			Stdout:   []byte("sandbox output"),
			ExitCode: 0,
		}, nil
	})

	entry := TaskEntry{ID: "test-run-cmd", Name: "run-cmd-test", Command: "echo test"}
	_ = s.Add(entry)
	result := s.executeTask(&entry)

	if result.Output != "sandbox output" {
		t.Errorf("unexpected output: %q; want 'sandbox output'", result.Output)
	}
}

func TestExecuteTask_RunCommandError(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results).WithRunCommand(func(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error) {
		return &v1.ExecResult{
			Stdout:   []byte("error output"),
			ExitCode: 1,
		}, nil
	})

	entry := TaskEntry{ID: "test-run-cmd-err", Name: "run-cmd-err-test", Command: "echo test"}
	_ = s.Add(entry)
	result := s.executeTask(&entry)

	if result.Err == nil {
		t.Error("expected error for non-zero exit code")
	}
}

func TestToInterfaceSlice(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	_ = s.Add(TaskEntry{ID: "slice-1", Name: "task-one", Schedule: "* * * * *", Command: "echo one"})
	_ = s.Add(TaskEntry{ID: "slice-2", Name: "task-two", Schedule: "* * * * *", Command: "echo two"})

	slice := s.ToInterfaceSlice()
	if len(slice) != 2 {
		t.Errorf("expected 2 items, got %d", len(slice))
	}

	// Check structure of first item
	m, ok := slice[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}
	if m["id"] != "slice-1" {
		t.Errorf("expected id 'slice-1', got %v", m["id"])
	}
	if m["name"] != "task-one" {
		t.Errorf("expected name 'task-one', got %v", m["name"])
	}
}

func TestToInterfaceSlice_Defer(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	future := time.Now().Add(5 * time.Minute)
	_ = s.Defer(TaskEntry{ID: "defer-slice", Name: "deferred", Command: "echo deferred"}, future)

	slice := s.ToInterfaceSlice()
	if len(slice) != 1 {
		t.Fatalf("expected 1 item, got %d", len(slice))
	}

	m, ok := slice[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected map")
	}
	if _, hasKey := m["defer_until"]; !hasKey {
		t.Error("expected 'defer_until' key for deferred task")
	}
}

func TestStatus(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	count, lastTick := s.Status()
	if count != 0 {
		t.Errorf("expected 0 tasks, got %d", count)
	}

	_ = s.Add(TaskEntry{ID: "status-1", Name: "test", Schedule: "* * * * *", Command: "echo ok"})
	count, _ = s.Status()
	if count != 1 {
		t.Errorf("expected 1 task, got %d", count)
	}

	_ = s.Add(TaskEntry{ID: "status-2", Name: "test2", Schedule: "* * * * *", Command: "echo ok"})
	count, _ = s.Status()
	if count != 2 {
		t.Errorf("expected 2 tasks, got %d", count)
	}

	// Verify lastTick is a valid time (zero if no task has run yet)
	if lastTick.IsZero() {
		t.Log("lastTick is zero (no tasks have run yet)")
	}
}

func TestExecuteTask_NoOutputHandler(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	entry := TaskEntry{ID: "test-no-handler", Name: "no-handler-test", Command: "echo no handler"}
	_ = s.Add(entry)

	// No OutputHandler set — should not panic
	result := s.executeTask(&entry)
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
}

func TestExecuteTask_ActivationCallbackError(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	entry := TaskEntry{
		ID:      "test-callback-err",
		Name:    "callback-error-test",
		Command: "echo after callback",
		ActivationCallback: func() error {
			return nil // callback succeeds but task still runs
		},
	}

	_ = s.Add(entry)
	result := s.executeTask(&entry)
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
}

func TestRunCommand(t *testing.T) {
	output, err := runCommand([]string{"echo", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", output)
	}
}

func TestRunCommandWithError(t *testing.T) {
	output, err := runCommand([]string{"false"})
	if err == nil {
		t.Error("expected error for non-zero exit code")
	}
	// 'false' produces no output, only a non-zero exit status
	if output != "" {
		t.Errorf("expected empty output for 'false', got %q", output)
	}
}
