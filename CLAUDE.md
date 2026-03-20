# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
- **Root module** (`/`) — CLI application, config, workspace, memory, skills, sandbox, MCP
- **Core module** (`/core`) — Agent interfaces, providers, session management, tools, planning

The `core` package is a separate module with its own `go.mod`. See `core/CLAUDE.md` for details.

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
- `session.go` - Session management (list, new, use, show, archive, delete)
- `heartbeat.go` - Send periodic tasks defined in HEARTBEAT.md

### Agent System (`core/agents/`)

The `agents/` package defines the `Agent` interface (`core/agents/interface.go:9-11`):
```go
type Agent interface {
    Chat(ctx context.Context, req *api.Request) *api.Response
}
```

- `react.go` - ReAct-style agent with thought/action/observation loop (max 50 iterations)
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
- `tool.go` - Main agent tool `run_task` delegates to registered expert agents

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

### Session Storage (`session/`)

- `manager.go` - Session manager for current session tracking
- `store.go` - Session store interface
- `file/store.go` - File-based session persistence

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
└── workspace/           # Agent context files
```

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
5. Load workspace content (system prompts + memory history)
6. Create agent with system prompt and tools
7. Ensure memory log exists for today

## Skills System (`skills/`)

Skills are markdown-based extensions with YAML frontmatter:
- `skill.go` — Skill struct with Frontmatter (name, description, allowed_tools)
- `loader.go` — Loads SKILL.md files from directories
- `registry.go` — Skill registration and discovery
- `hook.go` — Injects skill instructions into session context

## Sandbox (`sandbox/`)

OS-level sandboxing for secure command execution:
- `sandbox.go` — Sandbox interface (WrapCommand, IsAvailable, Name)
- `seatbelt.go` — macOS sandbox using Seatbelt framework
- `bwrap.go` — Linux sandbox using bubblewrap
- `executor.go` — Command execution with sandbox integration
- `permission.go` — Permission definitions (filesystem, network)

## MCP Server (`mcp/`)

Model Context Protocol integration:
- `server.go` — Connects to MCP SSE endpoints and converts MCP tools to internal Tool format

## Skills Command (`cmd/skills.go`)

CLI for managing skills: list, install, update installed skills.

