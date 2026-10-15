# Norman — MVP Implementation Plan

> **Goal:** An end-to-end CLI client → Agent Server workflow where a user can configure an agent, create workspaces, install skills, and run tasks.
>
> **Principle:** Minimize custom code. Compose capabilities from existing tools (OpenShell, git, MCP, shell utilities). The server is the only substantial Go implementation (~5–8k lines).

---

## Step 1 — Project scaffolding & module setup

> **Status: ✅ COMPLETE** (committed on `step1-scaffold` branch)

**Acceptance:** `go mod init github.com/norman/norman` succeeds; package tree compiles with `go build ./...`.

- Define package structure:
  ```
  norman/
    cmd/
      norman/           # CLI client binary
      server/           # Agent Server binary (subcommand of one binary)
    pkg/
      agent/            # Core agent loop: planning, context budgeting, memory management
      env/              # OpenShell session wrapper + tool dispatch
      skills/           # Skill discovery, manifest parsing, progressive activation
      mcp/               # MCP client integration (using go-sdk)
      memory/            # LTM wiki engine (flat markdown + frontmatter + git versioning)
      config/            # YAML config parser with ${secret:...} placeholder resolution
      notify/            # Notification routing (terminal bell, Slack webhook)
      scheduler/         # Cron registration via OpenShell's cron API
    internal/
      api/               # HTTP/WebSocket server endpoints (generated from api-spec.yaml)
      protocol/          # Thin client protocol (request/response types, generated)
  ```
- **Libraries:** `go-yaml/yaml/v3` (YAML parsing), `github.com/modelcontextprotocol/go-sdk/mcp` (MCP client), `github.com/labstack/echo/v4` (HTTP server framework, generated), `github.com/oapi-codegen/runtime` (runtime helpers for generated code).
- **Code generation:** All protocol types, client methods, and server handlers are generated from `api-spec.yaml` using `oapi-codegen`. Regenerate with:
  ```bash
  ~/go/bin/oapi-codegen -package protocol -generate types api-spec.yaml > pkg/protocol/types.go
  ~/go/bin/oapi-codegen -package protocol -generate client api-spec.yaml > pkg/protocol/client.go
  ~/go/bin/oapi-codegen -package api -generate server api-spec.yaml > internal/api/handlers.go
  ```

### Completion Notes
- All 10 package directories created and scaffolded with `init.go` stubs.
- Runtime dependencies declared: `yaml.v3`, `go-sdk/mcp`, `gorilla/websocket`, `tiktoken-go`, `go-git`.
- `go build ./...` passes cleanly (exit 0).
- Committed as `ef04176` on branch `step1-scaffold`.

---

## Step 1.5 — API Spec & Generation (new)

> **Status: ✅ COMPLETE** (api-spec.yaml valid, `make generate` produces compilable code, `go build ./...` passes)

**Acceptance:** `api-spec.yaml` is valid OpenAPI 3.1; running oapi-codegen produces compilable Go code in `pkg/protocol/` and `internal/api/`.

- The full client-server protocol is formalized in `api-spec.yaml` (OpenAPI 3.1).
- Three generation targets: types (`types.go`), client library (`client.go`), server handlers (`handlers.go`).
- WebSocket endpoint (`GET /ws`) included in generated server stubs.
- The spec is the **single source of truth** — any protocol change requires updating `api-spec.yaml` and regenerating.

---

## Step 2 — Config parser + CLI scaffolding

> **Status: ✅ COMPLETE** (config.yaml parser, `norman create/switch/list/config/skills/sessions/tasks` subcommands working via Echo routing)

**Acceptance:** Running `norman config --help` shows subcommands; `norman create my-workspace` creates a workspace directory + `.openshell/` overlay.

- Parse YAML from `~/.config/norman/config.yaml` (or env var `NORMAN_CONFIG`).
- **Do not resolve `${secret:...}` at parse time.** Store raw placeholders; resolve inside the environment via OpenShell's `get-secret`.
- Validate required fields early. Provide sensible defaults (`gpt-4o`, port `8080`).
- CLI subcommands: `create <name>`, `switch <name>`, `run [prompt]`, `list`, `config`, `skills list/install/remove`, `sessions`, `tasks`.

