# AGENTS.md

This file provides guidance to AI coding agents (Claude Code and equivalents) when working with code in this repository.

## Project Overview

Friday is a Unix-philosophy AI Agent for the terminal. Text in, text out. Pipe-friendly.

Core features:
- **Pipeline-first design** — stdin/stdout for Unix pipe composition
- **Multiple input modes** — Arguments, stdin, or combine both with `-m` flag
- **Local data** — Everything stored in `~/.friday/`
- **Session management** — Persistent conversations with history
- **Multi-provider support** — OpenAI, Anthropic, Ollama, Google Gemini

## Module Structure

This repository contains two Go modules:
- **Root module** (`/`) — CLI application, TUI, coder engine, actor runtime, event bus, daemon, sandbox, config, workspace, disk-defined agents, memory, skills, MCP
- **Core module** (`/core`) — Agent interfaces, actor framework, providers, session management, tools, planning

The `core` package is a separate module with its own `go.mod`. See `core/AGENTS.md` for details.

## Module Map

Every module below has its own `AGENTS.md` with file-level detail:

| Module | Purpose |
|---|---|
| [`cmd/`](cmd/AGENTS.md) | Cobra CLI entry points (chat, sessions, TUI, daemon, heartbeat, skills, sandbox, MCP, sunrise) |
| [`config/`](config/AGENTS.md) | Config loading (JSON/YAML), model pool, path resolution |
| [`setup/`](setup/AGENTS.md) | Agent assembly: wires client, workspace, session, memory, hooks |
| [`workspace/`](workspace/AGENTS.md) | Markdown workspace loading (SOUL.md, ENVIRONMENT.md, ...) |
| [`memory/`](memory/AGENTS.md) | Daily memory logs and retention |
| [`sessions/`](sessions/AGENTS.md) | Persistent session storage and usage accounting |
| [`skills/`](skills/AGENTS.md) | SKILL.md loading, registry, toolset, hook injection |
| [`mcp/`](mcp/AGENTS.md) | MCP server connections, trust, tool conversion |
| [`shellcmd/`](shellcmd/AGENTS.md) | Shell command quoting helper |
| [`utils/`](utils/AGENTS.md) | Shared helpers (events, fmt, hash, strings, logger, signal) |
| [`sandbox/`](sandbox/AGENTS.md) | OS sandboxing (Seatbelt/bwrap), bash/fs tools, background tasks, approval |
| [`actor/`](actor/AGENTS.md) | Per-session actor registry over `core/actor` |
| [`bus/`](bus/AGENTS.md) | Event bus topics and ordered Feed consumers |
| [`cache/`](cache/AGENTS.md) | Disk-backed JSON cache with TTL |
| [`daemon/`](daemon/AGENTS.md) | HTTP server exposing actors (default port 8999) |
| [`tui/`](tui/AGENTS.md) | Bubble Tea terminal UI |
| [`coder/`](coder/AGENTS.md) | Coding agent engine (agents, commands, file/config tools, loop, project) |
| [`e2e/`](e2e/AGENTS.md) | End-to-end test harness configuration |
| [`core/agents`](core/agents/AGENTS.md) | Agent interface, ReAct loop, research/summarize agents |
| [`core/session`](core/session/AGENTS.md) | Session state, hooks, compaction |
| [`core/subagents`](core/subagents/AGENTS.md) | Batched explore/run_task subagent orchestration |
| [`core/contextmgr`](core/contextmgr/AGENTS.md) | Context projection, compaction, refocus, session memory |
| [`core/planning`](core/planning/AGENTS.md) | TODO planning and LATS tree search |
| [`core/providers`](core/providers/AGENTS.md) | LLM clients (OpenAI, Anthropic, fallback) and embeddings |
| [`core/tools`](core/tools/AGENTS.md) | Tool definition, invoker, validation |
| [`core/api`](core/api/AGENTS.md) | Request/Response types and streaming |
| [`core/types`](core/types/AGENTS.md) | Message and event types |
| [`core/state`](core/state/AGENTS.md) | KV state interface and in-memory implementation |
| [`core/logger`](core/logger/AGENTS.md) | Logger interface and silent writer-backed default |
| [`core/tracing`](core/tracing/AGENTS.md) | Tracing interface and noop implementation |
| [`core/actor`](core/actor/AGENTS.md) | AG-UI protocol actor: async inbox, event stream, cards/forms |

