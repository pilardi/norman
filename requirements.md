# norman — Requirements Specification (MVP)

## 1. Purpose

A **thin client** + **Agent Server backbone** architecture that works on **tasks** by composing capabilities from externally-discovered or installed tools/skills. The *server* contains all orchestration logic; the *client* is a lightweight protocol endpoint (CLI, web UI, or custom). The same server can power a coding agent, a research assistant, a data analyst, or any other task-oriented agent simply by changing what's available in its discovery paths.

## 2. Core Principles

- **Thin client**: The *client* (CLI, web UI, or any interface) provides only a thin protocol layer for sending requests and receiving notifications. All orchestration logic — task planning, memory management, skill activation — lives in the **Agent Server** backbone. This keeps the client lightweight and portable.
- **Discoverable tools**: Tools and skills are not compiled into the binary. They are loaded at runtime from configured discovery paths, installed packages, or user-provided locations.
- **Task-oriented**: The app is agnostic to *what* it works on. "Project" is a workspace context; the agent's actual work depends on what tools/skills are available.
- **Minimal surface for MVP**: Start with the core loop — receive a task, discover available capabilities, compose a plan, execute using available tools, return results. Expand later.
- **Persistent state**: Workspaces are stateful. The agent remembers context across turns within a session.
- **Environment-isolated state**: All workspace metadata (`.norman/`) lives inside the OpenShell virtual environment — the host OS never sees it directly. State is only accessible through the environment's tools (read/write/edit).
- **Runtime**: The **Agent Server** is a Go binary (no interpreted languages). The client layer can be any language implementing the HTTP/WebSocket protocol. The agent's domain logic (planning, memory) is server-side and language-agnostic.

## 3. User Experience

### 3.1 Interaction Model
- **Clients**: The system supports multiple client types communicating with the Agent Server via HTTP/WebSocket:
  - CLI (terminal-based, like Claude CLI or OpenCode)
  - Web UI (browser-based dashboard)
  - Any custom client implementing the server protocol
- Conversational interface: user describes a task in natural language, agent works on it and returns results.
- Commands: `list`, `sessions create --workspace <name>`, `tasks send <prompt> --workspace <name> [--session <id>]`, `sessions close <id>`, `sessions list`. All interaction happens via sessions bound to workspaces; no global `switch` or bare `run`.
- **No host constraint**: Clients can run on any machine that can reach the server (local, remote, cloud). The client is purely a protocol endpoint — it does not need access to the workspace filesystem or environment.
- **Single-user identity (MVP)**: There is no authentication layer. Any client connecting to the server is treated as "the user." Workspace ownership and per-user session tracking are deferred; for MVP, all clients share the same logical identity against the server.

### 3.2 Workspace Model
- Multiple workspaces supported.
- **One active workspace per client session** (like conceptually `cd` into a workspace directory).
- Each workspace has its own persistent environment and available tools/skills.
- Workspaces are stateful — the agent remembers what it did, files created, changes made.
- A workspace is a directory + a set of configured tool/skill sources.
- **Multi-client per workspace**: The same workspace can be accessed concurrently by multiple clients (CLI, web UI, automation scripts), all sharing the same environment state.

## 4. Agent Capabilities

### 4.1 LLM Integration
- Uses an OpenAI-compatible API.
- **Configurable**: endpoint URL, model name — set via config file.
- **Secrets managed by OpenShell**: API keys, tokens, and credentials are injected into the environment by OpenShell at runtime. The agent never reads raw secrets from files or environment variables; it accesses them through OpenShell's secret management tools (e.g., `get-secret`, `list-secrets`).
- Does not assume OpenAI specifically; any OpenAI-compatible endpoint works.

### 4.2 Context Management

- The agent receives **context** before it starts working — this includes:
  - Workspace structure (file tree, key files like README, config files)
  - Custom instructions or domain knowledge the user provides
  - Prior conversation history / session state
- **Context is bounded by model context window size.** It must never exceed the LLM's token limit.
- The CLI layer implements a **context management strategy**: pruned or compressed over time as conversation grows.
- For MVP: automatic workspace scan (read file tree + key files) + user-provided text instructions.
- Context can be extended later (e.g., API docs, architecture diagrams).

### 4.3 Memory Architecture

The application maintains **three layers of memory**, each with a distinct purpose and lifecycle:

#### 4.3.1 Short-Term Memory (STM)
- The **active conversation context** visible to the LLM at any given turn.
- Contains: current task state, recent tool results, last N messages, active plan steps.
- **Size constraint**: bounded by the model's context window in **LLM tokens** (e.g., 128K tokens).
- **Token counting method**: Uses `usage.token_count` from LLM API responses (not character length). The server accumulates these counts across all messages in a session. Tool results that don't go through the LLM are estimated using the provider's token estimation heuristic (e.g., 1 token ≈ 4 characters for text, or provider-specific heuristics).
- **Eviction policy**: when approaching the limit, older or less-relevant content is compressed or summarized rather than simply dropped.
- For MVP: a sliding window of recent messages + tool results, with automatic summarization of earlier turns.