## Step 3 — OpenShell environment integration

> **Status: ✅ COMPLETE** (OpenShell Go SDK v0.1.x: client, session, workspace manager with crash recovery + exponential backoff)

**Acceptance:** `norman create my-workspace` creates a persistent OpenShell session; running `norman run "echo hello"` executes inside that session and returns output.

- Create one persistent OpenShell session per workspace via the OpenShell CLI/API.
- All file I/O goes through OpenShell's tools layer (`read`, `write`, `edit`) — **never** direct host OS access.
- Check OpenShell version ≥ 0.1.0 on session creation; log warning if features are missing.
- Session lifecycle: create on `norman create`, reuse across tasks, recover if crashed (retry up to 3× with exponential backoff).

## Step 4 — Skill discovery & progressive activation

> **Status: ✅ COMPLETE** (committed on `step4-skills` branch)

**Acceptance:** Running `norman skills list` shows discovered skills; running `norman run "use docker to build an image"` activates the Docker skill's tools into context.

- Scan configured discovery paths (`~/.norman/skills/`, `/usr/share/norman/skills/`, per-workspace `skills/`) for valid skill definitions (directory with `SKILL.md`).
- Load **metadata only** at startup. Tools NOT loaded into context until needed.
- On demand: planning loop determines a task needs a specific skill → inject that skill's tools into the active prompt.
- Deactivate unused skills to reclaim token budget.

### Completion Notes
- `pkg/skills/discover.go` — scans paths, parses SKILL.md frontmatter, returns metadata-only results.
- `pkg/skills/activate.go` — manages active/inactive skill state; injects tool definitions into context; auto-deactivates least-recently-used skills when at capacity.
- `cmd/norman/main.go` — `skills list/install/remove` commands fully wired to discovery and file system operations.
- `pkg/config/config.go` — added `SkillsConfig` struct with `DiscoveryPaths` and `MaxActive` fields; defaults applied in `DefaultConfig()`.
- Installed skills use frontmatter `name` field for directory naming; falls back to basename if missing.
- `go build ./...` passes cleanly (exit 0).

---

## Step 5 — Agent Server + Task API implementation

> **Status: Split into two sub-steps**

### Step 5a — Agent Server Skeleton & Protocol Wiring

> **Status: ✅ COMPLETE** (committed on `step5a-server` branch)

- Bootstrap Echo server in `cmd/norman-server/main.go`; register all generated handlers from `internal/api/handlers.go`
- Add `/health` endpoint; start WebSocket hub at `/ws` for event broadcasting
- Wire up `pkg/protocol/client.go` + `types.go` into server's internal state management
- Add ephemeral in-memory task store (map keyed by task ID)

**Acceptance:** `norman server --port 8080` starts; `curl /health` returns 200; WebSocket at `/ws` accepts connections and pushes mock events.

---

### Step 5b — Task API + Execution Engine

> **Status: ✅ COMPLETE** (committed on `step5b-tasks` branch)

- `pkg/agent/loop.go` — core agent loop with task submission, execution lifecycle (resolve→create sandbox→waitReady→execute→cleanup), retry logic (3× exponential backoff), WebSocket push events
- `pkg/memory/store.go` — file-based persistence with optimistic locking; save/load/delete tasks and sessions; prune expired entries
- `cmd/norman-server/main.go` — all 20 handlers wired to real implementations (not stubs); server struct holds Loop + Store + WSHub
- `pkg/skills/activate.go` — added `Registry()` method for agent loop access
- WebSocket event push: `task_progress`, `task_complete`, `task_failed`

**Acceptance:** Server starts, `/health` returns 200; `/api/tasks` returns task list; `POST /api/tasks` creates and executes a task; WebSocket clients receive real-time progress events. Build passes (`go build ./cmd/norman-server/`), `go vet` clean on all packages.

---

### API Spec & Code Generation (shared by 5a + 5b)

> **Status: ✅ COMPLETE**

