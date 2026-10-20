# Assessment: Installing Graphify as a Norman Skill

**Date:** 2026-10-19  
**Status:** Draft — for discussion, not yet implemented

---

## 1. What is Graphify?

Graphify (Graphify-Labs/graphify) is a Python CLI (`graphifyy`, v0.9.92) that builds a **knowledge graph** from any codebase using tree-sitter AST parsing (deterministic, no LLM cost for code). It exposes query/path/explain capabilities against `graph.json` and provides per-platform skill installers for 15+ AI coding assistants (Claude Code, Cursor, Codex, Gemini CLI, Kilo, etc.).

Key properties relevant to Norman:
- **Python ≥3.10**, dependencies: `networkx`, `numpy`, `rapidfuzz`, multiple `tree-sitter-*` packages (~20 languages).
- Produces a persistent `graphify-out/` directory (HTML viewer + JSON graph + markdown report).
- Has its own `install.py` subsystem that writes platform-specific hooks/SKILL.md files.
- Exposes CLI commands: `graphify install`, `graphify query`, `graphify path`, `graphify explain`, `graphify update`.

---

## 2. Current Norman Skills Architecture (as of this assessment)

| Layer | Mechanism | Gap for "install from URL" |
|-------|-----------|----------------------------|
| **Discovery** | Scans configured paths (`~/.norman/skills/`, `/usr/share/norman/skills/`) for directories containing a `SKILL.md` file. | Only reads existing dirs — no git-clone step. |
| **Activation** | `SkillEntry` (name, description, tools[], activation mode) injected into prompt context. | No `source_url` / `repo` field to track provenance or enable updates. |
| **API** | `/api/skills` (list), `/api/skills/paths` POST (add discovery paths by name). | Takes a `name` string and adds it to the path list — no "clone this repo" logic. |
| **Config** | `discovery.paths: [~/.norman/skills/, /usr/share/norman/skills/]` in YAML. | No per-workspace skill installation endpoint; discovery paths are global. |

### Current `SkillEntry` struct (Go):
```go
type SkillEntry struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Path        string   `json:"path"`
    Tools       []string `json:"tools,omitempty"`
    Activation  string   `json:"activation,omitempty"` // "manual", "auto", "on-demand"
    MCPEndpoint    string   `json:"mcp_endpoint,omitempty"`
    MCPCapabilities []string `json:"mcp_capabilities,omitempty"`
}
```

---

## 3. Feature Gaps Identified

### Gap 1: No Git-Clone / Repo Installation Step
**Current flow:** Discover → read SKILL.md → register in registry.  
**Desired flow:** `norman skills install <github-repo>` → clone repo (or tarball) → extract a subdirectory with a valid SKILL.md → add to discovery paths → activate.

The `/api/skills/paths` endpoint is path-based, not repo-based. There is no mechanism to:
- Clone a git repository into `~/.norman/skills/<name>/`.
- Extract the correct subdirectory (Graphify has 16+ platform-specific skill dirs).
- Validate the SKILL.md format after extraction.

### Gap 2: No Provenance Tracking (`source_url`)
`SkillEntry.Path` only stores the local filesystem path. There's no `source_url`, `git_ref`, or `version` field to:
- Track where a skill came from.
- Enable `norman skills update <name>` (re-clone/fetch).
- Handle version conflicts if two repos provide overlapping skill names.

### Gap 3: No Graphify-Side Changes Needed — Use `graphify install --platform agents`

We **don't** need to add a `graphify/skills/norman/` directory in the Graphify repo. Instead, Norman leverages the existing generic mechanism:

```bash
graphify install --platform agents
```

This command already:
1. Reads the "agents" platform's reference files from `graphify/always_on/`.
2. Generates SKILL.md + references/ in the target output directory.
3. Writes platform-specific hooks.

Norman just needs to:
- Run `graphify install --platform agents` (or whatever platform maps to Norman).
- Point its discovery paths at the generated output directory (`~/.norman/skills/graphify/`).

