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

**Acceptance:** Running `norman config --help` shows subcommands; `norman create my-workspace` creates a workspace directory + `.openshell/` overlay.

- Parse YAML from `~/.config/norman/config.yaml` (or env var `NORMAN_CONFIG`).
- **Do not resolve `${secret:...}` at parse time.** Store raw placeholders; resolve inside the environment via OpenShell's `get-secret`.
- Validate required fields early. Provide sensible defaults (`gpt-4o`, port `8080`).
- CLI subcommands: `create <name>`, `switch <name>`, `run [prompt]`, `list`, `config`, `skills list/install/remove`, `sessions`, `tasks`.

## Step 3 — OpenShell environment integration

**Acceptance:** `norman create my-workspace` creates a persistent OpenShell session; running `norman run "echo hello"` executes inside that session and returns output.

- Create one persistent OpenShell session per workspace via the OpenShell CLI/API.
- All file I/O goes through OpenShell's tools layer (`read`, `write`, `edit`) — **never** direct host OS access.
- Check OpenShell version ≥ 0.1.0 on session creation; log warning if features are missing.
- Session lifecycle: create on `norman create`, reuse across tasks, recover if crashed (retry up to 3× with exponential backoff).

## Step 4 — Skill discovery & progressive activation

**Acceptance:** Running `norman skills list` shows discovered skills; running `norman run "use docker to build an image"` activates the Docker skill's tools into context.

- Scan configured discovery paths (`~/.norman/skills/`, `/usr/share/norman/skills/`, per-workspace `skills/`) for valid skill definitions (directory with `SKILL.md`).
- Load **metadata only** at startup. Tools NOT loaded into context until needed.
- On demand: planning loop determines a task needs a specific skill → inject that skill's tools into the active prompt.
- Deactivate unused skills to reclaim token budget.

## Step 5 — Agent Server + Task API

**Acceptance:** `norman run "write a test file"` creates a task, executes it in the environment, and returns results via HTTP/WebSocket to the CLI.

- Single long-running process exposing HTTP API (`POST/GET /api/tasks`, `GET /api/tasks/{id}/log`) + WebSocket (`WS /ws`).
- Stateless between invocations (rebuilds in-memory state from workspace `.norman/` files on startup).
- Uses optimistic file-based writes with a lock file (`memory/.lock`) for concurrency safety.

### API Spec → Code Generation

The full client-server protocol is formalized in `api-spec.yaml` (OpenAPI 3.1). Both the **client protocol library** (`pkg/protocol/`) and **server handlers** (`internal/api/handlers.go`) are generated from this spec using `oapi-codegen`, rather than hand-written. The spec covers:

- REST endpoints for workspace, session, task, skill, and scheduled-task management
- WebSocket push event types (task_progress, task_complete, task_failed, notification, context_budget_warning)
- Task completion message schema (Environment → Server contract)
- Go struct definitions aligned with the generated types

This ensures the spec is the **single source of truth** — any protocol change requires updating `api-spec.yaml`, and regeneration produces consistent client + server code.

## Step 6 — Memory engine (LTM wiki) + git versioning

**Acceptance:** After running tasks, `.norman/memory/index.md` is auto-generated; `git log -- .norman/` shows history of state changes.

- Flat markdown pages in `.norman/memory/` with frontmatter (`type: entity|source|trend|task`).
- Auto-generated `index.md` (page → type + tags mapping). Append-only `log.md`. `schema.md` / `AGENTS.md` with procedural rules.
- Git-tracked inside the environment (`git add`, `git commit`, `git diff`).
- Use `github.com/src-d/go-git` (4871 stars) or shell out to `git` CLI.
- **No vector search at MVP** — grep/glob + frontmatter metadata suffices.

## Step 7 — Context budget & compression cycle

**Acceptance:** Running a long conversation triggers automatic summarization when token budget hits 75%/90%; original transcripts archived in `memory/sessions/`.

- Track token usage via LLM API's `usage.token_count` field (accumulated across all messages).
- At 75%: GPT-style summarization cycle (summarize oldest N turns → replace in STM → archive full transcripts to LTM).
- At 90%: forced aggressive compression.
- Selective retention: original request, active workspace config, current plan steps, pinned entries always preserved.
- Use `github.com/pkoukk/tiktoken-go` (955 stars) or estimate tokens from API response metadata.

## Step 8 — MCP integration + task scheduling + notifications

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

## OpenShell Notes

- The official Go SDK (`nvidia-ai-everything/openshell-sdk-go`) exists but appears private/inaccessible via public API. **Strategy:** Use the OpenShell CLI (`openshell`) for session management at MVP, with a thin wrapper that calls `openshell` commands (read/write/edit/execute). If the Go SDK becomes available publicly, swap to direct API calls.
- All secrets resolved by OpenShell at runtime — no server-side secret store needed.

## MVP Scope Boundaries

**In scope:** CLI client → HTTP/WebSocket → Agent Server → OpenShell environment → task execution → results returned to client. One active workspace per session. Flat markdown wiki. Cron scheduling via OpenShell. Slack + terminal bell notifications.

**Deferred:** Multi-workspace parallel environments, vector search, automatic raw-source ingestion loop, multi-agent coordination, skill versioning/dependency resolution, IDE integration, email notifications.
