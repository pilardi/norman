# Step 7 — Context Budget & Compression Cycle

> **Status:** 📋 PLAN ONLY — awaiting approval before implementation.

---

## Problem Statement

The agent's conversation context grows unbounded as tasks execute, skills activate, and tool calls accumulate. Without a budget mechanism, the LLM prompt exceeds token limits or becomes too expensive to run efficiently. We need a **token-aware compression cycle** that summarizes older turns while preserving critical state (pinned entries, workspace config, active plan steps).

---

## Current State

- `pkg/agent/loop.go` — core agent loop with task execution, no budget tracking.
- `pkg/memory/store.go` — flat file persistence for tasks/sessions; no session archival.
- `pkg/wiki/` — wiki engine (Step 6) provides the LTM storage layer; could serve as compression archive target.
- No existing token counting or summarization infrastructure.

---

## Proposed Architecture

### 1. New Package: `pkg/budget`

| File | Purpose |
|------|---------|
| `budget.go` | Token budget tracker + threshold callbacks (75%, 90%) |
| `compressor.go` | Summarization engine: selects turns to compress, preserves pinned/selective entries |
| `archive.go` | Session archival to `.norman/memory/sessions/` — stores full transcripts before compression |

### 2. Data Model

```go
type BudgetConfig struct {
    MaxTokens   int       // total budget (e.g., 128000 for GPT-4)
    WarnThreshold float64 // 0.75 → trigger at 75%
    CriticalThreshold float64 // 0.90 → trigger at 90%
    RetainOriginal bool   // keep full transcript in archive before summarizing
}

type Turn struct {
    Role      string // "user", "assistant", "system"
    Content   string
    Tokens    int    // estimated token count for this turn
    Pinned    bool   // if true, never compressed out
}

type SessionState struct {
    Turns     []Turn
    Budget    BudgetConfig
    Compressed bool  // flag: has a compression cycle run?
    ArchivedAt string // timestamp of last archival
}
```

### 3. Integration Points

| Component | Change |
|-----------|--------|
| `pkg/agent/loop.go` | Inject `*budget.SessionState` into `Loop`; call `Budget.Check()` after each tool response + LLM call; trigger `Compressor.Summarize()` at threshold |
| `pkg/memory/store.go` | Add `ArchiveSession(taskID string, turns []Turn) error` method — writes full transcript to `.norman/memory/sessions/<taskID>.jsonl` |
| `pkg/wiki/entry.go` | Optional: mark compressed sessions as `type: task` with `stale: true` until re-explored |

### 4. Compression Strategy

```
At 75% budget used:
  1. Archive full transcript → memory/sessions/<taskID>.jsonl (atomic write)
  2. Select oldest N turns for summarization (skip pinned + active workspace config)
  3. Replace selected turns with a single "compressed summary" turn
  4. Update token count in budget tracker

At 90% budget used (forced):
  1. Same as 75%, but compress MORE aggressively (keep only task prompt, pinned entries, current plan)
  2. Mark session as `Compressed: true` — no further compression until next turn
```

**Selective retention rules:**
- ✅ Original user request (first turn)
- ✅ Pinned entries (`pinned: true` in frontmatter)
- ✅ Active workspace config
- ✅ Current plan steps (from project.md or active task context)
- ❌ Tool call details (summarized to "executed X tool, result: ...")
- ❌ LLM internal reasoning (if available)

### 5. Token Estimation

Two approaches (configurable):
1. **API-provided**: Use `usage.token_count` from LLM response metadata (most accurate).
2. **Estimate fallback**: Use `github.com/pkoukk/tiktoken-go` to count tokens per turn if API doesn't expose it.

Config:
```yaml
budget:
  max_tokens: 128000
  warn_at: 0.75
  critical_at: 0.90
  estimation: "tiktoken"  # or "api"
```

---

## Implementation Steps (sub-steps)

| Sub-step | Description | Risk | Status |
|----------|-------------|------|--------|
| **7a** | Create `pkg/budget` package skeleton + `BudgetConfig`, `Turn`, `SessionState` types. Add unit tests for token counting logic. | Low — pure data model | ✅ COMPLETE |
| **7b** | Implement `Compressor.Summarize()` — turn selection, pinned retention, summary generation (calls LLM with a compression prompt). Tests with mock turns. | Medium — requires LLM integration | ✅ COMPLETE |
| **7c** | Wire budget thresholds into `Loop.execute()` — inject `Check()` calls, trigger archive + compress at 75%/90%. Add session archival to `memory/store.go`. | Medium — integration point | ✅ COMPLETE |
| **7c** | Wire budget thresholds into `Loop.execute()` — inject `Check()` calls, trigger archive + compress at 75%/90%. Add session archival to `memory/store.go`. | Medium — integration point |
| **7d** | Add CLI command `norman sessions archive` for manual archival; add WebSocket event `session_compressed` for real-time notification. | Low — thin wrapper |

---

## Acceptance Criteria

- [ ] Running a long task (simulated 200+ turns) triggers automatic compression at ~75% budget.
- [ ] Full transcript is archived to `.norman/memory/sessions/<taskID>.jsonl` before summarization.
- [ ] Pinned entries and original request are preserved post-compression.
- [ ] `git log -- .norman/memory/sessions/` shows archival history.
- [ ] `norman sessions archive` CLI command works for manual archival.
- [ ] WebSocket client receives `session_compressed` event notification.

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Compression loses critical context | Conservative retention: always keep original prompt + pinned entries; summarize tool calls but not their results |
| LLM summarization adds latency | Async compression — trigger in background goroutine; don't block task execution |
| Token estimation inaccuracy | Provide both `api` and `tiktoken` modes; log warnings when estimates differ >10% from API-reported |
| Archive file bloat | Compress archived JSONL files (gzip); prune archives older than 7 days unless tagged |

---

## Dependencies on Other Steps

- **Step 6** (wiki engine): Compression targets could optionally create wiki entries for compressed sessions (`type: task`, `tags: [compressed]`).
- **Step 8** (MCP + cron): Session archival could be scheduled via cron (`norman sessions cleanup --retain 7d`).
- **Step 5b** (agent loop): Budget checks must integrate cleanly into `Loop.execute()` without disrupting task flow.

---

*This is a plan draft. Actual implementation will follow approved sub-steps.*