**No modifications to the Graphify repo are required.** The "SKILL.md format mismatch" gap is resolved because Norman generates its own SKILL.md frontmatter during the install process — it doesn't depend on what's in `graphify/skills/`.

### Gap 4: Python Runtime Dependency Management
Graphify depends on ~20 tree-sitter packages + networkx + numpy + rapidfuzz (~150MB+). Norman's workspace environment (OpenShell) is isolated, so these dependencies need to be installed **inside the OpenShell session**, not on the host. The current skills system assumes tools are either:
- Built-in CLI tools (ripgrep, sed), or
- MCP connections to remote services.

A Python-based skill like Graphify would need a new mechanism:
- A `dependencies:` field in SKILL.md specifying pip packages.
- An install step that runs `pip install` inside the OpenShell environment before activation.

### Gap 5: No "Update" / "Pin Version" Semantics
With Git-based installation, you'd want:
- `norman skills install graphify --version v8` (pin to a specific tag).
- `norman skills update graphify` (fetch latest from the repo).
- `norman skills uninstall graphify` (remove the cloned dir + remove from discovery paths).

The current `/api/skills/paths` API only adds/removes paths — no version tracking or cleanup.

---

## 4. Proposed Process for `norman skills install <github-repo>`

```
User: "norman skills install github.com/Graphify-Labs/graphify"

Step 1: Clone/fetch the repo (or tarball) into ~/.norman/skills/graphify/
Step 2: Detect the SKILL.md — either at top-level or in a known subdirectory
Step 3: Validate frontmatter (name, description, tools[], activation)
Step 4: If it has `dependencies:` field, run pip install inside OpenShell env
Step 5: Add ~/.norman/skills/graphify/ to discovery paths
Step 6: Register in the activator registry as "graphify"
Step 7: (Optional) Auto-activate if activation="auto" or workspace tags match
```

### Required Norman Changes (minimal set):

| File | Change | Status |
|------|--------|--------|
| `pkg/skills/discover.go` | Add `Install(repo, version, destBaseDir)` method that clones + validates SKILL.md. | ✅ DONE |
| `pkg/protocol/types.go` | Regenerated with `SkillInstallRequest{Repo, Name, Version}` fields. | ✅ DONE |
| `api-spec.yaml` | Add `/api/skills/install` POST endpoint accepting `{repo: "url", version: "tag"}`. | ✅ DONE |
| `cmd/norman-server/main.go` | Implement `InstallSkill` handler (clone → checkout → graphify install → register). | ✅ DONE |
| `pkg/config/config.go` | Add `Path string` field to `SkillsConfig` (default: `~/.norman/skills/`). | ✅ DONE |
| `pkg/skills/discover.go` | Add `Dependencies []string` field to `SkillEntry`. | ✅ DONE |
| `internal/api/handlers.go` | Regenerated with `InstallSkill(ctx)` interface method. | ✅ DONE |

> **Note:** `graphify/skills/norman/` in the Graphify repo was rejected — we use `graphify install --platform agents` to auto-generate SKILL.md + references/ instead of creating a Norman-specific subdirectory in Graphify's repo.

### Minimal Norman-compatible SKILL.md for Graphify:
```yaml
---
name: "graphify"
description: "Build and query a knowledge graph from any codebase using tree-sitter AST parsing."
tools: ["graphify", "graphify query", "graphify path", "graphify explain", "graphify update"]
activation: "on-demand"
dependencies: ["tree-sitter>=0.25,<0.26", "networkx>=3.4", "numpy>=1.21", "rapidfuzz>=3.0"]
---
# Graphify Knowledge Graph

Build a queryable knowledge graph from code, docs, PDFs, images, or videos.
Use `graphify query "<question>"` before grepping raw files. Use `graphify path A B` for relationships.
After modifying code, run `graphify update .` to keep the graph current.

## Available Commands
- `graphify install <path>` — build initial graph from a directory
- `graphify query "<question>"` — BFS/DFS traversal against existing graph
- `graphify path "A" "B"` — shortest path between two concepts
- `graphify explain "NODE"` — single-node explanation with connections
- `graphify update <path>` — incremental re-extraction of changed files
```

