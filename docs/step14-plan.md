# Step 14 — Assessment Review & Remediation

> **Status:** ✅ COMPLETE (merged to `main`)

---

## Problem Statement

A comprehensive review of `requirements.md`, `glossary.md`, `docs/step*.md`, `docs/api-reference.html`, and the current codebase against the implementation revealed multiple issues: stale config paths, session format inconsistency, TitleToSlug collisions, missing schema.md support, plan persistence gaps, Raw Sources layer missing, budget threshold integration incomplete, workspace registry in-memory only, and skill discovery paths hardcoded. All 9 sub-steps (14a–14i) have been implemented and tested.

---

## Findings Summary (from 2026-10-15 review)

| # | Issue | Severity | Effort | Priority |
|---|-------|----------|--------|----------|
| 14a | Stale config path `~/.config/norman/config.yaml` in glossary.md and `docs/api-reference.html` | Low | Quick win | High | ✅ DONE |
| 14b | Session archive format inconsistency: `SaveTask` → `.json`, `ArchiveSession` → `.jsonl` | Medium | Small fix | Medium | ✅ DONE |
| 14c | TitleToSlug collision risk: two entries with same title but different types would collide on disk (e.g., "Docker" as entity vs task) | Low | Design decision | Low | ✅ DONE — type suffix appended to slug |
| 14d | `schema.md` read/write + git operations not implemented in wiki engine | Medium | Implementation | Medium | ✅ DONE — WriteSchema/ReadSchema added; GitDiff/GitCommit wired via exec.Command |
| 14e | Plan persistence missing from agent loop — only tracked in memory | Medium | Implementation | Medium | ✅ DONE — SavePlan/LoadPlan added; budget compression preserves plan state |
| 14f | Raw Sources layer as distinct abstraction over environment reads — not yet implemented | Low | Design | Low | ✅ DONE — pkg/memory/raw_sources.go implements read-only snapshot store; context injection ready |
| 14g | Budget threshold checks exist but not actively triggered in planning loop | Low | Integration | Low | ✅ DONE — threshold check at cycle start; notify path logging; idempotent compression |
| 14h | Workspace registry is in-memory only (no disk persistence) | Medium | Implementation | Low | ✅ DONE — workspaces persisted to disk; loaded on startup; deleted on removal |
| 14i | Skill discovery paths host-FS-bound; should become server-driven via config | Medium | Refactor | Low | ✅ DONE — AddDiscoveryPaths method on Loop; POST /api/skills/paths endpoint; config-driven paths |

---

## Sub-steps (detailed implementation notes)

### 14a ✅ DONE — Update stale docs config path references
- `docs/api-reference.html` §1: replaced `~/.config/norman/config.yaml` → `~/.norman/config.yaml`
- `glossary.md`: no stale references found (already clean)

### 14b ✅ DONE — Session format alignment
- `ArchiveSession` now stores `.json` (JSON array) instead of `.jsonl`
- Tests updated: verify JSON array semantics, overwrite on multiple calls
- Empty sessions produce valid empty JSON arrays

### 14c ✅ DONE — TitleToSlug collision mitigation
- `TitleToSlug(title, EntryType)` appends type as suffix (e.g., "Docker" + entity → "docker-entity")
- All callers updated: engine.go Create/PruneStale, version.go Create
- Collision test added verifying same-title/different-type produces distinct slugs

### 14d ✅ DONE — Schema.md read/write + git operations in wiki engine
- `WriteSchema(slug, content)` added to `pkg/wiki/engine.go` — writes schema.md with auto-generated frontmatter (title derived from slug)
- `ReadSchema(slug) -> string` accessor added — reads and returns markdown body after frontmatter
- `GitDiff(slug) ([]byte, error)` wired — runs `git diff --cached {slug}.md` via exec.Command
- `GitCommit(message string) error` wired — runs `git add . && git commit -m "message"` via exec.Command
- `ParseSchemaBody(markdown string) (string, error)` added to entry.go — extracts body without type validation
- Tests: WriteSchema, ReadSchema, ReadSchemaNotFound, GitDiff, GitCommit all pass

### 14e ✅ DONE — Plan persistence in agent loop
- `SavePlan(planID, content)` added to `pkg/agent/loop.go` — delegates to Store.SavePlan
- `LoadPlan(planID) -> string` added to `pkg/agent/loop.go` — delegates to Store.LoadPlan
- `DeletePlan(planID)` added for cleanup
- Plan persisted as `.json` in store directory (`plans/`)
- `SessionState.PlanContent` field added to budget tracker (survives compression)
- Budget compression flow now persists plan state before truncation
- `Store.Stats()` updated to include plan count

