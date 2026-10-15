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

> **Status: ✅ COMPLETE**

See **[docs/step5-plan.md](docs/step5-plan.md)** for full architecture, sub-steps (5a–5b), and acceptance criteria.

- **5a** — Echo server skeleton, `/health`, WebSocket hub at `/ws`, protocol wiring via `pkg/protocol`.
- **5b** — Agent loop (`pkg/agent/loop.go`), task store (`pkg/memory/store.go`), 20 handlers wired; WebSocket push events.
- **Shared** — `api-spec.yaml` (OpenAPI 3.1) is source of truth; `make generate` produces types, client, handlers.

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

## Step 9 — Follow-up: real SDK, scheduler wiring, CLI archive, config tests

> **Status:** ✅ COMPLETE (merged to `main`)

See **[docs/step9-plan.md](docs/step9-plan.md)** for full architecture and sub-steps.

- **9a** — `pkg/env/sdk_adapter.go` wraps `*env.Client`; falls back to `FakeOpenShellClient`.
- **9b** — Scheduler `Start()` wired into `main()`; results channel injected into Loop via `WithResultsChannel()`.
- **9c** — CLI command `norman sessions archive <task_id>` (POST `/api/sessions/archive`).
- **9d** — 7 unit tests for `NotifyConfig` defaults/validation in `pkg/config/config_test.go`.

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

## Step 11 — Build system cleanup

> **Status:** ✅ COMPLETE (merged to `main`)

See **[docs/step11-plan.md](docs/step11-plan.md)** for full architecture and sub-steps.

- **11a** — `Makefile` with `help`, `generate`, `build`, `test`, `vet`, `clean` targets.
- **11b** — `oapi-codegen` regeneration from `api-spec.yaml`.
- **11c** — Added `ArchiveSessionRequest`, `NotificationEntry`, `NotificationCreateRequest` schemas + `session_compressed` enum.
- **11d** — Generated `.go` files removed from git; `.gitignore` entries added.
- **11e** — Binaries output to `dist/`; repo root kept clean.
- **11f** — Stale `server/` directory removed (renamed to `cmd/norman-server/`).

Verification: `make clean → make build → make test` full cycle works with clean working tree.

Full plan with data models, integration points, and risks: [docs/step11-plan.md](docs/step11-plan.md).

---

## Step 13 — Logging verbosity

> **Status:** ✅ COMPLETE (merged to `main`)

See **[docs/step13-plan.md](docs/step13-plan.md)** for full architecture and sub-steps.

- **13a** — `LoggingConfig` struct (`Verbose bool`) added to `pkg/config/config.go`.
- **13b** — `-v`/`--verbose` CLI flag; prints `[debug] METHOD URL` to stderr before each request.
- **13c** — Server conditionally applies `middleware.Logger()` based on `cfg.Logging.Verbose`.
- **13d** — `LOGGING_VERBOSE` env var support (`true`/`false`/`1`/`0`).
- **13e** — `.env.example` updated; help text documented.

Before: `norman list` → confusing JSON log line + output. After (default): clean output. After (`-v`): `[debug] GET ...` to stderr.

---

## Step 14 — Assessment Review & Remediation

> **Status:** ✅ COMPLETE (merged to `main`)

See **[docs/step14-plan.md](docs/step14-plan.md)** for full architecture, all 9 sub-steps (14a–14i), data models, acceptance criteria, and risks.

### Findings Summary (from 2026-10-15 review)

| # | Issue | Severity | Effort | Priority |
|---|-------|----------|--------|----------|
| 14a | Stale config path `~/.config/norman/config.yaml` in glossary.md and api-spec.md | Low | Quick win | High | ✅ DONE |
| 14b | Session archive format inconsistency: `SaveTask` → `.json`, `ArchiveSession` → `.jsonl` | Medium | Small fix | Medium | ✅ DONE |
| 14c | TitleToSlug collision risk: two entries with same title but different types would collide on disk (e.g., "Docker" as entity vs task) | Low | Design decision | Low | ✅ DONE — type suffix appended to slug |
| 14d | `schema.md` read/write + git operations not implemented in wiki engine | Medium | Implementation | Medium | ✅ DONE — WriteSchema/ReadSchema added; GitDiff/GitCommit wired via exec.Command |
| 14e | Plan persistence missing from agent loop — only tracked in memory | Medium | Implementation | Medium | ✅ DONE — SavePlan/LoadPlan added; budget compression preserves plan state |
| 14f | Raw Sources layer as distinct abstraction over environment reads — not yet implemented | Low | Design | Low | ✅ DONE — pkg/memory/raw_sources.go implements read-only snapshot store; context injection ready |
| 14g | Budget threshold checks exist but not actively triggered in planning loop | Low | Integration | Low | ✅ DONE — threshold check at cycle start; notify path logging; idempotent compression |
| 14h | Workspace registry is in-memory only (no disk persistence) | Medium | Implementation | Low | ✅ DONE — workspaces persisted to disk; loaded on startup; deleted on removal |
| 14i | Skill discovery paths host-FS-bound; should become server-driven via config | Medium | Refactor | Low | ✅ DONE — AddDiscoveryPaths method on Loop; POST /api/skills/paths endpoint; config-driven paths |

---

## MVP Scope Boundaries

**In scope:** CLI client → HTTP/WebSocket → Agent Server → OpenShell environment → task execution → results returned to client. One active workspace per session. Flat markdown wiki. Cron scheduling via OpenShell. Slack + terminal bell notifications.

**Deferred:** Multi-workspace parallel environments, vector search, automatic raw-source ingestion loop, multi-agent coordination, skill versioning/dependency resolution, IDE integration, email notifications.

---

### MVP Completion Criteria (updated)

| Criterion | Status | Notes |
|-----------|--------|-------|
| Client → Server config sync | ✅ | PUT/GET /api/config working |
| Workspace CRUD | ✅ | Persisted to disk on Create/Delete; loaded on startup (14h) |
| Task execution + WebSocket push | ✅ | All 24 endpoints implemented |
| Wiki engine (flat markdown) | ✅ | Frontmatter, index, log.md working |
| Schema.md support | ✅ | WriteSchema/ReadSchema added; git diff/commit wired (14d) |
| Skill discovery paths | ✅ | Config-driven via `DiscoveryPaths`; POST /api/skills/paths for runtime expansion (14i) |
| Plan persistence | ✅ | SavePlan/LoadPlan added; plan survives budget compression (14e) |
| Session archival + budget compression | ✅ | Threshold checks exist but not loop-integrated (14g) |
| MCP integration | ✅ | Skills declare endpoints, tools injected on demand |
| Scheduler + notifications | ✅ | Cron registration, WS push, bell, Slack |
| OpenShell mTLS auth | ✅ | NewClientWithTLS with ca.crt/tls.crt/tls.key |
| Config unified under ~/.norman/ | ✅ | All paths resolved (14a) |

---
