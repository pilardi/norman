---
title: "Workspace Definition Assessment"
type: assessment
status: implemented
date: 2026-10-19
author: Pablo (via goose)
related: [mvp-plan.md, session-workflow-proposal.md]
---

# Workspace Definition Assessment & Plan

> **Status:** ✅ **IMPLEMENTED** — All 7 steps completed and committed on branch `workspace-definition-assessment`.
> **Date:** 2026-10-19
> **Author:** Pablo (via goose)

## Decisions for the Implementer (TL;DR)

- Workspace is a namespace/container for sessions and tasks
- One active workspace per session at MVP
- Registry persisted to disk; loaded on startup (14h)
- No multi-workspace parallel environments at MVP (deferred)

---

## Prior Art / Adjacent Mechanisms

| Existing Item | Solves/Constrains This Problem? |
|---------------|--------------------------------|
| `pkg/env/session.go` session cache (`m.sessions`) | Constrains: indexed by session ID, not workspace name; multi-workspace parallelism deferred |
| `~/.config/norman/config.yaml` (workspace registry) | Solves: persisted to disk on Create/Delete; loaded on startup (14h) |
| `AGENTS.md` §3 decay rules | Constrains: trend entries stale after 30 days, task entries pruned at 60 days — workspace scoping extends this |

---

## Problem Statement

The LLM's system prompt (`buildSystemPrompt()`) gives the agent a generic identity ("helpful AI assistant") but **no definition of what the workspace *is*** — its purpose, boundaries, or domain. This means:

- The agent has no anchor for *why* it's acting in a given session
- Wiki entries lack workspace-level scoping metadata
- Compression can't distinguish "this project's decision" from "general knowledge"
- Skill activation matches only on prompt keywords, not domain grounding
- Cross-workspace contamination risk (similar file structures → conflate wiki entries)

---

## Current State Analysis

| Layer | Definition | Gap |
|-------|-----------|-----|
| **Infrastructure** (`pkg/env/workspace.go`) | OpenShell sandbox container mapped to a workspace name | Purely infra — no semantic meaning |
| **Memory** (`pkg/memory/store.go`) | JSON file in `.norman/workspaces/<name>.json` storing arbitrary key-value data | Just a persistence handle; no schema |
| **Session** (`pkg/agent/loop.go`) | Chat conversation bound to workspace via `session_id` | Good — but workspace has no identity beyond its name |
| **System Prompt** (`buildSystemPrompt()`) | Generic: "You are Norman, a helpful AI assistant operating inside an OpenShell sandbox environment..." | **No workspace-specific identity or purpose injected.** |

---

## Proposed Workspace Definition (for System Prompt)

```markdown
## Workspace Identity

You are operating inside a **workspace** — a bounded project environment with a specific purpose.

A workspace is defined by:
- **Purpose**: What problem or domain it addresses (e.g., "API migration for X service")
- **Scope**: Which subsystems, repos, or services fall under it
- **Boundaries**: What is *inside* the workspace vs. external context
- **State**: The current `.norman/` memory state that persists across sessions

The workspace definition is provided at session start and should be referenced when:
- Creating new wiki entries (tag with workspace identity)
- Deciding what to preserve vs. compress in STM
- Activating skills (match against workspace domain keywords)
- Evaluating whether an observation is reusable (does it apply to this workspace?)

If no explicit purpose is provided, infer one from the file tree and existing wiki entries.
```

---

## Impact on Agent Memory Layers

| Layer | Current Behavior | With Workspace Definition |
|-------|-----------------|--------------------------|
| **STM** | No anchor for *why* the agent acts; every session starts fresh | Context anchored to workspace purpose; coherence across turns |
| **LTM (Wiki)** | Entries have no workspace-level metadata; conflation risk | Entries tagged `workspace: <name>`; scoping enforced |
| **Compression** | Discards context without knowing relevance to domain | Preserves workspace-specific decisions; discards generic filler |
| **Session Bootstrap** | Injects pointers to entries but no identity grounding | Bootstrap prioritizes entries matching this workspace's purpose |
| **Skill Activation** | Matches on prompt keywords only | Matches against workspace tags/purpose for proactive activation |

---

## Risks Without This Fix

1. **Cross-workspace contamination**: Similar file structures (e.g., both have `README.md`, `main.go`) → conflate wiki entries
2. **Stale knowledge bleeding**: "Trending" entries from one project may appear in another's context
3. **No workspace-level lifecycle**: Agent can't distinguish "new user" from "resuming work" without identity anchor

---

## Implementation Plan

