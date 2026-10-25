<img width="1573" height="562" alt="norman" src="https://github.com/user-attachments/assets/0122bb6b-e5d6-4069-90f3-d92bf0fdc40d" />

[![CI](https://github.com/pilardi/norman/actions/workflows/go.yml/badge.svg)](https://github.com/pilardi/norman/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25-blue.svg)](https://go.dev/)
[![Last Commit](https://img.shields.io/github/last-commit/pilardi/norman.svg)](https://github.com/pilardi/norman/commits/master)

## The poor man's agent

A chat-first agent built entirely with local AI tools — **goose**, **Berd**, **Unsloth**, **llamacpp**, and **Qwen3.8**. No cloud dependencies required. No grand claims about what it can do. It is a work in progress, and it will probably never be "complete."

### Architecture at a glance

Norman runs as a **client–server** pair:

| Component | Role |
|---|---|
| `norman-server` | Long-lived HTTP + WebSocket daemon (Echo). Manages sessions, skills, memory, scheduler. Lives inside an OpenShell sandbox. |
| `norman` | CLI client. Sends messages to the server and displays responses. Supports interactive REPL chat with slash-commands (`/help`, `/close`, `/new`, `/switch`, `/history`). |

You start the server, then use the CLI (or any HTTP/WebSocket client) to talk to it. Planned future distribution: a Docker image and a web interface (the CLI is the primary interface today). For now you run the binaries directly from `dist/`.

### How to use it

**1. Start the server:**
```bash
./dist/norman-server -p llamacpp   # select provider (llamacpp, openai, etc.)
# or with verbose debug logging:
./dist/norman-server --verbose -p default
```

The server reads config from `~/.norman/config.yaml` and uses the OpenShell gateway for agent isolation. By default it listens on `127.0.0.1:8080`.

**2. Talk to it via CLI:**
```bash
# Start an interactive REPL chat session in a workspace
./dist/norman sessions open my-project

# Or pipe a one-off message and exit (press Ctrl-D when done)
echo "What files changed this week?" | ./dist/norman sessions open my-project
```

**3. Slash-commands inside the REPL:**
| Command | Effect |
|---|---|
| `/help` | Show help |
| `/tasks` | List emergent scheduled tasks |
| `/close` | Close session (save transcript) |
| `/new` | Start a new session |
| `/history [N]` | Show last N messages |
| `/sessions` | List all active sessions |
| `/switch <id>` | Switch to another session |
| `/archive <id>` | Archive a session's transcript |

**4. Manage workspaces and skills:**
```bash
./dist/norman workspaces create api-migration
./dist/norman workspaces list
./dist/norman skills list
./dist/norman skills install ./path/to/skill-folder
```

**5. API reference docs:**
The server exposes a live Scalar-powered API reference at `http://localhost:8080/docs`. You can also generate a static HTML file with `make generate-docs`.

### What matters most: agent isolation

The agent lives entirely inside an OpenShell environment (`.norman/`). The host OS never sees it directly. Each workspace has its own session state, memory store, and skill set. This is the design constraint everything else follows — the server and client are just thin interfaces over that isolated runtime.

### Build & test

```bash
make generate   # regenerate types/handlers from api-spec.yaml
make build      # compile to dist/
make test       # run all tests with coverage
make test-race  # run with race detector
```

Generated code lives in `pkg/protocol/` and `internal/api/`; it is not committed to the repo. The source of truth for the API contract is `api-spec.yaml`.

### Status

**Alpha.** The API spec drives code generation. The server (`norman-server`) runs as a long-lived process; the CLI (`norman`) is the primary client. `roger` is a companion agent for background tasks.

Nothing here is production-ready. Nothing is claimed to be either. If you want to understand the design decisions, start with `requirements.md` and `AGENTS.md`.

---

*Built locally. Evolving locally.*

### Acknowledgments

Norman wouldn't exist without the tools that made it:

| Tool | Why it matters |
|---|---|
| [**goose**](https://goose-docs.ai/) | The agent framework used to design, build, and iterate on Norman itself |
| [**OpenShell**](https://docs.nvidia.com/openshell) | Provides the sandboxed runtime that gives Norman its isolation guarantees |
| [**Unsloth Studio**](https://unsloth.ai/docs/new/studio) | Local fine-tuning and inference tooling used during development |
| [**llama.cpp**](https://github.com/ggml-org/llama.cpp) | The local LLM inference engine powering the agent's reasoning |
| [**Berd**](https://github.com/block/berd/) | The desktop app environment where sessions, agents, and skills are managed |
| [**Qwen3.8**](https://qwen.ai/blog?id=qwen3.8) | The model used for local inference in this project |

No single one of these would be enough on its own. Together they make a poor man's agent possible without leaving your machine.