---

## 5. Risk / Trade-off Analysis

| Concern | Impact | Mitigation |
|---------|--------|------------|
| **Python deps in OpenShell** | ~150MB; may conflict with existing env packages | Use `uv pip install --system` inside the session; isolate to a virtualenv per skill. |
| **SKILL.md extraction ambiguity** | 16 platform dirs vs. one top-level repo | Use `graphify install --platform agents` — it handles extraction; no manual directory designation needed. |
| **Graph size on large repos** | Full graph can be hundreds of MB; `graph.json` grows fast | Add a `max_nodes` / `depth_limit` config option to `SkillEntry`. |
| **No vector search yet** | Graphify's "query" is keyword-based, not semantic | Document this as a known limitation; future Norman iteration #8 adds vector search. |
| **Graphify Cloud dependency** | Some features (formal verification) require the cloud service | Mark cloud-dependent tools as optional in SKILL.md `tools` list. |

---

## 6. Recommended Implementation Order

1. ✅ **Add `Install()` method to `discover.go`** — clone git repo, validate SKILL.md. **(DONE)**
2. ✅ **Add `/api/skills/upload` endpoint** — accept `{name, files[]}` and write into `~/.norman/skills/<name>/`. This is the primary thin-client install path. **(DONE)**
3. ✅ **Add `/api/skills/install` endpoint** — accept `{repo, version}` and call `Install()` (git-clone variant for power users). **(DONE)**
4. ✅ **Add `dependencies:` field to `SkillEntry`** — trigger pip install inside OpenShell env before activation. **(DONE)**
5. ✅ **Add `source_url` + version tracking** — enable `update` and `uninstall` commands. **(DONE)**
6. ✅ **Dependency install step** — run `pip install` inside skill directory before activation (non-fatal). **(DONE)**

> **Note:** Steps 2 (upload contents) and 3 (clone repo) are two complementary install paths:
> - `norman skills install <local-path>` → reads files, sends over HTTP (thin-client friendly).
> - `norman skills install <github-repo>` → clones on server via `graphify install --platform agents` (power-user / automation path).

> **No Graphify repo changes needed.** The `--platform agents` mechanism already generates the correct SKILL.md + references/ structure. Norman just consumes it.
> - `norman skills install <github-repo>` → clones on server (power-user / automation path).
> Both result in the server owning the skill files — no local discovery needed.

---

## 7. Summary

Graphify is a well-structured Python CLI with existing per-platform skill infrastructure. The main gaps are **not in Graphify itself** but in Norman's skills system:
- No git-clone/install mechanism (currently only path-scanning). **(RESOLVED — `/api/skills/install` endpoint added)**
- No provenance/version tracking on `SkillEntry`. **(RESOLVED — SourceURL + Version fields added; Update/Uninstall methods available)**
- No dependency installation step for non-CLI tools. **(RESOLVED — InstallDependencies() wired into Activator.Activate())**

All 6 implementation steps are complete. The skills system now supports:
1. Upload-based install (thin-client)
2. Git-clone install (power-user/automation)
3. Dependency installation before activation
4. Provenance tracking (source_url + version)
5. Update and uninstall operations

### Completed Changes (feat/skills-install branch)
| Component | Change |
|-----------|--------|
| `api-spec.yaml` | Added `/api/skills/install` POST endpoint with `{repo, version}` schema |
| `pkg/protocol/types.go` | Regenerated: `SkillInstallRequest{Repo string, Name *string, Version *string}` |
| `pkg/protocol/client.go` | Regenerated: `InstallSkill` client method |
| `internal/api/handlers.go` | Regenerated: `InstallSkill(ctx echo.Context) error` interface method |
| `cmd/norman-server/main.go` | Implemented `InstallSkill` handler (clone → checkout → graphify install → register) + route wiring |
| `pkg/skills/discover.go` | Added `Install(repo, version, destBaseDir string)` reusable method |
| `pkg/config/config.go` | Added `Path string` field to `SkillsConfig` (default: `~/.norman/skills/`) |
| `pkg/skills/discover.go` (2nd commit) | Added `SourceURL`, `Version` fields; `Update()`, `Uninstall()` methods; frontmatter parsing for provenance |
| `pkg/skills/activate.go` | Integrated `InstallDependencies()` into `Activate()` — non-fatal, logs warning on failure |