#### 4.3.2 Long-Term Memory (LTM)
- **Persistent knowledge** stored as files in the workspace's `.norman/memory/` directory, accessible on demand via retrieval.
- Stores: completed task outcomes, learned facts, user preferences, workspace history, skill usage patterns.
- Access pattern: **retrieval-augmented**. The agent queries LTM (reads relevant markdown/json files) and injects only what's needed into STM.
- For MVP: structured markdown or JSON documents in `.norman/memory/`, versioned by git. No vector search yet.
- Entries are automatically created when the agent completes a task or discovers a significant fact — written as a file, committed to git.
- Each LTM entry has metadata (timestamp, category, relevance score) stored as frontmatter in the markdown file.
- **Flat wiki entries**: All LTM entries are markdown files at the top level of `memory/`, classified by frontmatter `type:` field (`entity`, `source`, `trend`, `task`). No subdirectories needed. Example entry:
  ```yaml
  ---
  title: "Project Alpha"
  type: entity
  created: 2026-10-12
  updated: 2026-10-15
  tags: [api, backend]
  ---
  # Project Alpha

  A distributed API system handling...
  ```
- **Index catalog** (`index.md`): auto-generated mapping of page → type + tags for fast retrieval.
- **Chronological log** (`log.md`): append-only journal recording ingest/update/query events (not the actual content, just metadata).

#### 4.3.3 Context Control Mechanism
The system uses a **token budget** approach to manage STM:

1. **Token counting**: Each message/tool result is counted using the **LLM API's token count** (not text length in characters). The server tracks `response_token_count` from each API call and accumulates a running total for the active session. This aligns with how context windows are actually measured by LLM providers.

2. **Compression algorithm — GPT-style**: When budget reaches 75% of the window, the agent initiates a compression cycle using a GPT-style summarization approach:
   - **Step 1 (Summarize)**: The agent sends a prompt to the LLM asking it to summarize the oldest N turns into a compact bullet-point summary (~30% of original token count).
   - **Step 2 (Replace)**: The summarized entries replace the detailed raw turns in STM.
   - **Step 3 (Store)**: Original full transcripts are archived in LTM (`sessions/<id>/full.md`) for potential future retrieval.
   - This is an iterative process — each compression cycle reduces token count by ~60–70%, and the loop repeats until budget drops below 50%.

3. **Compression triggers**: When budget reaches 75% of the window, the agent initiates a compression cycle (see algorithm above). At 90%, a forced aggressive compression is applied.

4. **Selective retention**: Certain content is always preserved regardless of budget:
   - The user's original request (full text)
   - Active workspace config (`config.yaml`)
   - Current active plan steps
   - Any entry explicitly "pinned" by the user

5. **Manual intervention**: the user can pin specific entries or mark a conversation as "complete" to free budget.

