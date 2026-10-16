# norman — Requirements Specification (Chat-First Agent)

## 1. Purpose

**Norman is a chat agent.** You talk to it in a session, inside a workspace, and it helps you work using tools and skills available in the environment. Tasks and scheduling exist only as a *consequence* of conversation — they are not manually triggered by the user but emerge naturally when the user says "keep an eye on X" or "run this daily."

The primary interaction model is **interactive conversation**. Everything else (scheduled tasks, recurring jobs, async notifications) is secondary and driven by what the agent infers from the chat.

## 2. Core Principles

- **Chat-first**: The user's main interface is a conversational session bound to a workspace. You type messages; Norman responds, acts, asks questions, and reports results — all within that conversation.
- **Tasks are emergent**: A task (scheduled or recurring) is created when the agent *decides* one is needed during conversation, not when the user explicitly schedules something. Manual scheduling exists but is a convenience, not the primary model.
- **Session-centric**: A session = one continuous chat conversation within a workspace. The session carries state: context, plan, memory references, active skills.
- **Workspace-isolated**: All state lives inside the OpenShell environment (`.norman/`). The host OS never sees it directly. Each workspace has its own session(s).
- **Tools & skills are discovered at runtime**: Skills define what capabilities an agent has; they are loaded on demand (progressive disclosure) to keep context lean.
- **Stateful conversations**: The agent remembers what happened across turns within a session. It can reference earlier decisions, plans, or findings without the user repeating them.

## 3. User Experience

### 3.1 Interaction Model

**The primary experience is a chat session.** You are in a workspace and you talk to Norman:

```
User: "Hey, I'm working on the API migration. Can you check if the v2 endpoints are all documented?"
Norman: (scans docs, finds gaps, reports back)
User: "Great. Also keep monitoring that endpoint every 5 minutes and alert me if it's down."
→ Norman creates a recurring task in the background. You don't manage it; it just works.
```

- **Sessions**: A session is one conversation thread bound to a workspace. You can have multiple sessions (e.g., "research project X", "fix bug Y") but only one is *active* at a time per client.
- **Workspace context**: Each session lives inside a workspace (`<workspace>/`). The agent sees the file tree, tools, and skills available in that environment.
- **Multi-client per workspace**: CLI, web UI, or any protocol-compatible interface can connect to the same server. At MVP, all clients share one logical user identity.

### 3.2 What You Do (Not What Norman Does)

| User action | What happens |
|---|---|
| Open a session in a workspace | Norman loads context (file tree, skills), shows available tools/skills |
| Send a message | Agent plans, acts, responds — all within the conversation |
| Say "keep watching X" | Agent *creates* a scheduled task behind the scenes |
| Say "run this daily at 9am" | Agent registers it as a recurring job (emergent scheduling) |
| Close/exit session | Session is archived; state persisted to `.norman/sessions/` |
| `sessions list` / `sessions switch <name>` | Switch between conversations (convenience, not required for core UX) |

The user does **not** manually manage tasks. Tasks are created by the agent as a natural result of conversation. The user might *see* them or *cancel* them, but they don't create them explicitly.

## 4. Agent Capabilities

### 4.1 LLM Integration

- Uses an OpenAI-compatible API (configurable endpoint + model).
- Secrets managed by OpenShell — the agent never reads raw API keys from files.
- No automatic model switching at MVP; the model is set in config or conversation.

### 4.2 Context Management (Token Budget)

The agent manages its own context window:

1. **STM (Short-Term Memory)**: The active conversation visible to the LLM. Bounded by the model's token limit (~128K tokens at MVP).
2. **LTM (Long-Term Memory)**: Persistent wiki entries in `.norman/memory/` — flat markdown pages with frontmatter (`type: entity|source|trend|task`). Accessed on demand via retrieval-augmented reading.
3. **Compression**: When budget reaches 75%, the agent summarizes older turns into compact bullets (~30% of original tokens). At 90%, aggressive compression is forced. Pinned entries and the user's original request are always preserved.

The flow:
```
Raw Sources (immutable) → Wiki (LTM) ← retrieval on demand
    ↓                              ↑
STM (active window + compression)
```

### 4.3 Skills System

- Skills are external modules discovered at runtime from configured paths (`~/.norman/skills/`, per-workspace `skills/`, system paths).
- **Progressive activation**: Tools are only injected into the prompt when the planning loop determines they're needed. Unused skills stay out of context.
- A skill's `SKILL.md` declares what it provides (built-in CLI tools or MCP connections).
- At MVP: 3–5 built-in skills + mechanism to add more without rebuilding.

