# tools — Tool definitions, schema validation, and invocation

This package is the provider-independent tool layer. It defines JSON-schema-backed tools, handler requests/results, validation helpers, error semantics, and the invoker that enforces timeouts and panic containment.

## Files

- `tool.go` defines `Tool`, options, request/result types, handlers, and result constructors.
- `validate.go` validates tool names, schemas, and request arguments.
- `invoker.go` executes handlers with deadlines, tracing, and normalized failure categories.
- `markdown.go` formats selected results for prompt-safe display.
- `utils.go` contains small schema/value helpers.

## Key API

```go
func NewTool(name string, options ...ToolOption) *Tool
func NewInvoker(options ...InvokerOption) *Invoker
func (i *Invoker) Invoke(ctx context.Context, tool *Tool, request *Request) (*Result, error)
func NewToolResultText(text string) *Result
func NewToolResultError(text string) *Result
func NewToolResultRetryableError(text, reason string) *Result
func NewToolResultActionableError(cause, suggestion string) *Result
```

Tool options construct schema properties, required fields, handlers, examples, descriptions, and timeout behavior. Prefer these builders over hand-written unvalidated maps.

## Validation invariants

- Tool names and schemas must be valid before exposure to a provider.
- Invocation validates arguments against the tool schema before calling the handler.
- Unknown properties, required fields, arrays, enums, and nested values follow the validator's explicit rules; schema changes require contract tests.
- A model-generated request is untrusted input. Handlers must still enforce filesystem, network, and authorization boundaries.
- Tool-call IDs are transport identifiers and must be preserved in results.

## Invocation behavior

- Caller cancellation and tool timeout are distinct failure classes.
- Panics are recovered and returned as tool failures instead of crashing the agent process.
- Result-level business failures remain results; infrastructure failures returned as Go errors are handled by the agent loop.
- Retryable and actionable errors carry machine-readable semantics plus human guidance.
- Invoker tracing must not leak large arguments; use bounded attributes.
- Never start a handler after the context is already canceled.

## Tests

Run:

```bash
go test ./core/tools
```

Tests are hermetic. Add cases for schema boundaries, cancellation races, timeout classification, panic recovery, and result semantics for every new option.