### 14f ✅ DONE — Raw Sources layer
- Design: distinct abstraction between environment reads (raw JSON snapshots) and wiki entries (structured markdown with frontmatter types)
- Implement `pkg/memory/raw_sources.go` as read-only snapshot store (`Snapshot()`, `Query()`, `ListAll()`, `QueryByCategory()`)
- Context injection ready: `RawSources` integrates into agent loop via `Loop.rawSources` field; supports category-based filtering and TTL expiry

### 14g ✅ DONE — Budget threshold integration into planning loop
- Check thresholds at start of each planning cycle (before skill resolution)
- Trigger summarization when `WarnThreshold` hit, not just on budget tracker
- Log threshold events to notification path via `pushThresholdNotification()`

### 14h ✅ DONE — Workspace registry disk persistence
- `Store.SaveWorkspace(name, data)` added — persists workspace as `.json` in `workspaces/` dir
- `Store.LoadWorkspace(name) -> map[string]interface{}` added — loads workspace by name
- `Store.DeleteWorkspace(name)` added — removes workspace file
- `Store.ListWorkspaces() -> []map[string]interface{}` added — lists all workspaces
- Server initialization: loads persisted workspaces from disk on startup (log.Printf)
- `CreateWorkspace`: saves workspace to disk after in-memory registration
- `DeleteWorkspace`: deletes workspace file from disk after in-memory removal
- Concurrent access handled via existing `sync.Mutex` on server struct
- `Store.Stats()` updated to include workspace count

### 14i ✅ DONE — Server-driven skill discovery paths
- `DiscoveryPaths` moved from hardcoded host-FS scan to config-driven list (`SkillsConfig.DiscoveryPaths`)
- Server reads paths at startup, caches in memory via `cfg.System.Skills.DiscoveryPaths`
- `POST /api/skills/paths` endpoint added — accepts JSON `{ "paths": [...] }`, validates non-empty strings, updates config; broadcasts WebSocket event on success
- `Loop.AddDiscoveryPaths(paths []string) error` method for server-driven path expansion at runtime

---

## Data Models

### Raw Sources (`pkg/memory/raw_sources.go`)
```go
type Source struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    Category  string    `json:"category"`
    Content   string    `json:"content"`
    CreatedAt time.Time `json:"created_at"`
    TTL       int       `json:"ttl,omitempty"` // seconds until expiry
}
```

### Budget Threshold Flags (`pkg/budget/budget.go`)
```go
type ThresholdFlags uint8
const (
    ThresholdNone     ThresholdFlags = 0
    ThresholdWarn     ThresholdFlags = 1 << iota // 75%
    ThresholdCritical                              // 90%
)
func (f ThresholdFlags) Is(flag ThresholdFlags) bool { return f&flag != 0 }
```

---

## Acceptance Criteria

- [ ] All 9 sub-steps (14a–14i) implemented and tested.
- [ ] `go test ./...` passes with zero failures.
- [ ] No stale config paths remain in documentation.
- [ ] Session archives use consistent `.json` format.
- [ ] TitleToSlug produces distinct slugs for same-title/different-type entries.
- [ ] Schema.md read/write + git operations work end-to-end.
- [ ] Plans survive budget compression cycles.
- [ ] Raw Sources layer provides read-only snapshots with TTL expiry.
- [ ] Budget thresholds trigger summarization at 75% and 90%.
- [ ] Workspaces persisted to disk, loaded on startup, deleted on removal.
- [ ] Skill discovery paths configurable via `cfg.System.Skills.DiscoveryPaths`.

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Raw Sources TTL expiry races with concurrent reads | Use `time.AfterFunc` for auto-expiry; atomic compare-and-swap on delete. |
| Budget threshold double-trigger (75% + 90%) | ThresholdFlags bitmask; idempotent `checkAndCompress()` skips re-compression if already compressed. |
| Workspace disk persistence conflicts during concurrent Create/Delete | Existing `sync.Mutex` on server struct handles race conditions. |
| Skill discovery paths include invalid directories | Validate non-empty, trim whitespace; log warning for inaccessible paths. |

---

## Dependencies on Other Steps

- **Step 6** (wiki engine): Schema.md support extends wiki engine capabilities.
- **Step 7** (budget/compression): Threshold integration into agent loop; plan survives compression.
- **Step 8** (notifications): `pushThresholdNotification()` uses notifier path for threshold event logging.
- **Step 9** (scheduler): Scheduler results channel injection complements notification flow.
- **Step 10** (server rename): Config paths unified under `~/.norman/`.

---

*This is a plan draft. Actual implementation follows approved sub-steps.*