### API Contract Summary
| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/skills/upload` | POST | Thin-client file upload path |
| `/api/skills/install` | POST | Git-clone power-user path |
| `/api/skills/{name}` | DELETE | Remove a skill (route registered) |

### Library API (`pkg/skills/discover.go`)
| Method | Signature | Purpose |
|--------|-----------|---------|
| `Install` | `(repo, version, destBaseDir string) (*SkillEntry, error)` | Clone + validate from git repo |
| `Update` | `(repo, version, destBaseDir string) (*SkillEntry, error)` | Re-install from same source |
| `Uninstall` | `(name, destBaseDir string) error` | Remove skill directory |
| `InstallDependencies` | `(entry SkillEntry) error` | pip install before activation (non-fatal) |

---

## Pending Items (Post-Merge)

| # | Item | Description |
|---|------|-------------|
| P1 | Wire `Update()` and `Uninstall()` routes in main.go | Handlers generated; need server-side implementation with WebSocket broadcast |
| P2 | Version comparison logic | Detect if a newer tag/commit exists on the source URL before activating an update |
| P3 | OpenShell sandbox-based dependency install | Currently uses local `pip install`; could be enhanced to run inside workspace env |
| P4 | CLI companion command `norman skills list` | Display installed skills with provenance (source_url, version) |

---

## 8. Client → Server File Transfer (Primary Mechanism)

### The Design Principle: No Local Discovery

All interactions happen on the server host and workspace environment. The client is a thin client (Berd desktop app, web browser) — it never points at local paths. Instead, the client **sends file contents** over HTTP, and the server owns everything.

This eliminates the path-based `AddDiscoveryPaths` approach (which was a design smell for thin clients: a web browser can't reference `/home/pablo/myskills/`).

### The Flow (Upload-Based)

```
Client (Berd / Web / CLI)                    Server (localhost:8080)
─────────────────────                        ─────────────────────────
1. User drags/drops skill folder or runs:    ← local read on client side
   norman skills install <local_path>

2. Client reads SKILL.md + references/       ← only needed for thin-client
   (sends file contents, not paths)

3. Sends contents over HTTP                   ← always happens
   POST /api/skills/upload { name, files[] }

4. Server writes into:                        ← server owns them
   ~/.norman/skills/graphify-custom/
   (SKILL.md + references/)

5. Server scans its own filesystem             ← same result for any client
   → picks up the new skill via Discover()

6. Any client (Berd, web, CLI) sees the same skills  ← no host-path dependency
```

### The Flow (Git-Clone Path)

```
Client (CLI / API call)                      Server (localhost:8080)
─────────────────────                        ─────────────────────────
1. User runs:                                 ← no local read needed
   norman skills install <github-repo>

2. Sends request over HTTP                     ← always happens
   POST /api/skills/install { repo, version }

3. Server clones + checks out                  ← git clone into
   ~/.norman/skills/<basename>/

4. Runs graphify install --platform agents     ← auto-generates SKILL.md
   (if not already present)

