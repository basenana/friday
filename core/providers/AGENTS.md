# providers — Model client contracts and provider implementations

This tree defines the provider-neutral request/response interface, reasoning and observability capabilities, shared parsing/retry helpers, concrete OpenAI/Anthropic clients, and multi-endpoint fallback routing.

## Files and subpackages

- `interface.go` defines `Client`, `Request`, optional capabilities, streaming responses, tool definitions, and usage data.
- `common.go` implements the standard mutable request/response types.
- `reasoning.go` resolves request, policy, and model reasoning effort.
- `retry_observer.go` exposes retry observability.
- `common/parse.go` parses structured model output.
- `common/repair.go` repairs recoverable structured-output syntax.
- `common/retriable.go` classifies provider errors for retry.
- `common/retry.go` runs bounded retry loops.
- `common/structured_predict.go` implements shared structured prediction.
- `common/token_fallback.go` estimates usage when a provider omits tokens.
- `openai/client.go` implements OpenAI Chat Completions.
- `openai/compatible.go` adapts OpenAI-compatible endpoints.
- `openai/embedding.go` implements vector embeddings.
- `openai/parser.go` translates OpenAI streams and tool calls.
- `openai/prompts.go` contains OpenAI-specific prompt guidance.
- `openai/types.go` defines adapter-local types.
- `anthropics/client.go` implements Anthropic Messages.
- `anthropics/prompts.go` contains Anthropic-specific prompt guidance.
- `openairesponse/client.go` invokes the OpenAI Responses API.
- `openairesponse/response.go` translates Responses API events.
- `openairesponse/types.go` defines adapter-local types.
- `fallback/fallback.go` implements candidate routing and failover.
- `fallback/errors.go` defines aggregate failure types.
- `fallback/image.go` routes image-analysis requests.
- `fallback/options.go` defines fallback construction options.

## Core API

```go
type Client interface {
    Completion(ctx context.Context, request Request) Response
    CompletionNonStreaming(ctx context.Context, request Request) (string, error)
    StructuredPredict(ctx context.Context, request Request, model any) error
}

type ForkableClient interface {
    Fork(ClientPolicy) Client
}
```

Optional interfaces expose context windows, output limits, model names, runtime routing snapshots, embeddings, and reasoning effort without forcing every custom client to implement them.

## Request and streaming invariants

- Provider adapters must preserve message roles, tool-call IDs, image content, and tool-result pairing.
- Streaming responses send deltas/errors and close exactly once on every completion, error, or cancellation path.
- Structured prediction uses shared repair/parsing rules rather than provider-specific ad hoc JSON extraction.
- Retry only errors classified as retriable, and always respect context cancellation and configured bounds.
- Never log API keys or full sensitive request payloads.

## Reasoning and fallback

- Reasoning effort resolution honors explicit request/session/agent policy before model defaults; `default` does not override a concrete value.
- Fallback candidates are grouped/routed by request class and policy version.
- A request tries each eligible physical candidate at most once.
- Successful routing is sticky for the relevant client policy view, while forked views keep independent runtime snapshots.
- A preferred model promotes every configured endpoint exposing that model name; it is not an endpoint key.
- Aggregate failure must retain actionable per-candidate errors.

## Tests

Run:

```bash
go test ./core/providers/...
```

Provider unit tests use fake transports and are network-free. Real credentials and endpoints belong only in `e2e` tests.
