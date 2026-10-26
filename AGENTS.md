# AGENTS.md — Memory Maintenance Rules (MVP v1.0)

> This file encodes procedural rules for how the agent maintains its wiki (`memory/`).
> It is read at session start and referenced during maintenance cycles.
> **Status:** MVP implemented — v1.0. Core engine, index, and journal are wired into the agent loop.
> **Related:** `docs/memory-proposal.md` (future nested-directory enhancement).

---

## 1. Entry Classification

All wiki entries are flat markdown files in `.norman/memory/` with frontmatter `type:` field:

| Type | Purpose | Example |
|------|---------|---------|
| `entity` | Named objects, projects, services | "Project Alpha", "API Gateway" |
| `source` | External references, docs, repos | "OpenAI API Docs v1.2" |
| `trend` | Patterns observed over time | "Increasing use of MCP connections" |
| `task` | Completed task outcomes with lessons learned | "Docker build optimization pattern" |

**Rule:** Never create subdirectories. Classification is via frontmatter, not filesystem layout.

---

## 2. Deduplication

- When a new entry would duplicate an existing one (same title or overlapping content), **update the existing entry** rather than creating a new page.
- Compare by `title` field first; if no match, compare content similarity (>70% overlap = merge).
- Keep only the most recent version; archive older versions in git history.
- **Workspace scoping**: Entries with different `workspace:` frontmatter values are considered separate entries even if titles match (e.g., "Docker" in workspace `api-migration` vs `data-pipeline` are distinct). Cross-workspace duplicates should NOT be merged.

**Implementation note:** The current engine (`pkg/wiki/engine.go`) enforces uniqueness by slug (title + type suffix). Content-similarity merging is a planned enhancement — for now, duplicate titles of the same type will fail on `Create`.

---

## 3. Decay & Staleness

- Entries with `type: trend` that haven't been updated in >30 days are flagged as `stale` and pruned by `PruneStale()`.
- Entries with `type: entity` or `source` are never auto-decayed (they're reference data).
- `task` entries older than 60 days are pruned **unless they have `tags`** indicating recurring relevance.
  - Age is measured from `CreatedAt`, not `Updated`.
- **Workspace-aware decay**: Planned but not yet implemented. A trend relevant to workspace `api-migration` should not be flagged stale just because it hasn't been updated in the context of workspace `data-pipeline`.

---

## 4. One-Off vs. Reusable

- **One-off observations** (e.g., "File X had a bug on Oct 12") → no wiki entry needed; keep in session transcript.
- **Reusable knowledge** (e.g., "Pattern: always check MCP connection health before long operations") → create/update wiki entry.
- Heuristic: if the insight would help a *different* task or a *future* session, it's reusable.
- **Workspace reusability**: Knowledge tagged with `workspace: <name>` is scoped to that workspace. A cross-workspace observation should be duplicated (not merged) when created in a new workspace context.

---

## 5. Index Maintenance

After any write to `memory/`, update `index.md`:
```yaml
---
title: "Content Catalog"
type: system
updated: 2026-10-26
---
# Content Catalog

| Page | Type | Tags | Updated |
|------|------|------|---------|
| project-alpha.md | entity | [api, backend] | 2026-10-15 |
| ... | ... | ... | ... |
```

`WriteIndex()` is called after `Create`, `Update`, and `Delete` operations.

---

## 6. Log Entries (log.md)

Append-only journal entries for:
- `INGEST`: new wiki entry created
- `UPDATE`: existing entry modified
- `QUERY`: retrieval event (what was searched, what matched)
- `PRUNE`: stale entries removed

Format: `[YYYY-MM-DD HH:MM] TYPE: description`

---

## 7. Pinned Entries

Entries explicitly pinned by the user (via CLI command or frontmatter `pinned: true`) are never compressed during context budget cycles and are always preserved in STM regardless of token budget.

---

## 8. Related Layers (Not Wiki Entries)

The memory system has additional layers beyond wiki entries:

| Layer | Purpose | Storage |
|-------|---------|---------|
| `pkg/memory/store` | Task/session JSON persistence | `.norman/memory/tasks/`, `.norman/memory/sessions/` |
| `pkg/memory/raw_sources` | Read-only environment snapshots (env vars, config reads) | `.norman/memory/sources/` |

These are **not** wiki entries and should not be classified with the four type categories above.

---

## 9. Future Enhancements (Planned)

See `docs/memory-proposal.md` for the full proposal:
- **Nested directories**: `memory/<type>/<title>.md` instead of flat `.norman/memory/`
- **Session bootstrap**: Bounded (~8 KiB) context injection at session start
- **Per-workspace `axes.json`**: Opt-in customization of retrieval priority, layout, and promotion paths
- **Structural audit**: Automated `map_dangling`, `duplicate_file_hash`, `orphan_file` checks

---

*This is an MVP v1.0 release. The core engine, index builder, and journal are wired into the agent loop. Further refinements will be made based on observed agent behavior during development.*
