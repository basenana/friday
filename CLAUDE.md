# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Friday is a multi-agent AI system with RAG capabilities. Core architecture includes:
- ReAct-style agents with tool-calling (max 5 loop iterations, 20 tool calls)
- LATS (Language Agent Tree Search) for structured reasoning with tree exploration
- Sub-agent orchestration for expert task delegation

## Commands

```bash
make build    # Build for darwin/arm64, darwin/amd64, linux/arm64, linux/amd64
make test     # Run all unit tests (go test ./...)
```

## Architecture

### Entry Point

- `cmd/main.go` - Cobra CLI with `serve` (HTTP API) and `agent` commands

### Agent System (`agents/`)

The `agents/` package defines the `Agent` interface (`agents/interface.go:9-11`):
```go
type Agent interface {
    Chat(ctx context.Context, req *api.Request) *api.Response
}
```

- `react.go` - ReAct-style agent implementation with thought/action/observation loop
- `lats/` - LATS (Language Agent Tree Search) for tree-based reasoning with parallel node evaluation
- `summarize/` - Specialized agent for response synthesis

### Session System (`session/`)

Session manages conversation state and tool execution context:
- `session.go` - Core session with message history, token tracking, and workdir filesystem
- `hooks.go` - Hook system: `BeforeAgent`, `BeforeModel`, `AfterModel` lifecycle hooks
- `compact.go` - Conversation compaction/shortening utilities

Sessions support forking for sub-agent execution (`session/session.go:46-64`).

### Subagents (`subagents/`)

Orchestrates expert sub-agents:
- `hook.go` - Registers `BeforeAgent` and `BeforeModel` hooks to inject subagent tools
- Main agent tool `run_task` delegates to registered expert agents

### Planning (`planning/`)

- `lats/` - LATS reasoning tree with candidate generation, parallel execution, and evaluation
- `todo/` - TODO-based planning with hook integration

### Providers (`providers/openai/`)

- `client.go` - OpenAI-compatible API client with streaming support, rate limiting, and retry logic
- `types.go` - Request/Response models for LLM interactions
- `embedding.go` - Vector embedding generation

### Tools (`tools/`)

- `tool.go` - Tool definition with JSON Schema, handlers, and property builders
- Tools are composable with options pattern (WithDescription, WithString, etc.)

### API (`api/`)

- `requests.go` - Request/Response types for agent chat
- `context.go`, `stream.go` - HTTP context and streaming utilities

### Core Types (`types/`)

- `Message` - Conversation messages (system/user/assistant/agent/tool roles)
- `SessionType` - Hook type constants (`BeforeAgent`, `BeforeModel`, `AfterModel`)

### Filesystem (`fs/`)

- `inmemory.go` - In-memory filesystem for session workdir (default)

## Deprecated (`pkg/`)

Previous version implementations. Reference only - do not use in new code.

## Provider Interface (`providers/interface.go`)

LLM clients implement `Client` interface with:
- `Completion(ctx, Request) Response` - Streaming chat completion
- `CompletionNonStreaming(ctx, Request) (string, error)`
- `StructuredPredict(ctx, Request, model any) error` - Structured output
