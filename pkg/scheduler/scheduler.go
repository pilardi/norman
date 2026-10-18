// Package scheduler provides cron-based task scheduling for Norman.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
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

// RunCommandFunc is a function type for executing commands.
type RunCommandFunc func(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error)

// Scheduler manages registered tasks and dispatches them on schedule.
type Scheduler struct {
	mu         sync.RWMutex
	cron       *cron.Cron
	tasks      map[string]*TaskEntry // ID → entry
	done       chan struct{}
	results    chan TaskResult
	runCommand RunCommandFunc        // OpenShell Exec() adapter (nil = fallback to local exec)
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

// WithRunCommand wires an OpenShell Exec() adapter into the scheduler.
func (s *Scheduler) WithRunCommand(fn RunCommandFunc) *Scheduler {
	s.runCommand = fn
	return s
}

// WithOutputHandler sets a global callback invoked after each task execution.
func (s *Scheduler) WithOutputHandler(fn func(result string, err error)) *Scheduler {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.tasks {
		entry.OutputHandler = fn
	}
	return s
}

// Status returns scheduler health metrics: registered task count and last tick time.
func (s *Scheduler) Status() (int, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := len(s.tasks)
	var lastTick time.Time
	for _, entry := range s.tasks {
		if entry.LastRun.After(lastTick) {
			lastTick = entry.LastRun
		}
	}
	return count, lastTick
}

// ToInterfaceSlice converts all registered tasks to []interface{} for persistence.
func (s *Scheduler) ToInterfaceSlice() []interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]interface{}, 0, len(s.tasks))
	for _, entry := range s.tasks {
		result = append(result, map[string]interface{}{
			"id":       entry.ID,
			"name":     entry.Name,
			"schedule": entry.Schedule,
			"command":  entry.Command,
			"active":   entry.Active,
		})
	}
	return result
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
			for _, entry := range s.tasks {
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

				result := s.executeTask(entry)
				s.results <- result

				s.mu.Lock()
			}
			s.mu.RUnlock()
		}
	}
}

// executeTask runs a task's command and returns the result.
func (s *Scheduler) executeTask(entry *TaskEntry) TaskResult {
	start := time.Now()
	output := ""
	var err error

	cmd := []string{"bash", "-c", entry.Command}

	if s.runCommand != nil {
		// Use OpenShell Exec() for sandbox execution
		result, execErr := s.runCommand(context.Background(), "default", "", cmd)
		if execErr != nil {
			err = fmt.Errorf("exec: %w", execErr)
		} else {
			output = string(result.Stdout)
			if result.ExitCode != 0 {
				err = fmt.Errorf("exit code %d", result.ExitCode)
			}
		}
	} else {
		// Fallback: local shell exec
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
	}

	result := TaskResult{
		TaskID:    entry.ID,
		Name:      entry.Name,
		Output:    output,
		Err:       err,
		Timestamp: start,
	}

	// Invoke OutputHandler callback if set.
	if entry.OutputHandler != nil {
		entry.OutputHandler(output, err)
	}

	return result
}

// runCommand executes a shell command locally and returns combined output.
func runCommand(cmd []string) (string, error) {
	// Fallback local exec for MVP; will be replaced with OpenShell Exec()
	return "", fmt.Errorf("exec not yet connected to OpenShell")
}