Single-file packages without their own doc: `core/collaboration` (default/plan collaboration mode contract, consumed by the actor hook), `core/promptcontext` (request-local leading context blocks for project instructions and approved plans).

## Commands

```bash
make build    # Build for darwin/arm64, darwin/amd64, linux/arm64, linux/amd64
make test     # Run all unit tests (go test ./... and go test ./core/...)
```

To run a single test:
```bash
go test -v -run TestName ./path/to/package
```

- Binaries need to be placed in the bin directory; it is strongly recommended to use make for building.
- When executing commands, keep them simple and easy to audit. For example, if you need to execute `cmd1 && cmd2`, please use the tool twice, executing `cmd1` and `cmd2` respectively.

## Architecture

### Entry Point (`cmd/`)

Cobra CLI application with commands:
- `root.go` - Root command with config loading and session manager initialization
- `chat.go` - Send messages via arguments or stdin pipe
- `init.go` - Initialize workspace with default markdown files
- `session.go` - Session management (list, new, current, use, show, alias, archive, delete, compact)
- `heartbeat.go` - Send periodic tasks defined in HEARTBEAT.md
- `tui.go` - Launch the interactive terminal UI
- `daemon.go` - Run the HTTP actor daemon
- `mcp.go` - MCP server management (list, inspect, test, refresh, reconnect, trust, untrust)
- `skills.go` - Skill management (list, install, delete, update)
- `sandbox.go` - Sandbox command allow-list management
- `sunrise.go` - Sunrise greeting with daily briefing

### Agent System (`core/agents/`)

The `agents/` package defines the `Agent` interface (`core/agents/interface.go:9-11`):
```go
type Agent interface {
    Chat(ctx context.Context, req *api.Request) *api.Response
}
```

- `react.go` - ReAct-style agent with thought/action/observation loop (default max 500 iterations)
- `tools.go` - Tool execution and JSON Schema handling
- `research/` - Research agent with subagent delegation
- `summarize/` - Specialized agent for response synthesis and conversation compaction

### Session System (`core/session/`)

Session manages conversation state and tool execution context:
- `session.go` - Core session with message history, token tracking, and workdir filesystem
- `hooks.go` - Hook system: `BeforeAgent`, `BeforeModel`, `AfterModel` lifecycle hooks
- `compact.go` - Conversation compaction/shortening utilities

Sessions support forking for sub-agent execution (`core/session/session.go:57-75`).

### Subagents (`core/subagents/`)

Orchestrates expert sub-agents:
- `hook.go` - Registers `BeforeAgent` and `BeforeModel` hooks to inject subagent tools
- `tool.go` - Batched `explore` and `run_task` tools execute independent subagent work under a shared concurrency limit

### Actor Runtime (`actor/`, `bus/`, `core/actor/`)

The actor runtime turns agent sessions into addressable, event-driven actors:

- `core/actor/` - Protocol-level actor: wraps a `core agents.Agent` with an async inbox (multi-message coalescing), a structured AG-UI event stream, card/form tools, and an event translator. It only consumes `core` public interfaces; it never modifies them.
- `actor/` - Per-session `Registry` over `core/actor`: owns actor construction (wiring `setup.NewAgent`), event-stream subscription, idle eviction, and shutdown.
- `bus/` - Event bus topics plus `Feed`, an ordered consumer that registers one serial listener across topic patterns so deltas never reorder against lifecycle markers.

`core/collaboration` (single file) defines the default/plan collaboration mode contract consumed by the actor hook; `core/promptcontext` (single file) carries request-local leading context blocks (project instructions, approved plans).

### Coder Engine (`coder/`)

The coder engine drives friday's interactive coding agent (used by the TUI):
- `agents/` - Named Agent specs (`AGENT-SPEC.md`), factory, registry, router, expert/explorer agents
- `commands/` - Slash-command registry (`/agent`, `/info`, session and UI commands)
- `configtools/` - Agent config tool hooks and backing store
- `filetools/` - Project instruction file discovery hook (AGENTS.md/CLAUDE.md FYI)
- `loop/` - Turn loop manager and state
- `project/` - Project registry, per-project file store, locks, user history

### Disk-defined Agents (`coder/agents/`)