- `api-spec.yaml` (OpenAPI 3.1) is the single source of truth for all client-server protocol types, endpoints, and WebSocket event schemas.
- Regenerated via `make generate` → produces `pkg/protocol/types.go`, `pkg/protocol/client.go`, and `internal/api/handlers.go`.
- Verified: generated code is up-to-date with spec (zero diff).

**Acceptance:** `make generate` + `go build ./...` succeeds; all 20 handlers match endpoints defined in the spec.

## Step 6 — Memory engine (LTM wiki) + git versioning

> **Status: ✅ COMPLETE** (merged to `main`)

See implementation plan: [`docs/step6-plan.md`](docs/step6-plan.md).

**Acceptance:** After running tasks, `.norman/memory/index.md` is auto-generated; `git log -- .norman/` shows history of state changes.

- Flat markdown pages in `.norman/memory/` with frontmatter (`type: entity|source|trend|task`).
- Auto-generated `index.md` (page → type + tags mapping). Append-only `log.md`. `schema.md` / `AGENTS.md` with procedural rules.
- Git-tracked inside the environment (`git add`, `git commit`, `git diff`).
- Use `github.com/src-d/go-git` (4871 stars) or shell out to `git` CLI.
- **No vector search at MVP** — grep/glob + frontmatter metadata suffices.

## Step 7 — Context budget & compression cycle

> **Status: ✅ Step 7 COMPLETE** (all sub-steps 7a-7d done; budget tracker, compressor, Loop integration, session archival + CLI endpoint) — see [`docs/step7-plan.md`](docs/step7-plan.md).

**Acceptance:** Running a long conversation triggers automatic summarization when token budget hits 75%/90%; original transcripts archived in `memory/sessions/`.

- Track token usage via LLM API's `usage.token_count` field (accumulated across all messages).
- At 75%: GPT-style summarization cycle (summarize oldest N turns → replace in STM → archive full transcripts to LTM).
- At 90%: forced aggressive compression.
- Selective retention: original request, active workspace config, current plan steps, pinned entries always preserved.
- Use `github.com/pkoukk/tiktoken-go` (955 stars) or estimate tokens from API response metadata.

## Step 8 — MCP integration + task scheduling + notifications

> **Status:** ✅ COMPLETE — all sub-steps (8a-8d) implemented and tested. See [docs/step8-plan.md](docs/step8-plan.md) for details.

**Acceptance:** A skill declares an MCP connection → tools appear in context; a recurring task registered via `norman tasks add --schedule "*/5 * * * *" health-check` fires automatically and pushes results to the CLI.

- Each skill can declare MCP connections (`mcp:` section with `endpoint`, `capabilities[]`).
- Use `github.com/modelcontextprotocol/go-sdk/mcp` (5206 stars, official).
- Register recurring tasks with OpenShell's cron scheduler (5-field or 6-field expressions).
- Server pushes results to connected clients via WebSocket; terminal bell + Slack webhook at MVP.
- Rate limiting: max 10 notifications/minute across all tasks.

---

## Library Summary (Go)

| Purpose | Library | Stars | Notes |
|---------|---------|-------|-------|
| YAML parsing + secret placeholders | `go-yaml/yaml/v3` | 7017 | Standard, battle-tested |
| MCP client | `github.com/modelcontextprotocol/go-sdk/mcp` | 5206 | Official, Google-backed |
| Git operations | `github.com/src-d/go-git` | 4871 | Or shell out to `git` CLI |
| WebSocket | `github.com/gorilla/websocket` | 24872 | Mature, well-maintained |
| Token counting | `github.com/pkoukk/tiktoken-go` | 955 | OpenAI tokenizer port |
| HTTP server | stdlib `net/http` | — | No framework needed for MVP |

## OpenShell Integration

- Uses the official Go SDK (`github.com/NVIDIA/OpenShell/sdk/go v0.0.0-20261013154426-dfb0f4f91761`) — currently pinned to a commit SHA (not yet a tagged release).
- `pkg/env/` provides direct SDK bindings: sandbox lifecycle (`CreateSandbox`, `WaitReady`, `GetSandbox`, `DeleteSandbox`), command execution (`Exec().Run()`), and session caching with crash recovery (3× retry, exponential backoff 1s→2s→4s→8s).
- All secrets resolved by OpenShell at runtime via `v1.SecretClient`.

