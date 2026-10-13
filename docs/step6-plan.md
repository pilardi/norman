# Step 6 — Memory Engine (LTM Wiki) + Git Versioning

> **Status:** 📋 PLAN ONLY — awaiting approval before implementation.

---

## Problem Statement

We have `pkg/memory/store.go` (task/session persistence), but no **wiki engine** that implements the rules defined in `AGENTS.md`. The wiki is the agent's long-term memory: it stores reusable knowledge entries, tracks staleness, and needs durable history via git versioning.

---

## Proposed Architecture

### 1. New Package: `pkg/wiki`

| File | Purpose |
|------|---------|
| `entry.go` | `WikiEntry` struct + frontmatter parsing (YAML), classification helpers |
| `engine.go` | Core engine: Create/Update/Delete/List entries, apply decay/staleness rules |
| `index.go` | Catalog/index maintenance (`index.md`) — rebuilds table of contents |
| `log.go` | Append-only journal (`log.md`) for INGEST/UPDATE/QUERY/PRUNE events |

### 2. Data Model (matching AGENTS.md)

```go
type EntryType string // entity | source | trend | task

type WikiEntry struct {
    Title     string   `yaml:"title"`
    Type      EntryType `yaml:"type"`
    Tags      []string `yaml:"tags,omitempty"`
    Pinned    bool     `yaml:"pinned,omitempty"`
    Updated   time.Time `yaml:"updated"`
    CreatedAt time.Time `yaml:"created_at"`
    Content   string   // markdown body
}
```

### 3. Git Versioning Strategy

- On every **Create/Update/Delete**, run `git add memory/<entry>.md && git commit -m "wiki: <action> <title>"`
- Use a lightweight commit message format for easy log viewing: `wiki: create project-alpha`, `wiki: update API gateway`, `wiki: prune task/docker-build-opt`
- Optionally batch commits (N entries → 1 commit) to avoid flooding git history during bulk ingestion.

### 4. Integration Points

| Where | How |
|-------|-----|
| Agent loop (`pkg/agent/loop.go`) | `Engine.CreateEntry(ctx, entry)` called after task completion for reusable insights |
| CLI (`berdctl`) | `wiki create/update/list` commands (future) |
| Memory store (`pkg/memory/store.go`) | Keep existing task/session persistence; wiki engine is a *new* concern |

---

## Sub-steps (if approved, implement in this order)

| # | Task | Estimated Effort |
|---|------|-----------------|
| 6a | `pkg/wiki/entry.go` — struct + frontmatter parser + classification helpers | ~2h |
| 6b | `pkg/wiki/engine.go` — Create/Update/Delete/List with decay logic | ~3h |
| 6c | `pkg/wiki/index.go` — catalog rebuild on write | ~1h |
| 6d | `pkg/wiki/log.go` — append-only journal | ~0.5h |
| 6e | Git versioning hook + integration into agent loop | ~2h |

**Total estimated effort: ~8.5h (1 day)**

---

## Acceptance Criteria

- [ ] `make test` passes on `pkg/wiki/...` package
- [ ] `Engine.CreateEntry()` parses frontmatter, classifies type, writes to `memory/<slug>.md`
- [ ] `Engine.ListEntries(filter)` returns entries matching type/tags
- [ ] Decay logic flags `type: trend` entries >30 days old as stale
- [ ] Prune removes `type: task` entries >60 days unless tagged
- [ ] Every write triggers a git commit to the repo root
- [ ] `index.md` is rebuilt after each batch of writes
- [ ] Log entries appended to `memory/log.md` in `[YYYY-MM-DD HH:MM] TYPE: description` format

---

## Risks & Trade-offs

| Risk | Mitigation |
|------|------------|
| Git commits on every write could be noisy | Batch mode: collect writes, commit once per agent turn |
| Memory entries are markdown — parsing can be fragile | Use `gopkg.in/yaml.v3` for frontmatter; validate required fields |
| Concurrent writes from multiple agents/sessions | File-level locking (FLOCK) in engine; optimistic retry on conflict |

---

## What's NOT in scope (deferred)

- Real-time sync with external wiki tools (Notion, Obsidian, etc.)
- Semantic search / vector embeddings over entries
- Multi-agent collaboration conflict resolution beyond file locking