Named Agents load at startup from `~/.friday/agents/<name>/AGENT-SPEC.md` and
project-local `.friday/agents/<name>/AGENT-SPEC.md`; project definitions
override HOME definitions with the same name. Specs use permissive YAML
frontmatter plus a Markdown system prompt. Optional `model` and `effort`
frontmatter select defaults from the shared model pool; Session overrides have
higher priority. Loaded Agents reuse tools, hooks, and session services, are
exposed through `run_task`, and can handle one TUI turn through
`/agent-name <task>`.

### Planning (`core/planning/`)

- `lats/` - LATS reasoning tree with candidate generation, parallel execution, and evaluation
- `todo.go` - TODO-based planning with hook integration

### Providers (`core/providers/`)

- `interface.go` - Client interface with `Completion`, `CompletionNonStreaming`, `StructuredPredict`
- `openai/client.go` - OpenAI-compatible API client with streaming support
- `openai/compatible.go` - OpenAI-compatible providers (Ollama, Gemini)
- `openai/embedding.go` - Vector embedding generation
- `anthropics/client.go` - Anthropic Claude API client

### Tools (`core/tools/`)

- `tool.go` - Tool definition with JSON Schema, handlers, and property builders
- `utils.go` - Tool utility functions

### API (`core/api/`)

- `requests.go` - Request/Response types for agent chat
- `stream.go` - Streaming response utilities
- `context.go` - HTTP context utilities

### Core Types (`core/types/`)

- `session.go` - Message types with roles (system/user/assistant/agent/tool)
- `event.go` - Session hook type constants (`BeforeAgent`, `BeforeModel`, `AfterModel`)

### State (`core/state/`)

- `interface.go` - State interface for KV storage with app/user scopes
- `inmemory.go` - In-memory state implementation (default)

### Logger (`core/logger/`)

- `interface.go` - Logger interface
- `default.go` - Default implementation
- `root.go` - Root logger setup

### Workspace (`workspace/`)

Workspace loads markdown files for agent context:
- `loader.go` - Loads workspace files and memory logs
- `types.go` - FileSpec, FileRole, and LoadedContent types
- `defaults.go` - Default content templates for initialization

Workspace files (loaded into system prompt):
- `SOUL.md` - Persona and tone
- `ENVIRONMENT.md` - Machine and execution environment
- `AGENTS.md` - Behavior guidelines
- `IDENTITY.md` - Agent name and style
- `TOOLS.md` - Tool usage guidance
- `HEARTBEAT.md` - Periodic checklist
- `MEMORY.md` - Long-term memory

### Memory (`memory/`)

Daily memory log system:
- `memory.go` - Memory system for daily logs
- `forgetting.go` - Memory retention and cleanup

### Session Storage (`sessions/`)

- `manager.go` - Session manager for current session tracking
- `store.go` - Session store interface
- `lifecycle.go` - Session lifecycle operations (fork, archive, delete)
- `file/store.go` - File-based session persistence (with metadata locking)
- `usage/usage.go` - Root-session model and turn usage aggregates

### Configuration (`config/`)

- `config.go` - Config loading (JSON or YAML), path resolution, env expansion
- `types.go` - Config structs (ModelConfig, MemoryConfig, SessionConfig, LogConfig)

Default paths:
```
~/.friday/
├── config.json          # Configuration (or friday.yaml)
├── sessions/            # Conversation history
├── memory/              # Daily memory logs
├── log/                 # Application logs
├── agents/              # Named Agent definitions (AGENT-SPEC.md)
└── workspace/           # Agent context files
```

### Terminal UI (`tui/`)

Bubble Tea application (`tui.Run`) that hosts the interactive chat loop, slash commands, todo/planning views, tool cards, forms, and markdown rendering. It talks to the actor registry and bus; code reachable from the TUI must never write to stdout/stderr directly (see Friday repository guidance below).

### Daemon (`daemon/`)

HTTP server exposing the actor registry over a JSON protocol (default port 8999): actor catalog, connections, input dispatch, and event streams.

### Cache (`cache/`)

Small disk-backed JSON cache with TTL, entry-size limits, atomic replacement, and cross-platform file locking (miss/fresh/stale statuses).

## Provider Interface (`core/providers/interface.go`)