### 4.4 MCP and Tooling

- Skills expose tools via either built-in CLI tools (`ripgrep`, `sed`) or MCP connections to remote services.
- **Unified lifecycle**: discover → activate on demand → use → deactivate. Whether a tool comes from a skill's built-in set or an MCP connection, it follows the same progressive disclosure model.

### 4.5 Built-in Core Tools (always loaded)

| Capability | Tool |
|---|---|
| File read/write/edit | OpenShell `read`, `write`, `edit` |
| Search | `ripgrep` / `grep` wrapper |
| Git operations | `git log`, `git diff`, `git checkout` |

These are the minimum surface — no skill activation needed.

### 4.6 Planning Loop

- On receiving a user message, the agent:
  1. Observes current state (file tree, active skills, prior context)
  2. Plans steps (decomposes the request)
  3. Acts (uses tools/skills for each step)
  4. Observes results
  5. Adjusts or asks a clarifying question

- The LLM drives planning (prompted with structured instructions). No separate "planner" component at MVP.
- Planning state is kept in conversation context; no persistent plan file needed.

### 4.7 Memory Architecture

The application maintains **three layers of memory**:

#### STM — Active Conversation
- Current task state, recent tool results, last N messages, active plan steps.
- Size bounded by model's token count (not character length). Accumulated from `usage.token_count` in API responses.

#### LTM — Persistent Wiki
- Flat markdown pages in `.norman/memory/` with frontmatter classification (`type: entity|source|trend|task`).
- Accessed via retrieval-augmented reading — the agent reads only what's needed into STM.
- Entries auto-created when the agent completes a task or discovers significant facts.

#### Schema — Procedural Rules
- `memory/schema.md` (or `AGENTS.md`) encodes rules for wiki maintenance: merge duplicates, decay stale trends (>30d), distinguish one-off vs. reusable knowledge.
- The agent's "maintenance routine" — not human-maintained, but rules the agent follows.

### 4.8 Tasks & Scheduling (Secondary, Emergent)

**Tasks are created by the agent during conversation**, not manually scheduled by the user. Examples:

```yaml
# Created when user says: "keep monitoring this endpoint every 5 min"
- name: "monitor-api-health"
  schedule: "*/5 * * * *"
  skill: "monitoring.check-endpoint"
  config: { url: "${secret:api-url}", timeout: 30s }
  on_success: notify("slack", "#ops")
```

- The agent decides when to create a task based on conversation context.
- The user might *see* tasks (`list scheduled`), *cancel* them, or *override* their schedule — but doesn't manually register new ones as a primary workflow.
- Tasks run in the same OpenShell session as the active workspace.
- Results are pushed to configured channels (Slack, terminal bell) automatically.

**Constraints at MVP:**
- Cron expressions: standard 5-field format (`minute hour day month weekday`)
- Max concurrent scheduled tasks: configurable (default: 3 per workspace)
- Tasks are stateless between runs unless they explicitly read/write files
- No task dependencies ("run B after A") — deferred

## 5. Environment and State

### 5.1 OpenShell Session-Based Interaction

- Persistent, isolated environments per workspace via **NVIDIA OpenShell ≥ 0.1.x**.
- One session per workspace; the agent always interacts through the OpenShell session's API/tools layer (`read`, `write`, `edit`, `execute`).
- The server process (which may run on a different machine) never accesses the environment's filesystem directly — all data exchange goes through the session.

### 5.2 File-Based State (Inside Virtual Environment)

```
<workspace>/              ← user's actual project files (user's VCS)
└── .openshell/           ← OpenShell runtime overlay (not visible to host)
    └── .norman/
        ├── sessions/       ← per-session conversation transcripts
        ├── memory/         ← LTM wiki: flat markdown + frontmatter categories
        │   ├── index.md    ← Auto-generated content catalog
        │   ├── log.md      ← Append-only chronological journal
        │   └── schema.md   ← Procedural maintenance rules
        ├── config/         ← workspace overrides (git-tracked)
        └── plan.md         ← Active task plans
```

All files are plain text, human-readable, editable from within the environment. The host OS never sees `.norman/`.

### 5.3 Git-Based Versioning (Inside Virtual Environment)