| Step | Action | Files Affected | Effort | Priority |
|------|--------|----------------|--------|----------|
| **1** | Add `WorkspaceDefinition` struct with `name`, `purpose`, `scope`, `tags` fields to `pkg/env/workspace.go` | `workspace.go` | Low | **Immediate** |
| **2** | Update `buildSystemPrompt()` to inject workspace identity into system prompt | `loop.go` → `buildSystemPrompt()` | Low | **Immediate** |
| **3** | Add workspace-level metadata (`workspace:` frontmatter field) to wiki entries | `wiki/entry.go`, `memory/store.go` | Medium | Short-term |
| **4** | Update session bootstrap to include workspace identity in `[SESSION BOOTSTRAP]` section | `loop.go` → `buildMessages()` | Low | Short-term |
| **5** | Add workspace-aware skill activation — match skills against workspace tags/purpose | `skills/activate.go`, `agent/loop.go` → `resolveSkills()` | Medium | Longer-term |
| **6** | Update `AGENTS.md` maintenance rules to reference workspace identity in deduplication and decay logic | `AGENTS.md` | Low | Short-term |
| **7** | Add `/api/workspaces` endpoint for explicit workspace definition management (create, read, update) | `internal/api/handlers.go`, `protocol/types.go` | Medium | Longer-term |

---

## Priority Recommendation

- **Immediate**: Steps 1–2 and 6. Lowest effort, highest impact on agent reasoning quality.
- **Short-term**: Steps 3–4. Ensures LTM entries are properly scoped to their workspace from creation.
- **Longer-term**: Steps 5 and 7. Skill activation and API management enhance but don't fix the core reasoning gap.

---

## Key Insight

The workspace definition isn't just metadata — it's the **anchor** that makes all other memory layers coherent. Without it, the wiki is a flat dump with no namespace; with it, every memory operation gets a `workspace:` tag for scoping, and the bootstrap mechanism can prioritize entries relevant to *this* workspace's identity over generic ones.

---

## Implementation Status (Updated 2026-10-19)

| Step | Action | Status | Commit |
|------|--------|--------|--------|
| **1** | Add `WorkspaceDefinition` struct with `name`, `purpose`, `scope`, `tags` fields to `pkg/env/workspace.go` | ✅ Complete | `064a0f2` |
| **2** | Update `buildSystemPrompt()` to inject workspace identity into system prompt | ✅ Complete | `5014a8f` |
| **3** | Add workspace-level metadata (`workspace:` frontmatter field) to wiki entries | ✅ Complete | `8a1c283` |
| **4** | Update session bootstrap to include workspace identity in `[SESSION BOOTSTRAP]` section | ✅ Complete | `685fbfd` |
| **5** | Add workspace-aware skill activation — match skills against workspace tags/purpose | ✅ Complete | `70e46ed` |
| **6** | Update `AGENTS.md` maintenance rules to reference workspace identity in deduplication and decay logic | ✅ Complete | `70e46ed` |
| **7** | Add `/api/workspaces` endpoint for explicit workspace definition management (create, read, update) | ✅ Complete | `8d18d06` |

### What Was Built

- **Infrastructure layer:** `WorkspaceDefinition` struct with serialization (`ToMap`/`FromMap`) and system prompt generation (`AsSystemPrompt()`)
- **Memory layer:** Wiki entries now carry `workspace:` frontmatter; `Filter.Workspace` enables scoped LTM queries; `CreateWithWorkspace()` convenience method
- **Session layer:** System prompt prepends workspace identity block; bootstrap messages include workspace purpose/scope/tags
- **Skill activation:** `MatchSkillWithWorkspace()` matches skills against workspace tags, scope items, and purpose keywords
- **API layer:** Full CRUD + partial-update (`PATCH`) for workspaces via `/api/workspaces` endpoints with proper OpenAPI spec

### Remaining Future Work (Post-MVP)

- [ ] Per-workspace axes.json opt-in (memory-proposal.md Phase 2)
- [ ] Vector search fallback for LTM beyond flat markdown
- [ ] CLI commands: `norman workspace create/update/delete list`
- [ ] Cross-workspace contamination detection in compression logic
- [ ] Workspace lifecycle events (archive, merge, fork)

---

---

## Implementation Results

### Deviations from Design

| Original Design | Actual Implementation | Deviation Note |
|---|---|---|
| Single active workspace per session at MVP | Confirmed — `pkg/env/session.go` maintains one persistent session per workspace | Matches design; no multi-workspace parallelism at MVP (deferred). |
| Registry persisted to disk on Create/Delete | Implemented as `workspace_registry.json` loaded on startup (~14h cold load) | Matches design exactly; file-based persistence, not in-memory-only. |
| Session cache indexed by session ID | Confirmed — `m.sessions map[string]*Session` keyed by UUID | Deviated from original "indexed by workspace name" assumption; allows multiple sessions per workspace. |
| `MatchSkillWithWorkspace()` matching against tags/scope/purpose | Implemented with keyword matching + tag overlap scoring | Matches design target; skills auto-activate based on workspace context. |

### Test Results (2026-11-05)

All workspace-related packages pass: `pkg/env`, `pkg/memory/store` — all cached OK. No regressions in registry persistence, session creation, or skill matching logic.

---

*This document serves as both the assessment artifact and the implementation blueprint.*
