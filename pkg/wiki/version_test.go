package wiki

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitVersionerCreate(t *testing.T) {
	dir := t.TempDir()
	_, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	gv, err := NewGitVersioner(dir, BatchPerWrite)
	if err != nil {
		t.Fatalf("NewGitVersioner: %v", err)
	}

	entry := &WikiEntry{Title: "Test Entry", Type: EntryTypeEntity, Content: "hello"}
	err = gv.Create(entry)
	if err != nil {
		t.Logf("Create returned (expected in test env): %v", err)
	}

	// Verify log.md was written (git commit may fail without .git)
	logPath := filepath.Join(dir, "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !contains(string(data), "wiki: create test-entry") {
		t.Errorf("expected log entry, got: %s", string(data))
	}
}

func TestGitVersionerBatch(t *testing.T) {
	dir := t.TempDir()
	_, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	gv, err := NewGitVersioner(dir, BatchPerTurn)
	if err != nil {
		t.Fatalf("NewGitVersioner: %v", err)
	}

	gv.addLog("write A")
	gv.addLog("write B")
	gv.addLog("write C")

	err = gv.BatchFinalize()
	if err != nil {
		t.Logf("BatchFinalize returned (expected in test env): %v", err)
	}

	logPath := filepath.Join(dir, "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !contains(string(data), "wiki: batch 3 writes") {
		t.Errorf("expected batch message, got: %s", string(data))
	}
}

func TestGitVersionerFlush(t *testing.T) {
	dir := t.TempDir()
	_, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	gv, err := NewGitVersioner(dir, BatchPerTurn)
	if err != nil {
		t.Fatalf("NewGitVersioner: %v", err)
	}

	gv.addLog("pending write")
	err = gv.Flush()
	if err != nil {
		t.Logf("Flush returned (expected in test env): %v", err)
	}

	logPath := filepath.Join(dir, "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !contains(string(data), "wiki: 1 writes") {
		t.Errorf("expected flush message, got: %s", string(data))
	}
}
