# Plan: Workspace-Level On-Demand Skill Installation

**Date:** 2026-10-20  
**Status:** Draft — ready for implementation  

---

## Problem Statement

Currently, Norman installs skills globally on the server host (`~/.norman/skills/`). All workspaces share the same cloned skill directories. This means:
- Host accumulates ~150MB+ per Python-based skill (e.g., Graphify).
- No per-workspace version pinning — all workspaces see the same code.
- Skill files are always present on disk even if never used by a workspace.

**Goal:** Allow skills to be installed *inside* an OpenShell sandbox on demand, with no software living on the host machine. The global registry tracks what's installed but holds no actual files. Two workspaces using the same skill may have two independent installations (different versions, different pip deps).

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

### Phase 2: Add workspace-level API endpoints
**File:** `cmd/norman-server/main.go`  
**Endpoints:**
| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/workspaces/{ws}/skills/install` | Clone skill into sandbox |
| DELETE | `/api/workspaces/{ws}/skills/{name}` | Remove from sandbox |
| GET | `/api/workspaces/{ws}/skills/list` | List installed skills in workspace |

**Also add:** `GET /api/skills/registry` — returns the global registry JSON.

### Phase 3: Add global registry file
**File:** New `pkg/skills/registry.go`  
- Reads/writes `~/.norman/skills_registry.json`
- Tracks `{name} → {source_url, version, installed_in_workspaces[]}`
- Provides `GetEntry(name)` for lookup during `DeployToSandbox`.

### Phase 4: Migrate existing skills to workspace-level (optional)
- Script to migrate host-installed skills into a default workspace's sandbox.
- Keep global install path as fallback during transition.

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

- [ ] `DeployToSandbox()` method exists and works inside a sandbox
- [ ] `POST /api/workspaces/{ws}/skills/install` clones skill into workspace sandbox
- [ ] Global registry JSON is maintained and queryable
- [ ] No skill files exist on the host after migration (only registry metadata)
- [ ] Two workspaces can independently pin different versions of the same skill
- [ ] Existing `ActivateWithSession()` flow works with both global and workspace-level installs

---

## Files to Touch

| File | Change Type |
|------|-------------|
| `pkg/skills/activate.go` | Add `DeployToSandbox()` method, modify `ActivateWithSession()` |
| `pkg/skills/discover.go` | Add `InstallInSandbox(repo, version, sandboxPath)` variant |
| `cmd/norman-server/main.go` | Add workspace-level API endpoints + registry endpoint |
| `pkg/skills/registry.go` | New file — global registry management |
| `docs/workspace-skill-install-plan.md` | This document (source of truth for this feature) |

---

## Notes

- The `graphify install --platform agents` step is already implemented in the current `InstallSkill` handler. We reuse it inside sandboxes.
- No changes to SKILL.md format are needed — the existing frontmatter fields (`dependencies`, `source_url`, `version`) support this model.
- This plan assumes OpenShell sandbox filesystems are persistent (not ephemeral). If sandboxes get recreated, we need a "re-deploy" mechanism.
