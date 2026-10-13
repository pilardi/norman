package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestNewScheduler(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)
	if s == nil {
		t.Fatal("NewScheduler returned nil")
	}
}

func TestAddAndGet(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	entry := TaskEntry{
		ID:      "test-1",
		Name:    "health-check",
		Schedule: "* * * * *",
		Command: "echo ok",
	}

	if err := s.Add(entry); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	got, err := s.Get("test-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != "test-1" {
		t.Errorf("expected ID test-1, got %s", got.ID)
	}
}

func TestAddDuplicate(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	entry := TaskEntry{ID: "dup-1", Name: "test", Schedule: "* * * * *", Command: "echo ok"}
	if err := s.Add(entry); err != nil {
		t.Fatalf("first Add failed: %v", err)
	}
	if err := s.Add(entry); err == nil {
		t.Error("expected error on duplicate Add")
	}
}

func TestRemove(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	entry := TaskEntry{ID: "remove-1", Name: "test", Schedule: "* * * * *", Command: "echo ok"}
	if err := s.Add(entry); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if err := s.Remove("remove-1"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	_, err := s.Get("remove-1")
	if err == nil {
		t.Error("expected error after Remove")
	}
}

func TestList(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	s.Add(TaskEntry{ID: "list-1", Name: "a", Schedule: "* * * * *", Command: "echo a"})
	s.Add(TaskEntry{ID: "list-2", Name: "b", Schedule: "* * * * *", Command: "echo b"})

	list := s.List()
	if len(list) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(list))
	}
}

func TestStartStop(t *testing.T) {
	results := make(chan TaskResult, 10)
	s := NewScheduler(results)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s.Start(ctx)
	s.Stop()
}
