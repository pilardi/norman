# Step 11 — Build system cleanup

> **Status:** ✅ COMPLETE (merged to `main`)

---

## Problem Statement

The project had ad-hoc `go build` commands scattered across README and documentation, no proper Makefile, stale generated `.go` files tracked in git, compiled binaries left at repo root, and a leftover `server/` directory from a previous rename refactor. This made the build process unreliable and non-reproducible.

---

## Current State (at time of planning)

- No `Makefile` — developers ran `go build ./cmd/norman-server/` manually.
- Generated files (`pkg/protocol/types.go`, `internal/api/handlers.go`) tracked in git alongside hand-written code.
- Compiled binary `norman-server` left at repo root after builds.
- Stale `server/` directory from previous rename (now `cmd/norman-server/`).
- No `.gitignore` entries for build artifacts or generated files.

---

## Proposed Architecture

### 1. Makefile Pipeline (`Makefile`)

| Target | Purpose |
|--------|---------|
| `make help` | Document all available targets with descriptions. |
| `make generate` | Run `oapi-codegen` to regenerate protocol types, client library, and server handlers from `api-spec.yaml`. |
| `make build` | Compile both CLI (`norman`) and server (`norman-server`) binaries to `dist/`. |
| `make test` | Run `go test ./...` with race detector. |
| `make vet` | Run `go vet ./...`. |
| `make clean` | Remove `dist/`, generated files, and stale artifacts. |

---

### 2. API Spec Updates (`api-spec.yaml`)

| Change | Purpose |
|--------|---------|
| Add `ArchiveSessionRequest` schema | Request body for `/api/sessions/archive`. |
| Add `NotificationEntry` schema | Response type for notification list endpoint. |
| Add `NotificationCreateRequest` schema | Request body for creating notifications. |
| Add `session_compressed` enum value | WebSocket event type for compression events. |

---

### 3. Git Tracking Cleanup

| Change | Purpose |
|--------|---------|
| Add `.gitignore` entries for `dist/`, generated files, stale `server/`. | Prevent build artifacts from being tracked. |
| Remove `internal/api/handlers.go` from git tracking. | It's a generated file; only track the spec source. |

---

### 4. Binary Output Location

| Change | Purpose |
|--------|---------|
| Move compiled binaries from repo root to `dist/`. | Keep repo clean; match Go convention (e.g., `go install` behavior). |

---

### 5. Stale Directory Removal

| Change | Purpose |
|--------|---------|
| Remove `server/` directory (renamed to `cmd/norman-server/`). | Eliminate confusion from stale paths in documentation and imports. |

---

## Data Model

```go
// api-spec.yaml additions
components:
  schemas:
    ArchiveSessionRequest:
      type: object
      properties:
        task_id:
          type: string
          description: ID of the session to archive
    NotificationEntry:
      type: object
      properties:
        id:
          type: string
        title:
          type: string
        body:
          type: string
        severity:
          type: string
          enum: [info, warn, error]
        timestamp:
          type: string
          format: date-time

enums:
  WebSocketEventType:
    - task_progress
    - task_complete
    - task_failed
    - session_compressed   # NEW
```

---

## Integration Points

| Component | Change |
|-----------|--------|
| `Makefile` | New file — build pipeline with help, generate, build, test, vet, clean targets. |
| `api-spec.yaml` | Add 3 new schemas + 1 enum value; regenerate via `make generate`. |
| `.gitignore` | Add entries for `dist/`, generated `.go` files, stale artifacts. |
| `cmd/norman-server/main.go` | Remove imports/references to old `server/` path. |

---

## Acceptance Criteria

- [ ] `make help` prints all targets with descriptions.
- [ ] `make generate` produces up-to-date `pkg/protocol/types.go`, `client.go`, `internal/api/handlers.go`.
- [ ] `make build` outputs binaries to `dist/norman` and `dist/norman-server`.
- [ ] `make test` runs all package tests with `-race` flag.
- [ ] `git status` shows clean working tree after `make clean → make build`.
- [ ] No generated `.go` files tracked in git; only `api-spec.yaml` is the source of truth.

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| oapi-codegen version mismatch between dev and CI | Pin exact version in Makefile; add `make generate` to CI pipeline. |
| Generated files drift from spec | Run `make generate` before every build; fail CI if diff > 0. |
| Stale `server/` imports still referenced somewhere | `go vet ./...` catches unused imports; grep for remaining references. |

---

## Dependencies on Other Steps

- **Step 5a** (API spec): `api-spec.yaml` is the input to `make generate`.
- **Step 10** (server rename): `server/` → `cmd/norman-server/` must be complete before cleanup.
- **Step 6** (wiki engine): Wiki entries don't need build-time generation; this step is orthogonal.

---

*This is a plan draft. Actual implementation follows approved sub-steps.*