**Note:** The SDK is not yet publicly tagged. If the Go SDK becomes available as a stable release, swap from commit-sha pin to versioned import.

---

## Step 10 — Server rename + client-server communication review

> **Status:** ✅ COMPLETE (merged to `main`)

### Changes Made
- Renamed `cmd/server/` → `cmd/norman-server/` to avoid naming clash with other applications
- Refactored CLI client (`cmd/norman/main.go`) to be a **thin HTTP client** — delegates operations to the server instead of assuming local environment access:
  - `listSkills()` → now uses `GET /api/skills` (was scanning host filesystem)
  - `installSkill()` → now uses `POST /api/skills/install` (was copying files locally)
  - `removeSkill()` → now uses `DELETE /api/skills/:name` (was removing locally)
  - `handleList()` workspace listing → now uses `GET /api/workspaces` (was `os.ReadDir`)
  - `handleRun()` → now uses `POST /api/tasks` (was direct OpenShell SDK exec)
  - `handleCreate()` → now uses `POST /api/workspaces` (was creating local overlay dirs)
  - `handleSwitch()` → now uses `GET /api/workspaces/:name` (was constructing local paths)

### Design Rationale
The CLI is a thin application. It should not assume access to the local environment (e.g., when running in a container or remote SSH session). All file I/O, sandbox lifecycle, and skill management should happen on the server side via REST/WebSocket endpoints.

---

### Changes Made
- Renamed `cmd/server/` → `cmd/norman-server/` to avoid naming clash with other applications
- Refactored CLI client (`cmd/norman/main.go`) to be a **thin HTTP client** — delegates operations to the server instead of assuming local environment access:
  - `listSkills()` → now uses `GET /api/skills` (was scanning host filesystem)
  - `installSkill()` → now uses `POST /api/skills/install` (was copying files locally)
  - `removeSkill()` → now uses `DELETE /api/skills/:name` (was removing locally)
  - `handleList()` workspace listing → now uses `GET /api/workspaces` (was `os.ReadDir`)
  - `handleRun()` → now uses `POST /api/tasks` (was direct OpenShell SDK exec)
  - `handleCreate()` → now uses `POST /api/workspaces` (was creating local overlay dirs)
  - `handleSwitch()` → now uses `GET /api/workspaces/:name` (was constructing local paths)

### Design Rationale
The CLI is a thin application. It should not assume access to the local environment (e.g., when running in a container or remote SSH session). All file I/O, sandbox lifecycle, and skill management should happen on the server side via REST/WebSocket endpoints.

---

## Step 9 — Follow-up: real SDK, scheduler wiring, CLI archive, config tests

> **Status:** ✅ COMPLETE (merged to `main`)

All sub-steps implemented, tested, and merged. See [docs/step9-plan.md](docs/step9-plan.md) for details.

| # | Item | Status |
|---|------|--------|
| 9a | Wire real OpenShell SDK into `OpenShellClient` interface via `SDKAdapter` | ✅ Done |
| 9b | Connect scheduler `Start()` to `main()` and inject results channel into agent loop's notification path | ✅ Done |
| 9c | Add CLI command `norman sessions archive <task_id>` for manual archival (REST endpoint exists, CLI added) | ✅ Done |
| 9d | Add unit tests for `pkg/config/config.go` — `NotifyConfig` defaults validation (7 tests pass) | ✅ Done |

**Implementation notes:**
- `pkg/env/sdk_adapter.go` wraps `*env.Client` and adapts method signatures to match `OpenShellClient` interface; falls back to `FakeOpenShellClient` when SDK unavailable.
- Scheduler results channel (`chan scheduler.TaskResult`) injected into Loop via `WithResultsChannel()`.
- CLI command `norman sessions archive <task_id>` sends POST `/api/sessions/archive`.
- Config tests cover defaults, validation (zero/negative rate limits), and YAML unmarshal.

---

## Step 11 — Build system cleanup

