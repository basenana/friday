# CLAUDE.md (core)

This file provides guidance for the `core` module — the reusable agent framework.

## Module Overview

The `core` module is a standalone Go module (`github.com/basenana/friday/core`) providing:
- Agent interfaces and implementations (ReAct loop)
- LLM provider clients (OpenAI, Anthropic)
- Session management with hook system
- Tool definitions and execution
- Planning algorithms (LATS, TODO)

## Key Interfaces

### Agent (`agents/interface.go`)
```go
type Agent interface {
    Chat(ctx context.Context, req *api.Request) *api.Response
}
```

### Provider Client (`providers/interface.go`)
```go
type Client interface {
    Completion(ctx context.Context, request Request) Response
    CompletionNonStreaming(ctx context.Context, request Request) (string, error)
    StructuredPredict(ctx context.Context, request Request, model any) error
}
```

### Tool Definition (`tools/tool.go`)
Tools have Name, Description, InputSchema (JSON Schema), and a Handler function.

## Architecture

### ReAct Agent (`agents/react.go`)

The core agent implements a thought/action/observation loop:
1. Receive user message → append to session history
2. Call LLM with tools defined
3. If tool calls returned → execute tools, append results, go to step 2
4. If no tool calls → stream response to user
5. Max 50 iterations (configurable via `MaxLoopTimes`)

### Session (`session/session.go`)

Session holds:
- `History` — conversation messages
- `Workdir` — in-memory filesystem for tool file operations
- `hooks` — lifecycle hooks (BeforeAgent, BeforeModel, AfterModel)
- `Root/Parent/Children` — tree structure for forking sub-agents

Sessions can be forked (`sess.Fork()`) for sub-agent execution.

### Hook System (`session/hooks.go`)

Three hook types:
- `BeforeAgentHook` — called before agent starts processing
- `BeforeModelHook` — called before each LLM call (can modify request)
- `AfterModelHook` — called after LLM response (can modify tool calls)

Hooks are used for:
- Conversation compaction (`summarize/hook.go`)
- Subagent tool injection (`subagents/hook.go`)
- TODO tracking (`planning/todo.go`)

### Providers (`providers/`)

- `openai/client.go` — OpenAI-compatible API with streaming
- `openai/compatible.go` — Adapters for Ollama, Gemini
- `openai/embedding.go` — Vector embeddings
- `anthropics/client.go` — Anthropic Claude API

### Planning (`planning/`)

- `lats/` — LATS (Language Agent Tree Search) with candidate generation and evaluation
- `todo.go` — TODO-based planning with hook integration

## Message Types (`types/session.go`)

Roles: `system`, `user`, `assistant`, `agent`, `tool`

Messages can contain:
- `Content` — text content
- `Reasoning` — chain-of-thought (for models that support it)
- `ToolCalls` — tool invocations (assistant messages)
- `ToolResult` — tool execution results (tool messages)

## Testing

Run tests from the core directory:
```bash
cd core && go test ./...
```