- `.norman/` is git-tracked. Provides history, diff, rollback, and branching for agent state.
- Agent commits at natural breakpoints: after completing a task step, when LTM entries are created/updated, or on user request.
- Git is **not** used for the workspace's main files (code, data), only for `.norman/` metadata.

## 6. Configuration

Single YAML config file (`~/.config/norman/config.yaml`):
- LLM endpoint URL, model name, secret references (`${secret:...}`)
- Skill discovery paths
- MCP connection definitions
- Notification channels (Slack webhook, terminal bell)
- Secret values resolved by OpenShell at runtime — no raw credentials in config.

### 6.1 Discovery Configuration
```yaml
discovery:
  paths:
    - ~/.norman/skills/
    - /usr/share/norman/skills/
  auto_discover: true
  refresh_interval: 30s
```

## 7. Non-Goals (MVP)

- No IDE integration
- No direct host filesystem access (everything through environment)
- No multi-environment parallel work in MVP (one active workspace per session)
- No automatic LLM selection or model switching
- No complex external API orchestration beyond basic web search and documentation queries
- No skill versioning or dependency resolution
- **No vector search** for LTM at MVP (markdown/json + frontmatter only)
- **No authentication or multi-user support** — single user identity per server connection
- **No manual task creation as primary workflow** — tasks emerge from conversation

## 8. Iteration Path

After chat-first MVP:
- Multi-workspace parallel sessions
- Automatic tool detection/installation based on workspace type
- Git-aware operations (diff, commit suggestions)
- External service access (web search, API calls)
- Skill packaging and distribution (`norman install skill-name`)
- **Entity relationship graph** — knowledge graph of relationships between entities
- **Vector search** for semantic similarity over LTM entries
- **Automatic raw-source ingestion loop** — continuously watch and ingest changes

## 9. Agent Server Architecture

### 9.1 Role

The server is the single coordination point:
- **Clients** (CLI, web UI) → send requests, receive WebSocket push notifications
- **Environments** (OpenShell containers) → run tasks, report results
- **Chat sessions** → the primary user-facing interaction model

### 9.2 Server API (MVP — Chat-Focused)

```
POST   /api/sessions           # create a new chat session in a workspace
GET    /api/sessions/{id}      # get session status/details
POST   /api/sessions/{id}/messages  # send a message (chat input)
WS     /ws                     # real-time push: agent responses, task completions
GET    /api/tasks              # list emergent scheduled tasks (read-only convenience)
DELETE /api/tasks/{id}         # cancel an emergent task
```

**Note**: The primary API is `/api/sessions/{id}/messages` — this is the chat interface. Task management (`GET/DELETE /api/tasks`) exists but is secondary; tasks are created by the agent, not manually registered by the user.

### 9.3 Server Responsibilities

| Responsibility | How it's handled |
|---|---|
| Chat session lifecycle | Create, track, archive sessions per workspace |
| Message routing | Plan → act → respond loop per message |
| Notification push | WebSocket events to connected clients |
| State persistence | Write session transcripts to `.norman/sessions/` |
| Task creation (emergent) | Agent decides when to register scheduled tasks during conversation |

### 9.4 Constraints

- **Stateless between invocations**: Server relies on environment's file tree + git for persistence. No database at MVP.
- **Optimistic file-based writes**: Multiple clients/tasks write concurrently; conflicts detected at read time (stale content warning).
- **Resilient to OpenShell failures**: Session crash → retry 3× with exponential backoff; single tool error → adapt plan; MCP drop → deactivate skill, fall back.

## 10. Go Implementation Notes

Key interfaces:
- `Tool` — callable capability with metadata (name, description, parameters)
- `Skill` — composable capability activating multiple tools on demand
- `Environment` — persistent workspace context (OpenShell session wrapper)
- `MemoryStore` — flat markdown wiki with frontmatter categories; supports indexed catalog, chronological logging, query by category/timestamp/relevance
- `ContextBudget` — tracks token usage from API `usage` fields; triggers compression at 75%/90% thresholds
- `SecretManager` — delegates to OpenShell (`get-secret`/`list-secrets`); server never stores raw credentials
- `WikiSchema` — reads/writes procedural rules (`schema.md`/`AGENTS.md`)

Discovery mechanism is pluggable; no compiled plugin system at MVP. LTM backed by file system (`.norman/memory/`), entries are flat markdown with frontmatter. Git operations for versioning; config uses YAML with `${secret:...}` placeholders resolved at runtime.
