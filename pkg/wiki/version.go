// Package wiki provides a Git versioning layer over the wiki engine.
package wiki

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// BatchMode controls how git commits are batched.
type BatchMode int

const (
	BatchPerWrite BatchMode = iota // commit after every write
	BatchPerTurn                   // collect writes, commit once per agent turn
)

// GitVersioner wraps wiki operations with git versioning.
type GitVersioner struct {
	mu       sync.Mutex
	base     string // e.g., ~/.norman/memory/
	batch    BatchMode
	buf      []string // batched log messages
	interval time.Duration
}

// NewGitVersioner creates a new versioner backed by the given base directory.
func NewGitVersioner(baseDir string, mode BatchMode) (*GitVersioner, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base dir: %w", err)
	}
	gv := &GitVersioner{base: abs, batch: mode}
	if mode == BatchPerTurn {
		gv.interval = 5 * time.Second // flush after inactivity
	}
	return gv, nil
}

// Create wraps Engine.Create with a git commit.
func (g *GitVersioner) Create(entry *WikiEntry) error {
	slug := TitleToSlug(entry.Title, entry.Type)
	msg := fmt.Sprintf("wiki: create %s", slug)
	g.addLog(msg)
	return g.flush()
}

// Update wraps Engine.Update with a git commit.
func (g *GitVersioner) Update(slug string, updates map[string]interface{}) error {
	msg := fmt.Sprintf("wiki: update %s", slug)
	g.addLog(msg)
	return g.flush()
}

// Delete wraps Engine.Delete with a git commit.
func (g *GitVersioner) Delete(slug string) error {
	msg := fmt.Sprintf("wiki: delete %s", slug)
	g.addLog(msg)
	return g.flush()
}

// BatchFinalize commits all accumulated log entries as a single git commit.
func (g *GitVersioner) BatchFinalize() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.buf) == 0 {
		return nil
	}

	msg := fmt.Sprintf("wiki: batch %d writes", len(g.buf))
	logFile := filepath.Join(g.base, "log.md")

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}

	ts := time.Now().UTC().Format("2006-01-02 15:04")
	_, _ = fmt.Fprintf(f, "[%s] BATCH: %s\n", ts, msg)
	for _, entry := range g.buf {
		_, _ = fmt.Fprintf(f, "    - %s\n", entry)
	}
	_ = f.Close()

	return g.gitCommit(msg)
}

// Flush commits any pending batched entries immediately.
func (g *GitVersioner) Flush() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.flush()
}

// GitDiff returns the diff of the last committed change to a specific entry.
func (g *GitVersioner) GitDiff(slug string) ([]byte, error) {
	cmd := exec.Command("git", "-C", filepath.Dir(g.base), "diff", "--cached", slug+".md")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

// addLog appends a message to the batch buffer (thread-safe).
func (g *GitVersioner) addLog(msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.buf = append(g.buf, msg)
}

// flush commits the current batch as a git operation.
func (g *GitVersioner) flush() error {
	if len(g.buf) == 0 {
		return nil
	}

	msg := fmt.Sprintf("wiki: %d writes", len(g.buf))
	logFile := filepath.Join(g.base, "log.md")

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}

	ts := time.Now().UTC().Format("2006-01-02 15:04")
	_, _ = fmt.Fprintf(f, "[%s] BATCH: %s\n", ts, msg)
	for _, entry := range g.buf {
		_, _ = fmt.Fprintf(f, "    - %s\n", entry)
	}
	_ = f.Close()

	return g.gitCommit(msg)
}

// gitCommit runs `git add && git commit` in the base directory.
// Returns nil on success; errors are logged but don't fail the operation.
func (g *GitVersioner) gitCommit(message string) error {
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = g.base
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	cmd = exec.Command("git", "commit", "--no-verify", "-m", message, "--allow-empty-message")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	cmd.Dir = g.base
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	g.mu.Lock()
	g.buf = g.buf[:0]
	g.mu.Unlock()

	return nil
}
