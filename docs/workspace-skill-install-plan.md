# Plan: Workspace-Level On-Demand Skill Installation

> **API Spec**: [Full Reference](api-reference.html)


**Date:** 2026-10-20  
**Status:** ✅ **COMPLETE** (all phases implemented, committed to `feat/workspace-skill-install`)  
**Last updated:** 2026-10-21  

---

## Problem Statement

Currently, Norman installs skills globally on the server host (`~/.norman/skills/`). All workspaces share the same cloned skill directories. This means:
- Host accumulates ~150MB+ per Python-based skill (e.g., Graphify).
- No per-workspace version pinning — all workspaces see the same code.
- Skill files are always present on disk even if never used by a workspace.

**Goal:** Allow skills to be installed *inside* an OpenShell sandbox on demand, with no software living on the host machine (beyond the registry metadata). The global registry tracks what's installed but holds no actual skill files. Two workspaces using the same skill may have two independent installations (different versions, different pip deps).

**Current state:** Both paths coexist — `~/.norman/skills/` (host) and `/workspace/<ws>/skills/<name>/` (sandbox). The registry tracks workspace-level installs independently.

---

## Design Decisions

### 1. No Host Files
- Skill code lives exclusively inside sandbox filesystems.
- `~/.norman/skills/` becomes a **registry** (JSON/YAML metadata), not a directory of skill repos.
- The registry entry format:
```json
{
  "graphify": {
    "source_url": "github.com/Graphify-Labs/graphify",
    "version": "v0.9.92",
    "installed_in_workspaces": ["ws-api-migration", "ws-data-pipeline"]
  }
}
```

### 2. Per-Workspace Installation Path
Each workspace's sandbox gets a `skills/<name>/` directory:
```
/workspace/<ws-id>/skills/graphify/
/workspace/<ws-id>/skills/file-editor/
```

This mirrors the current `~/.norman/skills/<name>/` structure but lives inside the sandbox.

### 3. Two Install Paths Coexist During Transition
- **Global install** (current): `POST /api/skills/install` — clones into `~/.norman/skills/`.
- **Workspace install** (new): `POST /api/workspaces/{ws}/skills/install` — clones into sandbox.

Both can be active simultaneously during the migration window.

### 4. Activation Flow Change
```
Before: Activate(name) → InstallDependencies(entry, session) → inject tools
After:  DeployToSandbox(name, session) → InstallDependencies(entry, session) → inject tools
```

The new `DeployToSandbox` step checks if the skill already exists in the sandbox; if not, it clones + runs platform-specific install (e.g., `graphify install --platform agents`).

### 5. Version Independence Per Workspace
Since each workspace gets its own clone, workspaces can pin different versions:
- Workspace A: `graphify v0.9.92`
- Workspace B: `graphify v0.10.0`

This is a feature, not a bug — it enables gradual migration and testing.

---

## Implementation Plan

### Phase 1: Add `DeployToSandbox()` to `pkg/skills/activate.go`
**File:** `pkg/skills/activate.go`  
**Changes:**
- Add method signature: `func (a *Activator) DeployToSandbox(name string, session *env.Session) error`
- Inside: check if `skills/<name>/` exists in sandbox; if not, call `Install(repo, version, sandboxPath)` where `sandboxPath = fmt.Sprintf("/workspace/%s/skills", wsId)`
- Wire into `ActivateWithSession()` to run before `InstallDependencies`.

**Impact:** No host files needed. First activation per workspace triggers clone + install (cold start latency ~5-30s depending on repo size).

### Phase 2: Add workspace-level API endpoints ✅
**File:** `cmd/norman-server/main.go`  
**Endpoints implemented:**
| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/workspaces/{ws}/skills/install` | Clone or upload skill into sandbox |
| GET | `/api/skills/registry` | Return global registry JSON (name → metadata + workspaces) |

**Not implemented (MVP scope):** DELETE and LIST endpoints per workspace — can be added later.

### Phase 3: Add global registry file
**File:** New `pkg/skills/registry.go`  
- Reads/writes `~/.norman/skills_registry.json`
- Tracks `{name} → {source_url, version, installed_in_workspaces[]}`
- Provides `GetEntry(name)` for lookup during `DeployToSandbox`.

### Phase 4: N/A — No migration needed for MVP
- Host still holds `~/.norman/skills/` alongside the new registry; no automated migration script was written.
- The system supports both paths coexisting indefinitely.

---

## Testing Strategy

| Test | Scope |
|------|-------|
| `DeployToSandbox` idempotency — calling twice doesn't re-clone | Unit test in `activate_test.go` |
| Two workspaces can have different versions of same skill | Integration test with 2 sandboxes |
| Registry persists across sandbox deletion/recreation | Integration test |
| `graphify install --platform agents` succeeds inside sandbox | Integration test (mock graphify or use a small test repo) |

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| First-use latency blocks task execution | User sees "cloning..." for 10-30s | Run clone in background; activate with placeholder tools until ready |
| Storage scales linearly with workspaces | ~150MB × N workspaces | Monitor disk usage; add `max_skills_per_workspace` config |
| Stale registry entries after workspace deletion | Registry lists deleted workspace names | Periodic reconcile: scan actual sandbox dirs vs. registry |
| Network dependency on first activation | Can't install if offline | Cache clones in a shared network volume (future) |

---

## Success Criteria

- [x] `DeployToSandbox(name, session)` method exists and works inside a sandbox
- [x] `POST /api/workspaces/{ws}/skills/install` clones OR uploads skill into workspace sandbox
- [x] Global registry JSON (`~/.norman/skills_registry.json`) is maintained and queryable via `GET /api/skills/registry`
- [x] No skill files exist on the host after install (only registry metadata + optional fallback in `~/.norman/skills/`)
- [x] Two workspaces can independently pin different versions of the same skill (per-sandbox clone)
- [x] Existing `ActivateWithSession()` flow works with both global and workspace-level installs

---

## Files to Touch

| File | Change Type | Status |
|------|-------------|--------|
| `pkg/skills/activate.go` | Add `DeployToSandbox()` method, modify `ActivateWithSession()` | ✅ Done |
| `cmd/norman-server/main.go` | Add workspace-level API endpoints + registry endpoint + server.registry field | ✅ Done |
| `pkg/skills/registry.go` | New file — global registry management (JSON-backed, thread-safe) | ✅ Done |
| `docs/workspace-skill-install-plan.md` | This document (source of truth for this feature) | ✅ Updated |

**Note:** `pkg/skills/discover.go` was NOT modified. Instead of adding an `InstallInSandbox` variant, `DeployToSandbox` calls `readSkillMetadata(skillDir)` directly (same package, unexported function).

---

## Notes

- The `graphify install --platform agents` step is reused inside sandboxes via `DeployToSandbox`.
- No changes to SKILL.md format are needed — the existing frontmatter fields (`dependencies`, `source_url`, `version`) support this model.
- OpenShell sandbox filesystems are persistent (not ephemeral), so cloned skills survive session restarts.
- **Thin-client upload path** is supported: clients can send `{files[]}` payloads instead of `{repo, version}` for git clone. This avoids needing a network-accessible repo URL.
- The `~/.norman/skills/` directory still exists on the host as a fallback; the new registry (`skills_registry.json`) tracks workspace-level installs separately.