> **Status:** ✅ COMPLETE (merged to `main`)

Replaced ad-hoc `go build` with a proper Makefile pipeline and cleaned up stale artifacts.

| # | Item | Status |
|---|------|--------|
| 11a | Add `help` target documenting all make targets | ✅ Done |
| 11b | Generate protocol types, client library, server handlers from `api-spec.yaml` via oapi-codegen | ✅ Done |
| 11c | Add missing API schemas (`ArchiveSessionRequest`, `NotificationEntry`, `NotificationCreateRequest`) and `session_compressed` enum to spec; wire REST endpoints | ✅ Done |
| 11d | Remove generated `.go` files from git tracking; use `.gitignore` + `make generate` | ✅ Done |
| 11e | Output compiled binaries to `dist/` (not repo root); add `dist/` to `.gitignore` | ✅ Done |
| 11f | Remove stale `server/` directory from previous rename refactor | ✅ Done |

**Verification:** `make clean → make build → make test` full cycle works with clean working tree.

---

## Step 13 — Logging verbosity

> **Status:** 🚧 IN PROGRESS (on branch `step13-logging-config`)

Made CLI and server logging configurable with a verbose mode for debugging. By default, the server's structured JSON log lines no longer leak into user output; verbose mode re-enables them plus prints request details to stderr.

| # | Item | Status |
|---|------|--------|
| 13a | Add `LoggingConfig` struct (`Verbose bool`) to `pkg/config/config.go` | ✅ Done |
| 13b | Add `-v` / `--verbose` flag to CLI client; print `[debug] METHOD URL` before each request in verbose mode | ✅ Done |
| 13c | Server: conditionally apply `middleware.Logger()` based on `cfg.Logging.Verbose` | ✅ Done |
| 13d | Add `LOGGING_VERBOSE` env var support (true/false or 1/0) | ✅ Done |
| 13e | Update `.env.example` with new variables; update help text | ✅ Done |

**Before:** Running `norman list` produced a confusing JSON log line followed by the actual output:
```
{"time":"2026-10-14T15:56:06.468839413-04:00","id":"","remote_ip":"127.0.0.1",...}
No workspaces found.
```

**After (default):** Clean output — no JSON noise:
```
Available workspaces:
  my-project        /home/user/workspaces/my-project
```

**After (`norman -v list`):** Debug info printed to stderr:
```
[debug] GET http://127.0.0.1:8080/api/workspaces
Available workspaces:
  my-project        /home/user/workspaces/my-project
```

---

## MVP Scope Boundaries

**In scope:** CLI client → HTTP/WebSocket → Agent Server → OpenShell environment → task execution → results returned to client. One active workspace per session. Flat markdown wiki. Cron scheduling via OpenShell. Slack + terminal bell notifications.

**Deferred:** Multi-workspace parallel environments, vector search, automatic raw-source ingestion loop, multi-agent coordination, skill versioning/dependency resolution, IDE integration, email notifications.


---

## Step 14 — Assessment Review & Remediation

> **Status:** 🚧 IN PROGRESS (on branch `step14-assessment`)
>
> Comprehensive review of requirements.md, glossary.md, docs/step*.md, api-spec.md, and current codebase against the implementation. No changes made yet in this commit — only project plan update.

### Findings Summary (from 2026-10-15 review)

| # | Issue | Severity | Effort | Priority |
|---|-------|----------|--------|----------|
| 14a | Stale config path `~/.config/norman/config.yaml` in glossary.md and api-spec.md | Low | Quick win | High | ✅ DONE |
| 14b | Session archive format inconsistency: `SaveTask` → `.json`, `ArchiveSession` → `.jsonl` | Medium | Small fix | Medium | ✅ DONE |
| 14c | TitleToSlug collision risk: two entries with same title but different types would collide on disk (e.g., "Docker" as entity vs task) | Low | Design decision | Low | ✅ DONE — type suffix appended to slug |
| 14d | `schema.md` read/write + git operations not implemented in wiki engine | Medium | Implementation | Medium |
| 14e | Plan persistence missing from agent loop — only tracked in memory | Medium | Implementation | Medium |
| 14f | Raw Sources layer as distinct abstraction over environment reads — not yet implemented | Low | Design | Deferred |
| 14g | Budget threshold checks exist but not actively triggered in planning loop | Low | Integration | Low |
| 14h | Workspace registry is in-memory only (no disk persistence) | Medium | Implementation | Low |
| 14i | Skill discovery paths host-FS-bound; should become server-driven via config | Medium | Refactor | Deferred |

