package memory

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewRawSources(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}
	if rs == nil {
		t.Fatal("expected non-nil RawSources")
	}
	if rs.base != base {
		t.Errorf("expected base=%s, got %s", base, rs.base)
	}
}

func TestSnapshot(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	source := Source{
		ID:        "test-src-001",
		Name:      "Environment Snapshot",
		Category:  "env",
		Content:   `{"HOME": "/home/user", "PATH": "/usr/bin"}`,
		CreatedAt: time.Now(),
	}

	saved, err := rs.Snapshot(source)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	// Verify count
	if rs.Count() != 1 {
		t.Errorf("expected 1 source, got %d", rs.Count())
	}

	// Verify query
	got, err := rs.Query("test-src-001")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if got.ID != "test-src-001" {
		t.Errorf("expected ID test-src-001, got %s", got.ID)
	}
	if got.Category != "env" {
		t.Errorf("expected category env, got %s", got.Category)
	}

	// Verify returned source matches saved data
	if saved.ID != "test-src-001" {
		t.Errorf("returned ID mismatch: expected test-src-001, got %s", saved.ID)
	}
}

func TestSnapshotAutoID(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	// Source without ID should get auto-assigned
	source := Source{
		Name:      "Auto-ID Test",
		Category:  "config",
		Content:   `{"key": "value"}`,
		CreatedAt: time.Now(),
	}

	saved, err := rs.Snapshot(source)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	if saved.ID == "" {
		t.Error("expected auto-assigned ID")
	}
	if !strings.HasPrefix(saved.ID, "src-") {
		t.Errorf("expected ID prefixed with 'src-', got %s", saved.ID)
	}
}

func TestSnapshotDefaultCategory(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	source := Source{
		Name:      "Default Category",
		Content:   "test content",
		CreatedAt: time.Now(),
	}

	saved, err := rs.Snapshot(source)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	got, err := rs.Query(saved.ID)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if got.Category != "general" {
		t.Errorf("expected default category 'general', got %s", got.Category)
	}
}

func TestQueryNotFound(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	_, err = rs.Query("nonexistent-id")
	if err == nil {
		t.Error("expected error for non-existent source")
	}
}

func TestListAll(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	now := time.Now()

	snapshots := []Source{
		{ID: "src-1", Name: "First", Category: "env", Content: "a", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "src-2", Name: "Second", Category: "config", Content: "b", CreatedAt: now.Add(-1 * time.Minute)},
		{ID: "src-3", Name: "Third", Category: "env", Content: "c", CreatedAt: now},
	}

	for _, s := range snapshots {
		if _, err := rs.Snapshot(s); err != nil {
			t.Fatalf("snapshot %s failed: %v", s.ID, err)
		}
	}

	all, err := rs.ListAll()
	if err != nil {
		t.Fatalf("list all failed: %v", err)
	}

	if len(all) != 3 {
		t.Errorf("expected 3 sources, got %d", len(all))
	}

	// Verify sorted by CreatedAt descending (most recent first)
	if all[0].ID != "src-3" || all[2].ID != "src-1" {
		t.Errorf("expected descending order, got [src-3, src-2, src-1]")
	}
}

func TestQueryByCategory(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	snapshots := []Source{
		{ID: "src-1", Name: "Env1", Category: "env", Content: "a", CreatedAt: time.Now()},
		{ID: "src-2", Name: "Config1", Category: "config", Content: "b", CreatedAt: time.Now()},
		{ID: "src-3", Name: "Env2", Category: "env", Content: "c", CreatedAt: time.Now()},
	}

	for _, s := range snapshots {
		if _, err := rs.Snapshot(s); err != nil {
			t.Fatalf("snapshot %s failed: %v", s.ID, err)
		}
	}

	envSources, err := rs.QueryByCategory("env")
	if err != nil {
		t.Fatalf("query by category failed: %v", err)
	}

	if len(envSources) != 2 {
		t.Errorf("expected 2 env sources, got %d", len(envSources))
	}

	configSources, err := rs.QueryByCategory("config")
	if err != nil {
		t.Fatalf("query by category failed: %v", err)
	}

	if len(configSources) != 1 {
		t.Errorf("expected 1 config source, got %d", len(configSources))
	}
}

func TestSourceIsExpired(t *testing.T) {
	now := time.Now()

	// No TTL = never expired
	s1 := Source{CreatedAt: now.Add(-5 * time.Minute)}
	if s1.IsExpired() {
		t.Error("expected not expired with no TTL")
	}

	// Zero TTL = never expired
	s2 := Source{CreatedAt: now.Add(-5 * time.Minute), TTL: 0}
	if s2.IsExpired() {
		t.Error("expected not expired with zero TTL")
	}

	// Expired source
	s3 := Source{CreatedAt: now.Add(-10 * time.Minute), TTL: 5 * time.Minute}
	if !s3.IsExpired() {
		t.Error("expected expired source")
	}

	// Not yet expired
	s4 := Source{CreatedAt: now.Add(-2 * time.Minute), TTL: 5 * time.Minute}
	if s4.IsExpired() {
		t.Error("expected not expired (within TTL)")
	}
}

func TestSnapshotAtomicWrite(t *testing.T) {
	base := t.TempDir()
	rs, err := NewRawSources(base)
	if err != nil {
		t.Fatalf("new raw sources: %v", err)
	}

	source := Source{
		ID:        "atomic-test",
		Name:      "Atomic Test",
		Category:  "test",
		Content:   `{"test": true}`,
		CreatedAt: time.Now(),
	}

	if _, err := rs.Snapshot(source); err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	// Verify file exists and is valid JSON
	path := base + "/sources/atomic-test.json"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source file: %v", err)
	}

	// Verify no .tmp file remains
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf(".tmp file should not exist after rename, found at %s", tmpPath)
	}

	// Verify JSON validity
	var got Source
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if got.ID != "atomic-test" {
		t.Errorf("expected ID atomic-test, got %s", got.ID)
	}
}
