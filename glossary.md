# Norman — Glossary of Terms

> This glossary captures key terms from the requirements and project files. Each entry is a brief description (≤ 2 sentences) of a concept, component, or design decision in the norman agent system.

---

## Architecture & Components

**Agent Server** — The long-running backbone process that coordinates between clients and environments. It contains all orchestration logic (planning, memory management, skill activation, context budgeting) and exposes an HTTP/WebSocket API.

**Thin Client** — A lightweight protocol endpoint (CLI, web UI, or custom interface) that sends requests to the Agent Server and receives notifications. It contains no orchestration logic; its only job is forwarding input and displaying output.

**Environment** — A persistent, isolated OpenShell session per workspace. All file I/O happens through the environment's tools layer (`read`, `write`, `edit`), never directly on the host OS filesystem.

**Workspace** — A directory + a set of configured tool/skill sources. Each workspace has its own persistent environment and available capabilities. Multiple clients can access the same workspace concurrently.

**Session** — An OpenShell session that persists across task runs within a workspace. The server creates it on workspace initialization and reuses it; it does not spawn a new session per request.

## Memory & Context

**Short-Term Memory (STM)** — The active conversation context visible to the LLM at any given turn. It contains current task state, recent tool results, and last N messages, bounded by the model's token context window (~128K tokens).

**Long-Term Memory (LTM)** — Persistent knowledge stored as flat markdown pages in `.norman/memory/`. Entries are classified by frontmatter (`type: entity|source|trend|task`) and accessed via retrieval-augmented queries rather than being dumped into the context window.

**Wiki** — The LTM knowledge base consisting of a flat set of markdown pages with frontmatter categories, an auto-generated `index.md` catalog, and an append-only `log.md` journal. No subdirectories are needed; classification is metadata-driven.

**Schema (AGENTS.md)** — Procedural rules stored in `memory/schema.md` that govern how the wiki is maintained: merge duplicates, decay stale entries, avoid clutter. It encodes the agent's "maintenance routine" for keeping memory coherent.

**Context Budget** — A token-budget mechanism tracking LLM token usage across all messages in a session. When budget reaches 75% of the window, a GPT-style compression cycle (summarize → replace → archive) is triggered; at 90%, forced aggressive compression applies.

**Progressive Disclosure** — The mechanism by which skills are discovered (metadata scan) but their tools are only injected into the prompt when the planning loop determines they're needed for a specific task step, preventing context bloat.

## Tools & Capabilities

**Skills** — Reusable, composable capabilities defined as external modules (scripts + `SKILL.md` + manifest). They are discovered from configured paths at runtime and activated on demand via progressive disclosure.

**Discovery Paths** — Configurable directories where skills are scanned for valid definitions (`~/.norman/skills/`, `/usr/share/norman/skills/`, per-workspace `skills/`). The discovery mechanism performs a lightweight metadata scan without loading tool signatures into context.

**MCP (Model Context Protocol)** — A protocol by which skills expose remote tool capabilities. A skill's manifest can declare MCP connections (`endpoint`, `capabilities[]`) that provide external service tools following the same progressive activation lifecycle.

**Core Tools** — Always-available built-in tools loaded at session start: file read/write/edit via OpenShell, `ripgrep`/`grep` for searching, and git operations (`log`, `diff`, `checkout`). No skill activation is needed to use them.

**Tool** — A callable capability with metadata (name, description, parameters). It can be a built-in CLI tool or an MCP connection, following the discover → activate → use → deactivate lifecycle.

## Planning & Execution

**Planning Loop** — The observe → plan → act → observe → adjust cycle that drives task execution. At each step, the agent decides which skill to activate, whether to load LTM context, and whether to ask clarifying questions.

**Task Decomposition** — Breaking a user's natural-language request into a sequence of steps. Each step determines which tools are needed and activates only those tools before executing.

**Self-Correction** — If a tool call fails or results look wrong, the agent retries with adjusted parameters rather than blindly continuing through the plan.

## State & Persistence

**.norman/** — The hidden directory inside the OpenShell environment that holds all structured metadata: `sessions/` (conversation transcripts), `memory/` (LTM wiki), `config/` (overrides), and `plan.md` (active task plans). It is git-tracked but never visible on the host filesystem.

**Flat Wiki Design** — Instead of subdirectories per category, all LTM entries are top-level markdown files classified by frontmatter `type:` field (`entity`, `source`, `trend`, `task`). This reduces directory complexity from 7+ subdirectories to 3 files + 1 folder.

**Optimistic File-Based Writes** — Multiple clients/tasks write to the same `.norman/` files concurrently using a lock file (`memory/.lock`) for safety. Conflicts are detected at read time (stale content warning) rather than prevented by a database transaction.

## Configuration & Integration

**Config Parser** — A YAML reader that preserves `${secret:...}` placeholders without resolving them at parse time. Actual secret values are injected into the environment by OpenShell at runtime via `get-secret`/`list-secrets` calls.

**Secret Management** — Delegated entirely to OpenShell. The agent never stores or reads raw credentials; it accesses secrets through OpenShell's tool interface. Secrets are scoped per workspace/environment.

**Cron Scheduler** — Registered with OpenShell's built-in cron for recurring tasks (e.g., "every 5 minutes", "daily at 9am"). The server does not implement its own scheduler; it delegates scheduling to OpenShell and receives notifications when jobs fire.

**Notification Routing** — The server's responsibility to push task results/status to configured channels (terminal bell, Slack webhook). A task doesn't declare notification destinations; the server routes based on metadata, global settings, and per-workspace overrides.

## Design Principles

**Compositor, Not Implementor** — If a tool is available in the environment, use it directly rather than implementing the same logic in Go. The agent composes capabilities from pre-existing tools (OS shell, git, OpenShell, MCP) with minimal glue code (~5–8k lines of Go).

**Minimal Surface for MVP** — Start with the core loop: receive a task → discover capabilities → compose a plan → execute using available tools → return results. Expand later to multi-agent coordination, vector search, automatic ingestion loops.

**Environment-Isolated State** — All `.norman/` metadata lives inside the OpenShell virtual environment; the host OS never sees it directly. State is only accessible through the environment's tools layer.

## Identity & Access

**Single-User Assumption (MVP)** — For MVP, the system assumes a single user without authentication. There is no login flow, no session-per-user tokenization, and no multi-user access control. The client connects directly to the Agent Server; any machine that can reach the server's port is treated as "the user." Multi-user support (per-user tokens, workspace ownership) is deferred to a future iteration.

---

*This glossary was auto-generated from `requirements.md` and `project.md`. It captures terms as defined in the norman MVP specification.*
