# api — Agent request and streaming response boundary

This package contains the small public API exchanged between callers and `agents.Agent`: turn input, session/tool context, asynchronous deltas, errors, and stream collection helpers.

## Files

- `requests.go` defines `Request`, `Response`, input precedence, tool injection, and delta sending.
- `stream.go` collects a response stream into text with cancellation.
- `context.go` stores API-specific values in `context.Context`.
- `requests_test.go` covers compatibility and input precedence.

## Public API

```go
type Request struct {
    Session      *session.Session
    UserMessage  string
    AgentMessage string
    Image        *types.ImageContent
    Images       []types.ImageContent
    ImageURLs    []string
    Metadata     map[string]string
    Tools        []*tools.Tool
}

func NewResponse() *Response
func SendDelta(resp *Response, delta types.Delta, extraKV ...string)
func ReadAllContent(ctx context.Context, resp *Response) (string, error)
```

`Response` exposes:

```go
func (r *Response) Deltas() <-chan types.Delta
func (r *Response) Error() <-chan error
func (r *Response) Fail(err error)
func (r *Response) Close()
```

## Contracts

- `Request.InputMessage` gives `AgentMessage` precedence over `UserMessage`; this supports agent-originated turns without pretending they are user text.
- Legacy singular `Image` and plural image fields coexist for compatibility. Preserve both until an intentional API migration.
- `AppendTools` extends the request-local tool set and must not mutate a session-global registry.
- Producers own `Response.Close` and must call it exactly once.
- Error and delta channels are independent; consumers must select until error or delta closure rather than assuming one arrives first.
- `ReadAllContent` concatenates only textual delta content and returns partial content with the terminating error when applicable.
- Respect caller cancellation while consuming a response.
- Metadata keys are cross-package contracts; document and test any new key at its producer and consumer.

## Tests

Run:

```bash
go test ./core/api
```

Keep this package dependency-light. Higher-level agent and provider behavior belongs in their own packages.