LLM clients implement `Client` interface with:
- `Completion(ctx, Request) Response` - Streaming chat completion
- `CompletionNonStreaming(ctx, Request) (string, error)`
- `StructuredPredict(ctx, Request, model any) error` - Structured output

## Agent Setup Flow (`setup/setup.go`)

The setup package provides agent initialization:
- `NewAgent` - Creates AgentContext with all components
- `AgentContext` - Holds Client, Workspace, Session, Agent, Memory
- `Chat` method - Sends message to agent
- `PrintResponse` - Streams response to stdout
- Options: `WithSessionID`, `WithIsolate`, `WithTemporary`, `WithVerbose`

Setup flow:
1. Create provider client from config
2. Initialize workspace directory
3. Get or create session (from session manager)
4. Register compact hook for conversation summarization
5. Load workspace content and disk-defined Agent specs
6. Compose each disk Agent prompt as workspace prompt + AGENT-SPEC.md body
7. Create the primary Agent and named Agents with shared tools and hooks
8. Register named Agents for `run_task` and one-turn slash routing
9. Ensure memory log exists for today

## Skills System (`skills/`)

Skills are markdown-based extensions with YAML frontmatter:
- `skill.go` — Skill struct with Frontmatter (name, description, allowed_tools)
- `loader.go` — Loads SKILL.md files from directories
- `registry.go` — Skill registration and discovery
- `hook.go` — Injects skill instructions into session context

## Sandbox (`sandbox/`)

OS-level sandboxing and tool-safe execution:
- `sandbox.go` — Sandbox interface (`WrapCommand`, `IsAvailable`, `Name`) with noop fallback
- `seatbelt.go` / `bwrap.go` — macOS Seatbelt and Linux bubblewrap backends (`*_other.go` stubs, `bwrap_runtime.go`)
- `executor.go` — Command execution: bounded output buffers, timeouts, env scrubbing, permission checks
- `permission.go` — Permission allow/deny rules and decisions
- `approval.go` — Interactive command approval (`CommandApprover`, `FormPrompter`); project-scope grants persist via `project_overlay.go`
- `tool.go` — The agent-facing `bash` tool
- `fs_tool.go` — Native filesystem tools (`NewFsTools`) over a `FileSystem` backend with resolved-path access policy; parallel filename/path and content search with deterministic ordering
- `bg_task.go` / `bg_task_store.go` — Background task management
- `image_tool.go` — Image analysis tool
- `network_policy.go` — Network access policy
- `parser.go` — Command parsing for permission matching
- `config.go` / `defaults.go` — Sandbox configuration and defaults

## MCP Server (`mcp/`)

Model Context Protocol integration:
- `manager.go` — Multi-server manager: connections, warmup, tool snapshots, hot config reload
- `config.go` — MCP client-compatible JSON config loading with priority roots (workspace `mcp/` files)
- `server.go` — Legacy single Streamable HTTP client facade (deprecated; use Manager)
- `hook.go` — `BeforeAgent` hook injecting the latest MCP tool snapshot
- `trust.go` — Project MCP server trust model

## Skills Command (`cmd/skills.go`)

CLI for managing skills: list, install, delete.

# Behavioral Guidelines

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.

# Friday repository guidance

## Logging and terminal output

- Use `github.com/basenana/friday/core/logger` for runtime diagnostics. Create a named logger with `logger.New("component")`; do not use the standard-library `log` or `log/slog` packages.
- Code reachable from the TUI must never write logs or incidental diagnostics directly to stdout or stderr. This includes `fmt.Print*`, `fmt.Fprint*(os.Stdout/os.Stderr)`, built-in `print`/`println`, and libraries or child processes that inherit terminal output.
- Present user-visible TUI messages through Bubble Tea messages/model state. Return errors to the TUI update loop instead of printing them.
- Capture stdout and stderr from non-interactive child processes. Use `tea.ExecProcess` only for an intentional interactive handoff where Bubble Tea suspends and restores the terminal.
- Explicit stdout/stderr output is allowed for non-TUI CLI commands in `cmd/` when it is the command's documented user-facing output, not diagnostic logging.
- The application logger is file-backed. If its file cannot be opened, keep logging silent rather than falling back to a terminal stream.

When changing logging or TUI-reachable execution paths, run `go test ./core/logger ./core/actor ./core/providers/fallback ./tui` and verify that fallback/error paths produce no terminal output.
