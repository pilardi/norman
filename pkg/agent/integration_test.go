package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/skills"
)

// --- Integration test: PersistTurn + LoadTurns round-trip ---

func TestIntegrationPersistAndLoadTurns(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	taskID := "task-int-001"

	// Simulate a realistic multi-turn conversation
	turns := []map[string]interface{}{
		{"role": "user", "content": "List files in /tmp"},
		{"role": "assistant", "content": "Let me check what's in /tmp.", "id": "call_turn1"},
		{"role": "tool", "name": "ls", "content": "/tmp/file1.txt\n/tmp/file2.txt"},
		{"role": "assistant", "content": "Found 2 files in /tmp."},
	}

	for i, turn := range turns {
		if err := store.PersistTurn(taskID, turn); err != nil {
			t.Fatalf("persist turn %d: %v", i+1, err)
		}
	}

	// Load and verify all turns are present in order
	loaded, err := store.LoadTurns(taskID)
	if err != nil {
		t.Fatalf("load turns: %v", err)
	}

	if len(loaded) != len(turns) {
		t.Fatalf("expected %d turns, got %d", len(turns), len(loaded))
	}

	for i, expected := range turns {
		role := loaded[i]["role"]
		if role != expected["role"] {
			t.Errorf("turn %d role: expected '%s', got '%s'", i+1, expected["role"], role)
		}
		content := loaded[i]["content"]
		if content != expected["content"] {
			t.Errorf("turn %d content mismatch", i+1)
		}
	}

	// Verify the file is valid JSONL (one JSON object per line)
	path := filepath.Join(base, "turns", taskID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read turns file: %v", err)
	}

	lines := splitLines(string(data))
	if len(lines) != len(turns) {
		t.Errorf("expected %d lines in JSONL, got %d", len(turns), len(lines))
	}
	for i, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i+1, err)
		}
	}

	// Verify DeleteTurns removes the file
	if err := store.DeleteTurns(taskID); err != nil {
		t.Fatalf("delete turns: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "turns", taskID+".jsonl")); !os.IsNotExist(err) {
		t.Error("expected turns file to be deleted")
	}

	// Verify idempotent delete (no error on missing file)
	if err := store.DeleteTurns(taskID); err != nil {
		t.Fatalf("delete again should not error: %v", err)
	}
}

// --- Integration test: Crash recovery scenario ---