#### 4.3.5 Schema / Procedural Memory
The **schema layer** (analogous to Karpathy's `AGENTS.md`) encodes procedural rules that govern how the agent maintains its wiki:

- Rules for avoiding duplicate pages, decaying stale trends, distinguishing one-off observations from reusable cross-event knowledge.
- Guidelines for updating existing canonical pages instead of creating clutter.
- Instructions for logging meaningful operations and keeping `index.md` current.
- This is the agent's "dreaming" / maintenance routine — not human-maintained, but rules the agent follows to keep its own memory coherent.

For MVP: a `memory/schema.md` file (or `AGENTS.md` at the workspace root) containing structured instructions for wiki maintenance. The schema is read at session start and referenced during maintenance cycles.

### 4.3.6 Memory-to-Context Flow
```
Raw Sources (immutable evidence)
        ↓ (ingest / summarize)
    Wiki (LTM: flat markdown pages + frontmatter type:)
        ↑ (retrieval on demand)
    STM (active conversation window + compression)
        ↓ (compression at 75% budget)
    Long-Term Memory (persistent store)
```

The agent **does not** dump everything into the context window. Instead, it:
- Keeps STM lean (active task + recent turns)
- Falls back to LTM retrieval when needed ("let me check what I learned about X")
- Writes completed results to LTM for future sessions
- Follows schema rules during maintenance (linting, updating stale entries, merging duplicates)

The architecture maps directly to Karpathy's three-layer model:
| Layer | Norman Location | Purpose |
|---|---|---|
| Raw Sources | `.openshell/` workspace files (read-only by agent) | Immutable evidence layer |
| Wiki | `.norman/memory/` | LLM-maintained knowledge base |
| Schema | `memory/schema.md` or `AGENTS.md` | Procedural rules for maintenance |

### 4.4 Skills System

#### 4.4.1 Design
- The agent has a **skills system** — reusable, composable capabilities it can activate on demand via progressive disclosure.
- Skills are **not hardcoded into the binary**. They are defined as external modules (scripts + instructions) that the server discovers and loads at runtime.
- A skill defines its own tool set: either built-in CLI tools (e.g., `ripgrep`, `sed`) or MCP connections to remote services. The skill's `SKILL.md` declares what it provides.
- **Progressive activation**: Skills are discovered (metadata scan) but their tools are only injected into the prompt when the planning loop determines they're needed for a specific task step. This prevents context bloat from loading all available capabilities at once.
- **Skill location is user-configurable** (specified in the config file). No assumed directory path.
- For MVP: a small built-in set of 3–5 skills + mechanism to add more without rebuilding the binary.

#### 4.4.2 Discovery Mechanism
Skills are loaded from one or more **discovery paths**:
- `$HOME/.norman/skills/` — user-installed skills
- System-level discovery paths (e.g., `/usr/share/norman/skills/`)
- Per-workspace skill directories (`<workspace>/skills/`)
- Package-managed skills (e.g., `pip install norman-skill-foo`, `go install github.com/foo/skill-bar@latest`)

The discovery mechanism uses **progressive disclosure**:
1. **Discover**: Scans configured paths for valid skill definitions (a directory with a `SKILL.md` or equivalent manifest). This is a lightweight metadata scan — tool signatures are read but not loaded into context.
2. **Activate on demand**: When the planning loop determines a task requires a specific skill, only that skill's tools are injected into the active prompt. Unused skills remain unloaded.
3. **Deactivate**: When a skill is no longer needed (task step complete), its tools are removed from context to reclaim token budget.

This ensures the agent's reasoning context is never cluttered with all available capabilities — it only loads what it actually needs for the current task.

### 4.5 MCP and Tooling
- The agent supports **MCP (Model Context Protocol)** connections as one way skills expose tools. A skill's manifest can declare:
  - `tools:` — a list of built-in CLI tools this skill provides (e.g., `ripgrep`, `sed`)
  - `mcp:` — MCP connection definitions that provide remote tool capabilities
- **Unified activation model**: Whether a tool comes from a skill's built-in tools or an MCP connection, it follows the same progressive disclosure lifecycle: discover → activate on demand → use → deactivate.
- **Selective activation**: the user or config decides which skills are available and which should auto-load vs. lazy-load. This prevents context bloat from unnecessary tool definitions.
- The server manages the lifecycle: connect (MCP) → activate (inject into prompt) → use → disconnect when no longer needed.
- For MVP: a small set of built-in MCP connections + ability to add custom ones via config without rebuilding the binary.

### 4.6 Built-in Tools (Core Set)
- The agent has a small set of **always-available core tools** that are loaded at session start:
  - **File read/write/edit**: via OpenShell's capabilities (`read`, `write`, `edit`)
  - **Search**: `ripgrep` / `grep` wrapper calls
  - **Git operations**: `git log`, `git diff`, `git checkout`
- These are the minimum surface — no skill activation needed. They provide the agent's basic ability to interact with the environment.
- **Skill-defined tools**: Skills declare their own tool sets (either built-in CLI tools or MCP connections). These follow progressive disclosure: discovered but not loaded into context until the planning loop decides they're needed for a task step.
- **Progressive disclosure summary**: 
  ```
  Core tools (always loaded): read, write, edit, rg, git
  Skill tools (on-demand):   web-search, docker, terraform, ... (per skill)
  MCP tools (on-demand):     external-service-a, database-query, ... (per MCP connection)
  ```
  Only the tools relevant to the current task are injected into the prompt. Unused skills' tool definitions stay out of context.

### 4.7 Decision Logic / Planning
- The agent uses a **planning loop**: observe → plan → act → observe → adjust.
- On receiving a user request, the agent breaks it into a sequence of steps (task decomposition).
- At each step, it decides:
  - Which **skill** to activate (and which of that skill's tools are needed)
  - Whether to load additional context from LTM before acting
  - Whether to ask the user a clarifying question (vs. just proceeding)
- **Progressive tool loading**: Before executing a step, the agent activates only the tools required for that step (e.g., if a step needs `docker`, it loads the Docker skill's MCP connection; if the next step only needs `git`, it deactivates Docker and keeps git). This is the core progressive disclosure mechanism.
- The agent can **self-correct**: if a tool call fails or results look wrong, it retries with adjusted parameters rather than blindly continuing.
- For MVP: the LLM itself drives the planning (prompted with structured instructions). No separate "planner" component.
- Planning state is kept in the conversation context; no persistent plan file needed for MVP.

### 4.8 Recurring Tasks & Scheduling

#### 4.8.1 Design
- The agent supports **long-running or recurrent tasks** that the user doesn't need to wait for:
  - A task can be scheduled to run at a specific time (cron expression)
  - Or on a recurring interval (e.g., "every 5 minutes", "daily at 9am")
- The agent does **not** implement its own scheduler. Instead, it registers tasks with OpenShell's built-in cron scheduler and receives notifications when scheduled jobs fire.
- This keeps the runtime thin — scheduling is delegated to OpenShell.

#### 4.8.2 Usage Model
```yaml
tasks:
  - name: "check-api-health"
    schedule: "*/5 * * * *"       # every 5 minutes (cron expression)
    skill: "monitoring.check-endpoint"
    config:
      url: "${secret:api-url}"
      timeout: 30s
    on_success: notify("slack", "#ops")
    on_failure: notify("slack", "#ops", priority: high)

  - name: "daily-report"
    schedule: "0 9 * * *"         # daily at 9am
    skill: "reports.generate-daily"
    config:
      output_dir: "./reports/"
```

- The agent can also **manually trigger** a scheduled task via the CLI (`norman run --task check-api-health`).
- Scheduled tasks run in the same environment as the active session, sharing access to workspace state and secrets.

#### 4.8.3 Lifecycle
1. User defines a recurring task (in config or interactively)
2. Agent registers it with OpenShell's cron scheduler
3. When the schedule fires, OpenShell notifies the agent
4. Agent executes the task using available tools/skills
5. Results are stored in `.norman/sessions/` and optionally pushed to configured destinations (Slack, email, file)
6. If a task fails, it can retry with backoff or alert the user

#### 4.8.4 Constraints
- Cron expressions follow standard 5-field format (`minute hour day month weekday`)
- Maximum concurrent scheduled tasks: configurable (default: 3 per workspace)
- Tasks are **stateless between runs** unless they explicitly read/write workspace files — each run starts fresh with the current environment state
- No task dependencies at MVP (e.g., "run B after A completes") — that's a future iteration

#### 4.9.1 Agent Server (Central Hub)
- A **single agent server process** runs on the host machine (or a remote machine — same-host is not required).
- The server exposes an HTTP/WebSocket API on a configurable port.
- Both the **environment** (running tasks) and the **client** (CLI, web UI, or any protocol-compatible interface) communicate with this server — they do not talk directly to each other.
- The task itself does **not** need to know about Slack, email, or notification channels. It simply sends its results/status to the agent server via HTTP POST or WebSocket message.
- **Thin client contract**: Clients implement a lightweight protocol (HTTP for requests/responses, WebSocket for real-time push). They contain no orchestration logic — they merely forward user input to the server and display server responses. A CLI replacement or web UI can be swapped without changing the server or environments.

#### 4.9.2 Communication Flow
```
┌─────────────────┐     HTTP/WebSocket      ┌──────────────────┐
│   Environment    │ ◄────────────────────► │  Agent Server    │
│ (OpenShell)      │                         │  (host/remote)   │
│                  │                         │                  │
│ • runs tasks     │                         │ • routes results │
│ • reads/writes   │                         │ • manages state  │
│ • writes results │                         │ • dispatches     │
└─────────────────┘                         └──────────────────┘
         ▲                                           │
         │                                           ▼
┌─────────────────┐     HTTP/WebSocket      ┌──────────────────┐
│   Clients       │ ◄────────────────────► │  Notification    │
│ (CLI, Web UI,   │                         │  Channels        │
│  custom)        │                         │ • terminal bell  │
│ • sends requests │                         │ • Slack/Discord  │
│ • polls status   │                         │ • email (future) │
│ • reads output   │                         └──────────────────┘
└─────────────────┘
```

- **Task → Server**: Task writes results/status via HTTP POST (`POST /api/tasks/{id}/complete`) or WebSocket message. No knowledge of notification channels needed.
- **Server → Client**: Server pushes updates to connected clients (CLI, web UI) via WebSocket.
- **Server → Channels**: Server routes notifications to configured destinations (Slack webhook, terminal bell). This is a server concern, not a task concern.

#### 4.9.3 Task API Contract (Environment → Server)
A task sends a completion message like:
```json
{
  "task_id": "daily-report-20261012",
  "status": "completed",
  "duration_ms": 134000,
  "output_path": "./reports/summary.md",
  "metadata": { "rows_processed": 1542 }
}
```

The task does **not** specify notification channels — that's the server's responsibility. The server decides where to push the result based on the task's configuration or global settings.

- **Task execution context**: Tasks run inside the OpenShell session for their workspace. They have access to the environment's file tree, installed tools, and secrets (injected by OpenShell). Results are sent back to the server via HTTP POST (`POST /api/tasks/{id}/complete`) or WebSocket message.

#### 4.9.4 Server → Client Push (WebSocket)
Connected clients receive real-time updates:
```json
{
  "event": "task_complete",
  "task_id": "daily-report-20261012",
  "message": "Task completed in 2m 14s. Results at ./reports/summary.md"
}
```

The CLI can also poll for status (`GET /api/tasks`) or tail output (`GET /api/tasks/{id}/log`).

#### 4.9.5 Notification Routing (Server → Channels)
- The server maintains a **notification config** that maps task outcomes to channels.
- A task doesn't declare "notify Slack" — instead, the server's notification engine handles routing based on:
  - Task metadata (e.g., `priority: high` → email + Slack)
  - Global settings (`default_channels: ["terminal", "slack"]`)
  - Per-workspace overrides
- For MVP: terminal bell + Slack webhook are primary. Email is deferred.

#### 4.9.6 Constraints
- The agent server is the **single point of coordination** — tasks and clients never talk directly.
- Notifications are **fire-and-forget** at MVP — the server doesn't wait for confirmation the user received them.
- No retry on notification failure (e.g., Slack webhook down) — the task still completes; a warning is logged.
- Rate limiting: max 10 notifications per minute across all tasks to avoid spamming connected clients.
- The server can be **remote** — same-host is not required. A laptop client could connect to an agent server running on a cloud VM.

## 5. Environment and State
### 4.10 Leverage Existing Tools (Minimal Instrumentation)

#### 4.10.1 Design Principle
- The agent should **code as little custom logic as possible**. Instead, it composes capabilities from pre-existing tools provided by the OS, OpenShell, and git.
- "Instrumentation" means: a small amount of glue code (the server API, config parser) that wires these existing tools together into a coherent agent workflow.
- No reinventing wheels: file editing uses `sed`/`ed`, searching uses `ripgrep` or `grep`, versioning uses `git`, scheduling uses OpenShell's cron, secrets use OpenShell's secret store.

#### 4.10.2 Tool Source Mapping
| Capability | Uses existing tool | Custom code needed? |
|-----------|-------------------|--------------------|
| File read/write/edit | OS shell (`cat`, `sed`, `ed`) | Minimal — wrapper calls via OpenShell session |
| Searching | `ripgrep` / `grep` | Minimal — wrapper calls via OpenShell session |
| Versioning/history | `git` (log, diff, checkout) | Minimal — git CLI calls via OpenShell session |
| Scheduling | OpenShell cron scheduler | Minimal — register/unregister via API |
| Secret management | OpenShell secret store | Minimal — `get-secret`/`list-secrets` calls via session |
| Notifications | Terminal bell, Slack webhook (curl/wget) | Minimal — HTTP POST or `notify-send` |
| File I/O in environment | OpenShell read/write/edit tools | None — delegated to environment **via session** |

#### 4.10.3 What We *Do* Build
- **Agent server** (Go binary): HTTP API + WebSocket endpoint, task lifecycle management, notification routing, memory/wiki engine, skill activation, planning loop. ~5–8k lines of Go. This is the backbone process.
- **Client protocol library** (Go package): Thin HTTP/WebSocket client implementation that any client type (CLI, web UI, custom) can use to communicate with the server. ~0.5k lines.
- **Config parser**: YAML reader that preserves `${secret:...}` placeholders; actual secret values are resolved by OpenShell at runtime within the environment (no server-side secret resolution needed).
- **Skill manifest loader**: Reads `.norman/skills/*/manifest.yaml` files to discover available skills. A skill's manifest declares:
  ```yaml
  name: "docker-skill"
  version: "1.0.0"
  description: "Interact with Docker containers"
  tools:
    - type: built-in
      name: docker
      args: ["--version", "inspect", "exec", "run"]
    - type: mcp
      endpoint: "http://localhost:3100"
      capabilities: ["container-log", "image-build"]
  ```
  The `tools` array defines what this skill provides — either CLI tools (built-in) or MCP connections. These are loaded into context only when the planning loop activates this skill for a task step.
- **The "glue"**: wiring tools together into a coherent agent loop (plan → act → observe).

#### 4.10.4 What We *Don't* Build at MVP
- No custom VCS (git handles versioning)
- No custom scheduler (OpenShell cron handles scheduling)
- No custom secret store (OpenShell handles secrets)
- No custom notification engine (terminal bell + curl for webhooks)
- No compiled plugin system (skills are scripts + manifests, not `.so` files)
- No vector search (grep/glob + frontmatter metadata suffices)

#### 4.10.5 Constraint
- If a tool is available in the environment, use it directly rather than implementing the same logic in Go. The agent is a **compositor**, not an implementor.
- **Version pinning**: All interactions with OpenShell assume API surface present in version 0.1.x or later. If a feature used (e.g., `get-secret`, MCP connections) is absent in the target version, the server should log a warning and degrade gracefully rather than crash. The minimum supported version for MVP is `0.1.0`.
- **Session lifecycle**: Each workspace gets its own OpenShell session. Sessions are long-lived (persist across task runs within a workspace) and support read/write/edit/execute operations. The server creates sessions on workspace initialization and reuses them; it does not spawn a new session per request.


### 5.1 Environment Model
- Persistent, isolated environments per workspace via **NVIDIA OpenShell ≥ 0.1.x**.
- One environment per workspace; each remembers its own state (files, working directory).
- Tools are installed *inside* the environment, not assumed present on the host.
- For MVP: basic file read/write/edit via OpenShell's capabilities.
- **Session-based interaction**: The agent always interacts with the environment through an **OpenShell session** — it never accesses the environment's filesystem directly from the host OS or the server process. All data exchange (read, write, edit, execute) goes through the OpenShell session's API/tools layer. This ensures consistency and isolation regardless of where the server runs.

### 5.2 State Management

#### 5.2.1 File-Based State (Inside Virtual Environment)
- The workspace's **file tree is the primary state store**, but it lives **inside** the OpenShell virtual environment — never on the host filesystem.
- All file access goes through the **OpenShell session's tools layer** (`read`, `write`, `edit`), not direct host OS calls. This ensures the server process (which may run on a different machine) always sees a consistent view of state.
- A hidden `.norman/` directory inside the environment holds structured metadata:
  ```
  .norman/
  ├── sessions/         ← per-session conversation transcripts (one file per session)
  ├── memory/           ← LTM wiki (flat markdown pages with frontmatter categories)
  │   ├── index.md      ← Auto-generated content catalog (page → category mapping)
  │   ├── log.md        ← Append-only chronological journal (ingest, update, query events)
  │   └── schema.md     ← Procedural rules for wiki maintenance (AGENTS.md equivalent)
  ├── config/           ← workspace-specific configuration overrides
  ├── plan.md           ← Active/pending task plans (single file, updated in-place)
  ```
- **Wiki design**: Instead of separate directories per category (`entities/`, `sources/`, `trends/`, `tasks/`), the wiki uses a **flat set of markdown pages** with frontmatter to classify entries:
  ```yaml
  ---
  title: "Project Alpha"
  type: entity        # entity | source | trend | task
  created: 2026-10-12
  updated: 2026-10-15
  tags: [api, backend]
  ---
  # Project Alpha

  A distributed API system handling...
  ```
- This reduces directory complexity from 7+ subdirectories to **3 files + 1 folder** for the wiki itself. Categories are determined by frontmatter `type:` field, not filesystem layout.
- All files are plain text, human-readable, and editable by the user **from within the environment**.
- The host OS never sees `.norman/`; it is entirely encapsulated inside the container.

#### 5.2.2 Git-Based Versioning (Inside Virtual Environment)
- The `.norman/` directory is **git-tracked** inside the OpenShell environment.
- Git provides:
  - **History**: `git log` shows all past sessions and state changes
  - **Diff**: `git diff` shows what changed between turns
  - **Rollback**: `git checkout` can restore a previous agent state
  - **Branching**: parallel experiments or task branches (e.g., `feature/rewrite-api`)
- The agent commits at natural breakpoints:
  - After completing a major task step
  - When LTM entries are created or updated
  - On user request (`git commit` the current state)
- Git is **not** used for the workspace's main files (code, data), only for `.norman/` metadata.

#### 5.2.3 State Lifecycle
```
Host filesystem
├── <workspace>/              ← user's actual project files (user's VCS)
│   └── .openshell/           ← OpenShell runtime overlay (not visible to host)
│       └── .norman/
│           ├── sessions/     ← conversation transcripts (one file per session, git-tracked)
│           ├── memory/       ← LTM wiki: flat markdown pages + frontmatter categories
│           │   ├── index.md  ← Auto-generated content catalog (page → type mapping)
│           │   ├── log.md    ← Append-only chronological journal
│           │   └── schema.md ← Procedural maintenance rules
│           ├── config/       ← workspace overrides (git-tracked inside env)
│           └── plan.md       ← Active task plans (single file, updated in-place)
```

The agent's "memory" is maintained across turns within a single CLI session via in-memory state, and persists across sessions via the `.norman/` file tree + git history — all **inside** the virtual environment. The host OS has no direct access to this metadata; it is only exposed through OpenShell's capabilities (read/write/edit tools).

The memory follows Karpathy's three-layer architecture:
- **Raw Sources**: immutable workspace files inside `.openshell/` (agent reads but does not modify)
- **Wiki**: the `memory/` directory — a **flat set of markdown pages** with frontmatter (`type: entity|source|trend|task`) for categorization. No subdirectories needed; classification is metadata-driven, not filesystem-driven.
- **Schema**: `memory/schema.md` / `AGENTS.md` encoding procedural rules for how the wiki is maintained (merge, decay, deduplicate operations).

Git operations (`git add`, `git commit`, `git diff`) are used for versioning and history; use a Go git library (e.g., `go-git`) or shell out to `git` CLI.

#### 5.2.4 Session Lifecycle Management
- **Creation**: The server creates an OpenShell session when a workspace is initialized (`norman create <name>`). The session persists until explicitly archived or the workspace is deleted.
- **Reuse**: All tasks within a workspace share the same session. The server does not spawn a new session per task — it sends commands to the existing session via its API.
- **Recovery**: If a session becomes unresponsive (e.g., crashed, timed out), the server can restart it without losing state (since `.norman/` is persisted inside the environment).
- **Version compatibility check**: On session creation, the server verifies the OpenShell version (`0.1.x+`) and logs a warning if features are missing. Future versions may add migration logic for breaking API changes.

## 6. Configuration

MVP config approach:
- Single YAML config file (`~/.config/norman/config.yaml` or equivalent)
- Contains: LLM endpoint URL, **secret references** (e.g., `${secret:openai-api-key}`), default model name, preferred environment type, skill discovery paths, MCP connection definitions
- Falls back to sensible defaults if not provided
- Secret values are resolved by OpenShell at runtime — the config file itself contains no raw credentials and can be safely git-tracked.

### 6.1 Discovery Configuration
```yaml
discovery:
  paths:
    - ~/.norman/skills/
    - /usr/share/norman/skills/
  auto_discover: true   # scan system paths on startup
  refresh_interval: 30s # re-scan for new skills
```

### 6.2 Secret Management (Delegated to OpenShell)
- The agent **never stores or reads raw secrets** (API keys, tokens, passwords). All credentials are managed by OpenShell and injected into the environment at runtime.
- Config files reference secret **names** (not values):
```yaml
llm:
  endpoint: "https://api.openai.com/v1"
  model: "gpt-4o"
  api_key: "${secret:openai-api-key}"   # resolved by OpenShell at runtime
```
- The agent accesses secrets through OpenShell's tool interface (e.g., `get-secret <name>`, `list-secrets`). It does not implement its own secrets store.
- Secrets are scoped per workspace/environment — different workspaces can have different credentials injected into the same environment.
- For MVP: a small set of built-in secrets (API keys for LLM endpoints, MCP connection auth) + ability to add custom ones via OpenShell's secret management layer without rebuilding the binary.

## 7. Non-Goals (MVP)

- No IDE integration
- No direct host filesystem access (everything through environment)
- No multi-environment parallel work in MVP (one active workspace per session)
- No automatic LLM selection or model switching
- No complex external API orchestration beyond basic web search and documentation queries
- No skill versioning or dependency resolution
- **No vector search** for LTM at MVP level (markdown/json files with frontmatter only; semantic similarity search is a future iteration)
- **No automatic raw-source ingestion** — the agent reads raw sources on demand rather than continuously polling for changes. A future iteration may add a watcher/ingest loop.
- **No authentication or multi-user support** — MVP assumes a single user connecting directly to the Agent Server. Any machine that can reach the server's port is treated as "the user." Per-user tokens, workspace ownership, and access control are deferred.

## 8. Iteration Path

After MVP is defined and built, possible extensions:
- Multi-workspace parallel environments
- Automatic tool detection/installation based on workspace type
- Git-aware operations (diff, commit suggestions)
- External service access (web search, API calls)
- State persistence across CLI invocations (not just within a session)
- Skill packaging and distribution (e.g., `norman install skill-name`)
- Multi-agent coordination (one agent delegating to another)
- **Entity relationship graph** — beyond flat entity pages, build a knowledge graph of relationships between entities for richer retrieval
- **Vector search** for semantic similarity over LTM entries (future iteration)
- **Automatic raw-source ingestion loop** — continuously watch and ingest changes from raw sources into the wiki

## 9. Agent Server Architecture

#### 9.1 Role
- The **Agent Server** is a long-running backbone process that coordinates between:
  - **Clients** (CLI, web UI, custom integrations) — thin protocol endpoints that send requests and receive notifications via HTTP/WebSocket
  - **Environments** (OpenShell containers) — run tasks, report results
- It is the single point of coordination; clients and environments never communicate directly.
- The server **is not** a "thin runtime" — it contains all orchestration logic (planning, memory management, skill activation, context budgeting). The *client* layer is thin by comparison.

#### 9.2 Deployment
- Runs on the host machine by default, but **can be remote** (e.g., a cloud VM). The server is network-addressable from any client machine.
- Exposes an HTTP API + WebSocket endpoint on a configurable port.
- Multiple clients can connect to the same server simultaneously. At MVP these all share a single user identity (no per-user tokens); true multi-user support with workspace ownership is deferred.
- Multiple environments can register with the same server (each workspace gets its own environment).
- **Client independence**: A CLI running on a laptop, a web UI on a tablet, and a custom automation script can all talk to the same server concurrently. The server is the single source of truth for task state.

#### 9.3 Server Responsibilities
| Responsibility | How it's handled |
|---------------|------------------|
| Task lifecycle | Create, track status, store results |
| Notification routing | Push to Slack, terminal bell, email based on config |
| State persistence | Write session data to `.norman/` in the environment |
| Cron scheduling | Register/unregister scheduled tasks with OpenShell's scheduler |
| Client connections | Manage WebSocket subscriptions, push real-time updates |

#### 9.4 Server API (MVP)
```
POST   /api/tasks              # create a new task
GET    /api/tasks              # list active/scheduled tasks
GET    /api/tasks/{id}         # get task status/details
POST   /api/tasks/{id}/complete  # task reports completion
GET    /api/tasks/{id}/log     # tail output (for CLI polling)
WS     /ws                     # real-time notifications to clients
```

#### 9.5 Constraints
- The server is **stateless between invocations** — it relies on the environment's file tree + git for persistence.
- No database needed at MVP; the server's in-memory state is rebuilt from workspace files on startup.
- The server process can be restarted without losing task history (stored in `.norman/sessions/`).
- **Concurrency model**: Uses **optimistic file-based writes** — multiple clients/tasks write to the same `.norman/` files concurrently. A simple lock file (`memory/.lock`) prevents simultaneous writes to the same entry. Conflicts are detected at read time (stale content warning) rather than prevented by a database transaction. This is sufficient for MVP; a proper DB can be added later if needed.
- **Error handling**: The server must be resilient to OpenShell failures without hard-failing tasks:
  - **Session crash** (OOM, timeout): retry up to 3× with exponential backoff (1s, 2s, 4s); if still failing → mark task `unavailable`, push WebSocket notification to clients.
  - **Single tool call error** (e.g., file not found): retry once; if still failing → adapt plan (skip step, use fallback), don't panic the whole task.
  - **MCP connection drop**: deactivate the skill (reclaim context tokens), fall back to a simpler tool (e.g., `grep` instead of remote query).
  - **Network timeout** between server and environment: retry with exponential backoff (3 attempts max); do not mark task permanently failed on first timeout.

## 10. Go Implementation Notes

- The **server package** should define clean interfaces (`interface{}` or explicit Go interfaces) for:
  - `Tool` — a callable capability with metadata (name, description, parameters)
  - `Skill` — a composable capability that may activate multiple tools
  - `Environment` — the persistent workspace context
  - `MemoryStore` — read/write access to LTM entries stored as flat markdown pages with frontmatter (`type: entity|source|trend|task`). Supports indexed catalog (`index.md`), chronological logging (`log.md`), and query by category, timestamp, relevance. Includes methods for wiki maintenance operations (merge, decay, deduplicate). No subdirectories — all entries are top-level markdown files.
  - `ContextBudget` — tracks LLM token usage (accumulated from API `usage` fields) and triggers GPT-style compression cycles when budget thresholds are crossed (75%, 90%). Supports: `addTokens(count int)`, `getUsed() int`, `getRemaining() int`, `triggerCompression()` which returns a list of summarized entries to replace raw turns.
  - `SecretManager` — delegates secret resolution to OpenShell (read-only access via `get-secret`/`list-secrets` tools); the server never stores raw credentials
  - `WikiSchema` — reads/writes procedural rules (`schema.md` / `AGENTS.md`) that govern how the wiki is maintained; defines linting, decay, and merge behaviors.
- The discovery mechanism should be pluggable: users can add custom discovery backends without modifying the core.
- No third-party plugin system (e.g., `plugin.go`) needed for MVP; skills are external scripts + manifests, not compiled plugins.
- LTM is backed by **file system** (`.norman/memory/`); entries are flat markdown files with frontmatter metadata (`type: entity|source|trend|task`). No subdirectories — classification is via frontmatter, not filesystem layout.
- Git operations (`git add`, `git commit`, `git diff`) are used for versioning and history; use a Go git library (e.g., `go-git`) or shell out to `git` CLI.
- Config parsing uses a simple YAML reader with `${secret:...}` placeholder resolution — values are not resolved at parse time but deferred until the environment is initialized (so OpenShell can inject secrets).
