# Norman — MVP Implementation Plan



> **Goal:** An end-to-end CLI client → Agent Server workflow where a user can configure an agent, create workspaces, install skills, and run tasks.
>
> **Principle:** Minimize custom code. Compose capabilities from existing tools (OpenShell, git, MCP, shell utilities). The server is the only substantial Go implementation (~5–8k lines).

---

## Vision Summary

Norman is a **chat agent**. You talk to it in a session within a workspace. Tasks and scheduling are *emergent* from conversation, not manually triggered. The primary interaction model is interactive conversation — everything else exists to support it.

## Package Layout (MVP)

```
norman/
  cmd/
    norman/           # CLI client binary (thin HTTP client)
    norman-server/    # Agent Server binary
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

---

## Completed Steps

### Step 1 — Project scaffolding & module setup ✅

- `go mod init github.com/norman/norman`; package tree compiles with `go build ./...`.
- Runtime dependencies declared: `yaml.v3`, `go-sdk/mcp`, `gorilla/websocket`, `tiktoken-go`, `go-git`.

### Step 1.5 — API Spec & Generation ✅

- `api-spec.yaml` (OpenAPI 3.1) is the single source of truth for client-server protocol.
- Three generation targets: types (`types.go`), client library (`client.go`), server handlers (`handlers.go`).
- Regenerate with `make generate`.

### Step 2 — Config parser + CLI scaffolding ✅

- Parse YAML from `~/.config/norman/config.yaml` (or env var `NORMAN_CONFIG`).
- Do **not** resolve `${secret:...}` at parse time; resolve inside the environment via OpenShell's `get-secret`.
- CLI subcommands: `create`, `switch`, `run`, `list`, `config`, `skills list/install/remove`, `sessions`, `tasks`.

### Step 3 — OpenShell environment integration ✅

- One persistent OpenShell session per workspace via the OpenShell Go SDK v0.1.x.
- All file I/O goes through OpenShell's tools layer (`read`, `write`, `edit`) — never direct host OS access.
- Session lifecycle: create on `norman workspace create`, reuse across tasks, recover if crashed (3× retry, exponential backoff).

### Step 4 — Skill discovery & progressive activation ✅

- Scan configured discovery paths for valid skill definitions (`SKILL.md`).
- Load **metadata only** at startup; inject tools into context on demand.
- Deactivate unused skills to reclaim token budget.

### Step 5 — Agent Server + Task API implementation ✅

- Echo server skeleton, `/health`, WebSocket hub at `/ws`, protocol wiring via `pkg/protocol`.
- Agent loop (`pkg/agent/loop.go`), task store (`pkg/memory/store.go`), 24 handlers wired; WebSocket push events.

### Step 6 — Memory engine (LTM wiki) + git versioning ✅

- Flat markdown pages in `.norman/memory/` with frontmatter (`type: entity|source|trend|task`).
- Auto-generated `index.md` (page → type + tags mapping). Append-only `log.md`. `schema.md` / `AGENTS.md` with procedural rules.
- Git-tracked inside the environment; no vector search at MVP — grep/glob + frontmatter metadata suffices.

### Step 7 — Context budget & compression cycle ✅

- Track token usage via LLM API's `usage.token_count` field.
- At 75%: GPT-style summarization (summarize oldest N turns → replace in STM → archive full transcripts to LTM).
- At 90%: forced aggressive compression. Selective retention: original request, active workspace config, current plan steps, pinned entries always preserved.

### Step 8 — MCP integration + task scheduling + notifications ✅

- Skills declare MCP connections (`mcp:` section with `endpoint`, `capabilities[]`).
- Register recurring tasks with OpenShell's cron scheduler (5-field or 6-field expressions).
- Server pushes results to connected clients via WebSocket; terminal bell + Slack webhook at MVP. Rate limiting: max 10 notifications/minute.

### Step 9 — Follow-up: real SDK, scheduler wiring, CLI archive, config tests ✅

- `pkg/env/sdk_adapter.go` wraps `*env.Client`; falls back to `FakeOpenShellClient`.
- Scheduler `Start()` wired into `main()`; results channel injected into Loop via `WithResultsChannel()`.
- CLI command `norman sessions archive <task_id>` (POST `/api/sessions/archive`).
- 7 unit tests for `NotifyConfig` defaults/validation.

### Step 10 — Server rename + client-server communication review ✅

- Renamed `cmd/server/` → `cmd/norman-server/`.
- CLI is now a **thin HTTP client** — delegates operations to the server:
  - `listSkills()` → `GET /api/skills`
  - `installSkill()` → `POST /api/skills/install`
  - `removeSkill()` → `DELETE /api/skills/:name`
  - `handleList()` workspace listing → `GET /api/workspaces`
  - `handleRun()` → `POST /api/tasks`
  - `handleCreate()` → `POST /api/workspaces`
  - `handleSwitch()` → `GET /api/workspaces/:name`

### Step 11 — Build system cleanup ✅

- `Makefile` with `help`, `generate`, `build`, `test`, `vet`, `clean` targets.
- `.gitignore` entries for generated `.go` files and build artifacts; binaries output to `dist/`.
- Stale `server/` directory removed.

### Step 13 — Logging verbosity ✅

- `-v`/`--verbose` CLI flag; prints `[debug] METHOD URL` to stderr before each request.
- Server conditionally applies `middleware.Logger()` based on config.
- `NORMAN_VERBOSE` env var support (`true`/`false`/`1`/`0`).

### Step 14 — Assessment Review & Remediation ✅

All 9 sub-steps (14a–14i) implemented:
| # | Issue | Resolution |
|---|-------|------------|
| 14a | Stale config path in glossary.md / api-reference.html | Updated to `~/.norman/config.yaml` |
| 14b | Session archive format inconsistency (`.json` vs `.jsonl`) | Unified to `.jsonl` |
| 14c | TitleToSlug collision risk | Type suffix appended to slug |
| 14d | `schema.md` read/write + git operations | `WriteSchema`/`ReadSchema` added; GitDiff/GitCommit wired |
| 14e | Plan persistence missing from agent loop | `SavePlan`/`LoadPlan` added |
| 14f | Raw Sources layer not implemented | `pkg/memory/raw_sources.go` — read-only snapshot store |
| 14g | Budget threshold checks not loop-integrated | Threshold check at cycle start; idempotent compression |
| 14h | Workspace registry in-memory only | Persisted to disk; loaded on startup |
| 14i | Skill discovery paths host-FS-bound | `AddDiscoveryPaths` method; `POST /api/skills/paths` endpoint |

---

## Phase 0 — Interactive Chat CLI (P0)

**Goal:** Provide the REPL experience that makes the chat-first model usable. This is the *primary* interaction layer — everything else exists to support it.

| CLI Command | Purpose | Notes |
|-------------|---------|-------|
| `norman chat --workspace <name>` | Enter REPL chat mode for a session | Primary interaction; blocks until session closed |
| `/help` | Show available slash-commands | In-chat help |
| `/list tasks` | List emergent scheduled tasks for current session | Read-only, in-chat |
| `/close` | Close the current session (save transcript) | Graceful shutdown |
| `/new` | Start a new chat session (auto-create or prompt) | Creates new session ID |

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

---

## OpenShell Integration

- Uses the official Go SDK (`github.com/NVIDIA/OpenShell/sdk/go v0.0.0-20261013154426-dfb0f4f91761`) — currently pinned to a commit SHA (not yet a tagged release).
- `pkg/env/` provides direct SDK bindings: sandbox lifecycle (`CreateSandbox`, `WaitReady`, `GetSandbox`, `DeleteSandbox`), command execution (`Exec().Run()`), and session caching with crash recovery (3× retry, exponential backoff 1s→2s→4s→8s).
- All secrets resolved by OpenShell at runtime via `v1.SecretClient`.

---

## Scope Boundaries

**In scope:** CLI client → HTTP/WebSocket → Agent Server → OpenShell environment → task execution → results returned to client. One active workspace per session. Flat markdown wiki. Cron scheduling via OpenShell. Slack + terminal bell notifications.

**Deferred:** Multi-workspace parallel environments, vector search, automatic raw-source ingestion loop, multi-agent coordination, skill versioning/dependency resolution, IDE integration, email notifications.

---

## MVP Completion Criteria

| Criterion | Status | Notes |
|-----------|--------|-------|
| Client → Server config sync | ✅ | PUT/GET /api/config working |
| Workspace CRUD | ✅ | Persisted to disk on Create/Delete; loaded on startup (14h) |
| Task execution + WebSocket push | ✅ | All 24 endpoints implemented |
| Wiki engine (flat markdown) | ✅ | Frontmatter, index, log.md working |
| Schema.md support | ✅ | WriteSchema/ReadSchema added; git diff/commit wired (14d) |
| Skill discovery paths | ✅ | Config-driven via `DiscoveryPaths`; POST /api/skills/paths for runtime expansion (14i) |
| Plan persistence | ✅ | SavePlan/LoadPlan added; plan survives budget compression (14e) |
| Session archival + budget compression | ✅ | Loop-integrated: checkAndCompress called at planning cycle start (line 480) and post-sandbox provisioning (line 539); idempotent; archives transcript before truncation |
| MCP integration | ✅ | Skills declare endpoints, tools injected on demand |
| Scheduler + notifications | ✅ | Cron registration, WS push, bell, Slack |
| OpenShell mTLS auth | ✅ | NewClientWithTLS with ca.crt/tls.crt/tls.key |
| Config unified under ~/.norman/ | ✅ | All paths resolved (14a) |

---

## Future Iteration Path

After chat-first MVP:
- Multi-workspace parallel sessions
- Automatic tool detection/installation based on workspace type
- Git-aware operations (diff, commit suggestions)
- External service access (web search, API calls)
- Skill packaging and distribution (`norman install skill-name`)
- **Entity relationship graph** — knowledge graph of relationships between entities
- **Vector search** for semantic similarity over LTM entries
- **Automatic raw-source ingestion loop** — continuously watch and ingest changes

---

## Related Documents

- `requirements.md` — Full requirements spec (chat-first agent vision)
- `glossary.md` — Terminology reference
- `docs/llm-integration-plan.md` — Provider-agnostic LLM abstraction plan
- `docs/llm-tool-access-assessment.md` — Tool access assessment for environment integration
- `docs/memory-proposal.md` — Memory architecture enhancement proposal
- `docs/provider-config-drift-assessment.md` — Config drift assessment
- `docs/task-management-assessment.md` — Task management assessment
- `docs/workspace-definition-assessment.md` — Workspace definition plan
- `docs/workspace-skill-install-plan.md` — On-demand skill installation plan
- `docs/config-embedding-proposal.md` — Embedded default config proposal
- `docs/graphify-skill-install-assessment.md` — Graphify skill assessment
- `docs/roger-client-design.md` — Roger client design