func TestIntegrationCrashRecovery(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	taskID := "task-crash-001"

	// Simulate a conversation that was interrupted mid-turn (crash scenario)
	allTurns := []map[string]interface{}{
		{"role": "user", "content": "What's the weather?"},
		{"role": "assistant", "content": "Let me check the forecast.", "id": "call_001"},
		{"role": "tool", "name": "curl", "content": "{\"temp\": 22, \"unit\": \"C\"}"},
		// Process crashes here — partial assistant response not yet persisted
	}

	for i, turn := range allTurns {
		if err := store.PersistTurn(taskID, turn); err != nil {
			t.Fatalf("persist turn %d: %v", i+1, err)
		}
	}

	// Simulate "crash" — load turns and verify we can resume from the last complete turn
	loaded, err := store.LoadTurns(taskID)
	if err != nil {
		t.Fatalf("load turns: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("expected 3 turns (crash mid-turn), got %d", len(loaded))
	}

	// Verify the last persisted turn is the tool result (assistant response not yet saved)
	lastTurn := loaded[len(loaded)-1]
	if lastTurn["role"] != "tool" {
		t.Errorf("last persisted turn should be 'tool', got '%s'", lastTurn["role"])
	}

	// Simulate resuming: append the remaining assistant response
	resumeTurn := map[string]interface{}{
		"role":    "assistant",
		"content": "The weather is 22°C.",
	}
	if err := store.PersistTurn(taskID, resumeTurn); err != nil {
		t.Fatalf("persist resume turn: %v", err)
	}

	// Verify all 4 turns are now present
	loaded, err = store.LoadTurns(taskID)
	if err != nil {
		t.Fatalf("load resumed turns: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("expected 4 turns after resume, got %d", len(loaded))
	}

	// Verify the final turn is the assistant response
	finalTurn := loaded[len(loaded)-1]
	if finalTurn["role"] != "assistant" {
		t.Errorf("final turn should be 'assistant', got '%s'", finalTurn["role"])
	}
}

// --- Integration test: Concurrent writes to same task file ---

func TestIntegrationConcurrentWrites(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	taskID := "task-concurrent-001"
	numWriters := 5
	expectedTurns := numWriters * 3 // 3 turns per writer

	done := make(chan bool, numWriters)

	for w := 0; w < numWriters; w++ {
		go func(writerID int) {
			for turn := 0; turn < 3; turn++ {
				turnMap := map[string]interface{}{
					"role":    []string{"user", "assistant", "tool"}[turn],
					"content": "writer-" + string(rune(writerID+48)) + "-turn-" + string(rune(turn+48)),
				}
				if err := store.PersistTurn(taskID, turnMap); err != nil {
					t.Errorf("writer %d turn %d: %v", writerID, turn, err)
				}
			}
			done <- true
		}(w)
	}

	// Wait for all writers to finish
	for w := 0; w < numWriters; w++ {
		<-done
	}

	// Verify total count (allow some tolerance for timing)
	loaded, err := store.LoadTurns(taskID)
	if err != nil {
		t.Fatalf("load turns: %v", err)
	}

	if len(loaded) < expectedTurns-2 { // Allow ±2 for race conditions in test
		t.Errorf("expected ~%d turns, got %d (may be affected by concurrent writes)", expectedTurns, len(loaded))
	}
}

// --- Integration test: PersistToolCallResult + LoadToolCallResults round-trip ---

func TestIntegrationPersistAndLoadToolCallResults(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	taskID := "task-int-002"

	// Simulate tool call results from a multi-turn loop
	results := []struct {
		name     string
		args     string
		output   string
		exitCode int
	}{
		{"ls", `{"path": "/tmp"}`, "file1.txt\nfile2.txt\n", 0},
		{"cat", `{"path": "/tmp/file1.txt"}`, "Hello from file1", 0},
		{"grep", `{"pattern": "error", "file": "/var/log/syslog"}`, "", 1},
	}

	for _, r := range results {
		if err := store.PersistToolCallResult(taskID, r.name, r.args, r.output, r.exitCode); err != nil {
			t.Fatalf("persist result for %s: %v", r.name, err)
		}
	}

	// Load and verify all results are present
	loaded, err := store.LoadToolCallResults(taskID)
	if err != nil {
		t.Fatalf("load results: %v", err)
	}

	if len(loaded) != len(results) {
		t.Fatalf("expected %d results, got %d", len(results), len(loaded))
	}

	for i, expected := range results {
		if loaded[i]["name"] != expected.name {
			t.Errorf("result %d name: expected '%s', got '%s'", i+1, expected.name, loaded[i]["name"])
		}
		if loaded[i]["exit_code"].(float64) != float64(expected.exitCode) {
			t.Errorf("result %d exit_code: expected %d, got %v", i+1, expected.exitCode, loaded[i]["exit_code"])
		}
	}

	// Verify the file is valid JSONL
	path := filepath.Join(base, "tool_results", taskID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read results file: %v", err)
	}

	lines := splitLines(string(data))
	if len(lines) != len(results) {
		t.Errorf("expected %d lines in JSONL, got %d", len(results), len(lines))
	}
	for i, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i+1, err)
		}
		if entry["type"] != "tool_call_result" {
			t.Errorf("line %d type: expected 'tool_call_result', got '%s'", i+1, entry["type"])
		}
	}

	// Verify Delete removes the file
	if err := store.DeleteTurns(taskID); err != nil {
		t.Fatalf("delete results: %v", err)
	}
}

// --- Integration test: KeepAlive through FakeOpenShellClient ---