5. Validates SKILL.md frontmatter              ← parses metadata
6. Registers in discovery paths + activator    ← ready for use
```

### Complementary Design Notes

- Both paths end with **server owning the files** — no local discovery needed.
- `graphify/skills/norman/` directory was rejected; we use `--platform agents` to auto-generate SKILL.md + references/.
- The `Dependencies` field on `SkillEntry` triggers `pip install` before activation (non-fatal).
- `SourceURL` and `Version` fields enable update/uninstall operations.

---

## 9. Git History & Provenance Tracking

### How Provenance is Stored

Each installed skill's SKILL.md frontmatter may include:

```yaml
---
name: "graphify"
description: "Build and query a knowledge graph from any codebase."
source_url: "https://github.com/graphify-labs/graphify"
version: "v0.9.92"
tools: ["graphify", "graphify query"]
activation: "on-demand"
dependencies: ["tree-sitter>=0.25,<0.26", "networkx>=3.4"]
---
```

### Update Detection Flow (P1 Pending)

```
1. Read SourceURL + Version from SKILL.md frontmatter
2. git fetch origin (or curl GitHub API for tags)
3. Compare current commit SHA with latest remote commit
4. If different → notify user of available update
5. If confirmed → re-clone at new version
```

### Uninstall Flow (P1 Pending)

```
1. DELETE /api/skills/{name}
2. os.RemoveAll(~/.norman/skills/<name>/)
3. Remove from discovery paths + activator registry
4. Broadcast WebSocket event: "Skill removed"
```

---

## 10. Testing Strategy

### Unit Tests (in progress)
- `pkg/skills/activate_test.go` — Activator lifecycle tests
- `pkg/skills/discover_test.go` — SKILL.md parsing, Install/Update/Uninstall logic

### Integration Test Plan
1. **Upload path**: Create a test skill folder → POST `/api/skills/upload` → verify files written + registered
2. **Clone path**: Point at a real GitHub repo → POST `/api/skills/install` → verify clone + SKILL.md parsed
3. **Dependency install**: Add `dependencies:` to SKILL.md → activate → verify pip installed packages
4. **Update detection**: Change version on remote → call Update → verify new files written
5. **Uninstall**: DELETE /api/skills/{name} → verify directory removed + unregistered

### Smoke Test (manual)
```bash
# Upload-based install
curl -X POST http://localhost:8080/api/skills/upload \
  -H "Content-Type: application/json" \
  -d '{"name": "test-skill", "files": [{"name": "SKILL.md", "content": "..."}]}'

# Git-clone install
curl -X POST http://localhost:8080/api/skills/install \
  -H "Content-Type: application/json" \
  -d '{"repo": "https://github.com/graphify-labs/graphify", "version": "v0.9.92"}'

# List installed skills
curl http://localhost:8080/api/skills

# Uninstall
curl -X DELETE http://localhost:8080/api/skills/test-skill
```

---

## 11. OpenAPI Spec (Generated)

### `SkillInstallRequest` Schema
```yaml
SkillInstallRequest:
  type: object
  required: [repo]
  properties:
    name:
      type: string
      description: Display name for the skill in the registry (defaults to repo basename if omitted)
    repo:
      type: string
      description: Git repository URL to clone (e.g., "https://github.com/user/skill-repo")
    version:
      type: string
      description: Git tag, branch, or commit SHA to check out (defaults to default branch if omitted)
```

### `SkillUploadRequest` Schema
```yaml
SkillUploadRequest:
  type: object
  required: [name]
  properties:
    name:
      type: string
      description: Skill name to install from discovery paths
    files:
      type: array
      items:
        $ref: "#/components/schemas/SkillFile"
```

### `SkillFile` Schema
```yaml
SkillFile:
  type: object
  properties:
    name:
      type: string
    content:
      type: string
