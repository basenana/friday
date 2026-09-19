# agents — Core agent implementations and the ReAct loop

This package defines the minimal agent interface and the default tool-using ReAct implementation. Its subpackages provide simple completion, summarization, and leader/worker research agents.

## Files

- `interface.go` defines the cross-package `Agent` contract.
- `react.go` implements iterative model/tool execution, hooks, streaming, limits, and cancellation.
- `tools.go` validates and executes requested tools and records their results.
- `prompts.go` contains stable ReAct system guidance.
- `simple/simple.go` implements a direct model-backed agent.
- `summarize/summarize.go` and `summarize/prompt.go` implement summarization.
- `research/research.go` coordinates research workers.
- `research/subagent.go` builds the default worker.
- `research/report.go` aggregates worker results.
- `research/prompt.go` contains research prompts.

## Core API

```go
type Agent interface {
    Chat(ctx context.Context, req *api.Request) *api.Response
}

// core/agents
func New(llm providers.Client, option Option) Agent

// core/agents/simple
func New(llm providers.Client, opt Option) *Agent

// core/agents/summarize
func New(llm providers.Client, option Option) *Agent

// core/agents/research
func New(llm providers.Client, opt Option) *Agent
```

`Chat` returns immediately with an asynchronous `api.Response`; producers send deltas/errors and then close it.

## ReAct invariants

- The default maximum loop count is 500. Tests or callers may lower it, but documentation and safety checks must not assume the historical value 50.
- Each iteration builds a provider request from the session, injected prompts/tools, and hook projections.
- Tool calls are validated against the current tool set before invocation.
- Tool-call and tool-result messages remain paired in history. Cancellation and failures must not leave malformed history for the next turn.
- Hooks execute in their defined phases; ordering affects prompts, context projection, and persisted history.
- Streaming provider errors and tool errors are surfaced through `api.Response`, not hidden as successful assistant text.
- Context cancellation stops provider reads and prevents starting later tools.
- Concurrent tool execution must preserve deterministic result association by tool-call ID.
- Loop-limit termination is explicit so callers can distinguish it from a normal final answer.

## Specialized agents

- `simple` is appropriate when no iterative tool loop is required.
- `summarize` uses a dedicated prompt and returns concise history summaries.
- `research` delegates independent work to workers and assembles a report; worker ordering and report structure are part of its behavior.
- Specialized agents still satisfy the same `Agent` interface and must follow response-closing conventions.

## Tests

From the core module or repository workspace, run:

```bash
go test ./core/agents/...
```

Tests use fake providers and tools and are network-free. Add regression tests for hook order, cancellation, tool pairing, iteration limits, and response closure when changing the loop.
