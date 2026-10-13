package wiki

import (
	"testing"
)

func TestJournalWrite(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/log.md"

	j, err := NewJournal(path)
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}

	err = j.Write(EventIngest, "created entry Alpha")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	lines, err := j.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !contains(lines[0], "INGEST") || !contains(lines[0], "created entry Alpha") {
		t.Errorf("unexpected log line: %s", lines[0])
	}
}

func TestJournalMultipleWrites(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/log.md"

	j, err := NewJournal(path)
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}

	j.Write(EventIngest, "entry A")
	j.Write(EventUpdate, "updated entry A")
	j.Write(EventPrune, "pruned 2 entries")

	lines, err := j.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestJournalTruncateLast(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/log.md"

	j, err := NewJournal(path)
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}

	j.Write(EventIngest, "entry A")
	j.Write(EventUpdate, "updated entry A")
	j.Write(EventPrune, "pruned 2 entries")

	err = j.TruncateLast(1)
	if err != nil {
		t.Fatalf("TruncateLast: %v", err)
	}

	lines, err := j.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines after truncate, got %d", len(lines))
	}
}

func TestJournalEmpty(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/log.md"

	j, err := NewJournal(path)
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}

	lines, err := j.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected empty, got %d lines", len(lines))
	}
}