### Sub-steps (status as of 2026-10-15)

#### 14a ✅ DONE — Update stale docs config path references
- [x] `api-spec.md` §1: replaced `~/.config/norman/config.yaml` → `~/.norman/config.yaml`
- [x] `glossary.md`: no stale references found (already clean)

#### 14b ✅ DONE — Session format alignment
- [x] `ArchiveSession` now stores `.json` (JSON array) instead of `.jsonl`
- [x] Tests updated: verify JSON array semantics, overwrite on multiple calls
- [x] Empty sessions produce valid empty JSON arrays

#### 14c ✅ DONE — TitleToSlug collision mitigation
- [x] `TitleToSlug(title, EntryType)` appends type as suffix (e.g., "Docker" + entity → "docker-entity")
- [x] All callers updated: engine.go Create/PruneStale, version.go Create
- [x] Collision test added verifying same-title/different-type produces distinct slugs

#### 14d — Schema.md read/write + git operations in wiki engine (PENDING)
- [ ] Add `WriteSchema(slug, content)` to `pkg/wiki/engine.go`
- [ ] Add `ReadSchema(slug) -> string` accessor
- [ ] Wire `git add/commit/diff` into the engine (use `go-git` or shell out)

#### 14e — Plan persistence in agent loop (PENDING)
- [ ] Add `SavePlan(planID, content)` and `LoadPlan(planID) -> string` to loop
- [ ] Persist plan as `.json` or markdown in store directory
- [ ] Integrate with budget compression (preserve plan during truncation)

#### 14f — Raw Sources layer (DEFERRED)
- [ ] Design: distinct abstraction between environment reads and wiki entries
- [ ] Implement `pkg/memory/raw_sources.go` as read-only snapshot store
- [ ] Wire into agent loop for context injection

#### 14g — Budget threshold integration into planning loop (LOW PRIORITY)
- [ ] Check thresholds at start of each planning cycle (not just after API calls)
- [ ] Trigger summarization when `WarnThreshold` hit, not just on budget tracker
- [ ] Log threshold events to notification path

#### 14h — Workspace registry disk persistence (PENDING)
- [ ] Persist `workspaces map[string]*protocol.Workspace` to JSON file in store
- [ ] Load on server startup; save on CreateWorkspace/DeleteWorkspace
- [ ] Handle concurrent access with lock (already has sync.Mutex)

#### 14i — Server-driven skill discovery paths (DEFERRED)
- [ ] Move `DiscoveryPaths` from host-FS scan to config-driven list
- [ ] Server reads paths at startup, caches in memory
- [ ] CLI sends path additions via POST /api/skills/paths (new endpoint?)
---

### MVP Completion Criteria (updated)

| Criterion | Status | Notes |
|-----------|--------|-------|
| Client → Server config sync | ✅ | PUT/GET /api/config working |
| Workspace CRUD | ✅ | In-memory registry; disk persistence pending (14h) |
| Task execution + WebSocket push | ✅ | All 24 endpoints implemented |
| Wiki engine (flat markdown) | ✅ | Frontmatter, index, log.md working |
| Schema.md support | ❌ | Pending (14d) |
| Plan persistence | ❌ | In-memory only; pending (14e) |
| Session archival + budget compression | ✅ | Threshold checks exist but not loop-integrated (14g) |
| MCP integration | ✅ | Skills declare endpoints, tools injected on demand |
| Scheduler + notifications | ✅ | Cron registration, WS push, bell, Slack |
| OpenShell mTLS auth | ✅ | NewClientWithTLS with ca.crt/tls.crt/tls.key |
| Config unified under ~/.norman/ | ✅ | All paths resolved (14a) |

---
