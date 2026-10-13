// Package scheduler provides cron-based task scheduling for Norman.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// TaskEntry represents a scheduled task to be executed.
type TaskEntry struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Schedule    string    `json:"schedule"` // cron expression (5 or 6 field)
	Command     string    `json:"command"`  // shell command to execute
	OutputHandler func(result string, err error) `json:"-"` // callback for results
	NextRun     time.Time `json:"next_run"`
	LastRun     time.Time `json:"last_run,omitempty"`
	Active      bool      `json:"active"`
}

// TaskResult holds the outcome of a task execution.
type TaskResult struct {
	TaskID    string
	Name      string
	Output    string
	Err       error
	Timestamp time.Time
}

// Scheduler manages registered tasks and dispatches them on schedule.
type Scheduler struct {
	mu      sync.RWMutex
	cron    *cron.Cron
	tasks   map[string]*TaskEntry // ID → entry
	done    chan struct{}
	results chan TaskResult
}

// NewScheduler creates a new scheduler with the given output channel.
func NewScheduler(results chan TaskResult) *Scheduler {
	return &Scheduler{
		cron:    cron.New(cron.WithSeconds()), // 6-field (second minute hour day month weekday)
		tasks:   make(map[string]*TaskEntry),
		done:    make(chan struct{}),
		results: results,
	}
}

// Add registers a new task and starts its schedule.
func (s *Scheduler) Add(entry TaskEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[entry.ID]; exists {
		return fmt.Errorf("task %q already registered", entry.ID)
	}

	entry.Active = true
	entry.NextRun = time.Now().Add(1 * time.Minute) // initial next-run estimate

	s.tasks[entry.ID] = &entry
	return nil
}

// Remove unregisters a task and cancels its schedule.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task %q not found", id)
	}

	entry.Active = false
	delete(s.tasks, id)
	return nil
}

// Get returns a task by ID.
func (s *Scheduler) Get(id string) (*TaskEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %q not found", id)
	}
	return entry, nil
}

// List returns all registered tasks.
func (s *Scheduler) List() []*TaskEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*TaskEntry, 0, len(s.tasks))
	for _, entry := range s.tasks {
		result = append(result, entry)
	}
	return result
}

// Start begins the scheduler's background tick loop.
func (s *Scheduler) Start(ctx context.Context) {
	s.cron.Start()
	go s.tickLoop(ctx)
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	close(s.done)
	s.cron.Stop()
}

// tickLoop dispatches tasks on their schedule and pushes results.
func (s *Scheduler) tickLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			for id, entry := range s.tasks {
				if !entry.Active {
					continue
				}
				now := time.Now()
				if now.Before(entry.NextRun) {
					continue
				}

				entry.LastRun = now
				entry.NextRun = now.Add(1 * time.Minute) // approximate; cron handles precise scheduling

				s.mu.RUnlock()

				result := s.executeTask(id, entry.Command)
				s.results <- result

				s.mu.Lock()
			}
			s.mu.RUnlock()
		}
	}
}

// executeTask runs a command and returns the result.
func (s *Scheduler) executeTask(id string, command string) TaskResult {
	start := time.Now()
	output := ""
	var err error

	// In MVP, execute via exec.Command (will be extended to OpenShell Exec())
	cmd := []string{"bash", "-c", command}
	resultChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		output, err = runCommand(cmd)
		resultChan <- output
		errChan <- err
	}()

	select {
	case out := <-resultChan:
		output = out
	case e := <-errChan:
		if e != nil {
			err = e
		}
	}

	return TaskResult{
		TaskID:    id,
		Name:      "", // caller fills this
		Output:    output,
		Err:       err,
		Timestamp: start,
	}
}

// runCommand executes a shell command and returns combined output.
func runCommand(cmd []string) (string, error) {
	// Simplified exec for MVP; will be replaced with OpenShell Exec()
	return "", fmt.Errorf("exec not yet connected to OpenShell")
}