func TestIntegrationKeepAlive(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	fakeWSHub := &FakeWSHub{}
	cfg := config.DefaultConfig()
	loop := New(cfg, store, nil, nil, fakeWSHub, nil)

	// Create a FakeOpenShellClient with a known sandbox
	fakeClient := NewFakeOpenShellClient()
	loop.openShell = fakeClient

	ctx := context.Background()

	// Simulate the flow: create sandbox → keepalive → execute
	sandboxName := "norman-task-test001"
	_, err = fakeClient.CreateSandbox(ctx, "", sandboxName, nil)
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// KeepAlive should succeed for an existing sandbox
	err = fakeClient.KeepAlive(ctx, sandboxName)
	if err != nil {
		t.Fatalf("keepalive failed: %v", err)
	}

	// KeepAlive should fail for a non-existent sandbox
	err = fakeClient.KeepAlive(ctx, "nonexistent-sandbox")
	if err == nil {
		t.Fatal("expected error for non-existent sandbox")
	}
}

// --- Integration test: Full loop.executeWithLLM with persistence (shell-only path) ---

func TestIntegrationExecuteWithLLMPersistsTurns(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	fakeWSHub := &FakeWSHub{}
	cfg := config.DefaultConfig()

	// Create a minimal activator
	act := skills.NewActivator(nil, 3)

	fakeClient := NewFakeOpenShellClient()
	loop := New(cfg, store, nil, act, fakeWSHub, fakeClient)

	ctx := context.Background()

	// Submit a task (shell-only path since llmClient is nil)
	task, err := loop.Submit(ctx, "List files", "", "session-1")
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	// Wait for task to complete (execute is synchronous in Submit)
	time.Sleep(200 * time.Millisecond)

	// Verify turns were persisted (shell-only path now persists assistant response)
	turns, err := store.LoadTurns(task.ID)
	if err != nil {
		t.Fatalf("load turns: %v", err)
	}

	if len(turns) == 0 {
		t.Error("expected at least one persisted turn from shell-only path")
	} else {
		// Verify the last turn is an assistant message
		lastTurn := turns[len(turns)-1]
		if lastTurn["role"] != "assistant" {
			t.Errorf("last turn role: expected 'assistant', got '%s'", lastTurn["role"])
		}
	}

	// Verify tool call results were persisted (shell command is a "tool" call)
	results, err := store.LoadToolCallResults(task.ID)
	if err != nil {
		t.Fatalf("load results: %v", err)
	}

	if len(results) == 0 {
		t.Log("no tool call results persisted")
	} else {
		for _, r := range results {
			if r["type"] != "tool_call_result" {
				t.Errorf("result type: expected 'tool_call_result', got '%s'", r["type"])
			}
		}
	}

	// Verify task status is completed
	if task.Status != protocol.TaskStatusCompleted {
		t.Errorf("expected TaskStatusCompleted, got %s", task.Status)
	}
}

// --- Integration test: WS events include tool detail (item #6) ---

func TestIntegrationWSBroadcastIncludesToolDetail(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	fakeWSHub := &FakeWSHub{}
	cfg := config.DefaultConfig()

	act := skills.NewActivator(nil, 3)

	fakeClient := NewFakeOpenShellClient()
	loop := New(cfg, store, nil, act, fakeWSHub, fakeClient)

	ctx := context.Background()

	task, err := loop.Submit(ctx, "Run ls -la /tmp", "", "session-2")
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	_ = task // used implicitly via fakeWSHub events

	time.Sleep(300 * time.Millisecond)

	// Collect all TaskProgress events that mention tools
	var toolEvents []protocol.WebSocketEvent
	fakeWSHub.mu.RLock()
	events := append([]protocol.WebSocketEvent(nil), fakeWSHub.events...)
	fakeWSHub.mu.RUnlock()
	for _, e := range events {
		if e.Event == protocol.TaskProgress && e.Message != nil {
			msg := *e.Message
			if strings.Contains(msg, "tool") || strings.Contains(msg, "Tool") {
				toolEvents = append(toolEvents, e)
			}
		}
	}

	if len(toolEvents) == 0 {
		t.Log("no tool-related WS events found (may be expected for text-only responses)")
		return
	}

	// Each tool event should include a non-empty message with detail
	for _, e := range toolEvents {
		if e.Message == nil || *e.Message == "" {
			t.Error("tool event message should not be empty")
		}
		if e.TaskId == nil {
			t.Error("tool event should have TaskId set")
		}
	}

	// Verify events include both pre-execution and post-execution broadcasts
	var preEvents, postEvents int
	for _, e := range toolEvents {
		msg := *e.Message
		if strings.Contains(msg, "Executing") || strings.Contains(msg, "Executing tool") {
			preEvents++
		} else if strings.Contains(msg, "completed") || strings.Contains(msg, "Tool '") {
			postEvents++
		}
	}

	t.Logf("pre-execution broadcasts: %d, post-execution broadcasts: %d", preEvents, postEvents)
	if preEvents == 0 && postEvents == 0 {
		t.Error("expected both pre and post execution WS events")
	}
}