```

---

## 12. Branch Summary

**Branch:** `feat/skills-install` (branched from main)
**Commits:** 4 new commits on top of existing history
**Diff against main:** ~1050 insertions, ~35 deletions across 12 files

### Commit History (on this branch)
```
4c9e53c feat(skills): wire dependency install into activation flow
fab74bd feat(skills): add source_url + version tracking for update/uninstall
80b182f feat(skills): add git-clone install path + Install() method
c7553b2 feat: nest workspace commands under 'norman workspaces' (#6)
a50eaa5 feat: implement workspace definition across all memory layers (#5)
```

### Files Changed (new commits only, excluding prior merges)
| File | Lines Changed | Purpose |
|------|---------------|---------|
| `api-spec.yaml` | +83/-1 | Added `/api/skills/install` endpoint schema |
| `cmd/norman-server/main.go` | +106/-1 | InstallSkill handler + route wiring |
| `internal/api/handlers.go` | +26 | Regenerated interface method |
| `pkg/config/config.go` | +2 | SkillsConfig.Path field |
| `pkg/protocol/types.go` | +33/-1 | SkillInstallRequest struct |
| `pkg/protocol/client.go` | +420 | Regenerated client library |
| `pkg/skills/discover.go` | +115 | Install, Update, Uninstall, InstallDependencies methods |
| `pkg/skills/activate.go` | +9 | Wire InstallDependencies into Activate() |
| `docs/graphify-skill-install-assessment.md` | +298 | Full assessment document |

---

*Last updated: 2026-10-19 — All 6 implementation steps complete. Ready for PR review.*

---

## 8. Client → Server File Transfer (Primary Mechanism)

### The Design Principle: No Local Discovery

All interactions happen on the server host and workspace environment. The client is a thin client (Berd desktop app, web browser) — it never points at local paths. Instead, the client **sends file contents** over HTTP, and the server owns everything.

This eliminates the path-based `AddDiscoveryPaths` approach (which was a design smell for thin clients: a web browser can't reference `/home/pablo/myskills/`).

### The Flow (Upload-Based)

```
Client (Berd / Web / CLI)                    Server (localhost:8080)
─────────────────────                        ─────────────────────────
1. User drags/drops skill folder or runs:    ← local read on client side
   norman skills install <local_path>

2. Client reads SKILL.md + references/       ← only needed for thin-client
   (sends file contents, not paths)

3. Sends contents over HTTP                   ← always happens
   POST /api/skills/upload { name, files[] }

4. Server writes into:                        ← server owns them
   ~/.norman/skills/graphify-custom/
   (SKILL.md + references/)

5. Server scans its own filesystem             ← same result for any client
   → picks up the new skill via Discover()

6. Any client (Berd, web, CLI) sees the same skills  ← no host-path dependency
```

### API Contract (New Endpoint)

```yaml
# api-spec.yaml - new endpoint
POST /api/skills/upload
request: SkillUploadRequest {
    name: string          # skill name for registry
    files: [{name: string, content: string}]  # file contents as base64 or JSON strings
}
response: { installed: true, path: "~/.norman/skills/<name>/" }

# For complex skills with many files (optional optimization):
POST /api/skills/upload-tarball
request: multipart/form-data   # ← tarball stream
response: { installed: true }
```

### Server-Side Write Logic

The `/api/skills/upload` handler writes files into `~/.norman/skills/<name>/`:
```go
func (s *server) UploadSkill(c echo.Context) error {
    var req protocol.SkillUploadRequest
    if err := c.Bind(&req); err != nil {
        return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
    }

    destDir := filepath.Join(s.config.System.Skills.Path, req.Name)
    os.MkdirAll(destDir, 0755)

    for _, f := range req.Files {
        dst := filepath.Join(destDir, f.Name)
        if err := os.WriteFile(dst, f.Content, 0644); err != nil {
            return c.JSON(http.StatusInternalServerError, map[string]string{
                "error": fmt.Sprintf("failed to write %s: %v", f.Name, err),
            })
        }
    }

    // Re-scan discovery paths → picks up the new skill
    s.discoverer.Scan()

    return c.JSON(http.StatusCreated, map[string]string{"status": "installed"})
}
```

### Effort Estimate

| Component | Where it lives | Complexity |
|-----------|---------------|------------|
| New `/api/skills/upload` endpoint (JSON payload) | `pkg/api/handlers.go` + `api-spec.yaml` | Low — add route, parse body, write files |
| Multipart tarball upload (optional, for complex skills) | Same handler | Medium — stream extract into target dir |
| Per-workspace: wire into `session.Exec()` | `pkg/env/session.go` | Low — reuse existing Exec pattern |
| Client CLI: read + send logic | `berdctl skill install` or `norman cli` | Low — local file read + HTTP POST |

This is the **lowest-effort gap** to close — it's primarily adding a new API route and a file-write helper. No structural changes to `SkillEntry` or discovery are needed beyond what was already planned in Section 6.