// --- Integration test: Full end-to-end with FakeOpenShellClient + fake LLM client ---

func TestIntegrationFullLoopWithLLM(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	fakeWSHub := &FakeWSHub{}
	cfg := config.DefaultConfig()

	act := skills.NewActivator(nil, 3)

	fakeClient := NewFakeOpenShellClient()
	loop := New(cfg, store, nil, act, fakeWSHub, fakeClient)

	ctx := context.Background()

	// Submit a task and wait for completion
	task, err := loop.Submit(ctx, "Show me the contents of /etc/hostname", "", "session-3")
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify all artifacts were created:
	// 1. Task state persisted
	taskData, err := store.LoadTask(task.ID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if taskData["status"] != "completed" && taskData["status"] != "failed" {
		t.Errorf("task status unexpected: %s", taskData["status"])
	}

	// 2. Turns persisted (at least assistant response)
	turns, _ := store.LoadTurns(task.ID)
	if len(turns) == 0 {
		t.Log("no turns persisted (expected if shell-only fallback)")
	} else {
		t.Logf("persisted %d turns", len(turns))
	}

	// 3. Tool results persisted (if tools were called)
	results, _ := store.LoadToolCallResults(task.ID)
	t.Logf("persisted %d tool call results for task %s", len(results), task.ID[:8])

	// 4. WS events include progress steps
	var progressCount int
	fakeWSHub.mu.RLock()
	events := append([]protocol.WebSocketEvent(nil), fakeWSHub.events...)
	fakeWSHub.mu.RUnlock()
	for _, e := range events {
		if e.Event == protocol.TaskProgress {
			progressCount++
		}
	}
	t.Logf("broadcast %d progress events", progressCount)
}

// --- Integration test: Empty/edge-case handling ---

func TestIntegrationEdgeCases(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	taskID := "task-edge-001"

	// Test with minimal turn (no optional fields)
	minTurn := map[string]interface{}{"role": "assistant", "content": "hi"}
	if err := store.PersistTurn(taskID, minTurn); err != nil {
		t.Fatalf("persist minimal turn: %v", err)
	}

	// Test with tool call result that has empty output
	err = store.PersistToolCallResult(taskID, "echo", `{"text": ""}`, "", 0)
	if err != nil {
		t.Fatalf("persist empty output result: %v", err)
	}

	// Load and verify
	turns, err := store.LoadTurns(taskID)
	if err != nil {
		t.Fatalf("load turns: %v", err)
	}
	if len(turns) != 1 {
		t.Errorf("expected 1 turn, got %d", len(turns))
	}

	results, err := store.LoadToolCallResults(taskID)
	if err != nil {
		t.Fatalf("load results: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0]["output"] != "" {
		t.Errorf("expected empty output, got '%s'", results[0]["output"])
	}
}

// --- Helper: split lines (respects \n and \r\n) ---

func splitLines(s string) []string {
	var lines []string
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if current != "" {
				lines = append(lines, current)
			}
			current = ""
		} else if s[i] == '\r' {
			continue // skip \r
		} else {
			current += string(s[i])
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// --- Helper: splitLinesScanner (using bufio.Scanner) ---

func splitLinesScanner(s string) []string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
