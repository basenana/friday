# setup — Agent assembly: wires client, workspace, session, memory, tools, hooks

Builds a fully-wired agent (`AgentContext`) from config + session manager:
provider client/model pool, workspace, session (existing/isolated/temporary/
lifecycle-owned), memory, tools (fs/image/bash/background tasks), the full
hook chain (planning, collaboration, skills, MCP, context, memory, loop,
filetools, approved plan, subagents, config tools), and the shared tool
invocation pipeline (retry + trace). Consumed by `cmd` (chat/heartbeat/
sunrise), `actor.Registry`, and the TUI/daemon paths.

## Files

| File | Responsibility |
|---|---|
| `setup.go` | `AgentContext` aggregate + `NewAgent`/`NewAgentWithLifecycle` wiring order, `Option` setters, hook composition, `Chat`/`ChatWithImageRefs`, `PrintResponse`, `Close` |
| `provider.go` | Provider construction: `CreateProviderClient`, `CreateModelPool` (one leaf client per catalog entry, pooled view sharing transports), `CreateProviderClientFromModel` (anthropic/openai/openai-response), image analyzer with config-signature caching |
| `memory_hook.go` | `BeforeModel` hook re-injecting the latest workspace memory into every request history at compose time (re-read from disk; persisted history is never modified) |
| `tool_retry.go` | Tool invocation retry middleware: default 3 attempts, 500ms base exponential backoff + jitter, 30s cap; honors `Retryable()` errors and `result.Retryable`; context cancellation never retries |
| `tool_trace.go` | Tool invocation trace middleware + `ToolTraceEvent` (start/end/error, invocation IDs, retry chains, redacted+budgeted params/results), evidence context propagation |

## Key API (verbatim signatures)

```go
type AgentContext struct {
    Client      providers.Client
    Policy      *fallback.SessionPolicy
    Workspace   *workspace.Workspace
    Lifecycle   sessions.SessionLifecycle
    Session     *coreSession.Session
    Agent       agents.Agent
    Memory      *memory.MemorySystem
    TaskManager *sandbox.TaskManager
    Approver    *sandbox.CommandApprover
}

func NewAgent(sessionMgr SessionManager, cfg *config.Config, opts ...Option) (*AgentContext, error)
func NewAgentWithLifecycle(lifecycle sessions.SessionLifecycle, services SessionManager, cfg *config.Config, opts ...Option) (*AgentContext, error)
func (ac *AgentContext) Close()
func (ac *AgentContext) Chat(ctx context.Context, message string) *api.Response
func (ac *AgentContext) ChatWithImageRefs(ctx context.Context, message string, imageRefs ...string) *api.Response
func PrintResponse(resp *api.Response)
```

Options: `WithSessionID`, `WithIsolate`, `WithTemporary`, `WithVerbose`,
`WithExtraTools`, `WithProviderClient`, `WithModelPool`, `WithSessionPolicy`,
`WithSkillRegistry`, `WithAgentRegistry`, `WithMCPManager`, `WithWorkdir`,
`WithConfigTools`.

```go
// provider.go
func CreateProviderClient(cfg *config.Config) (providers.Client, error)
func CreateModelPool(cfg *config.Config) (*fallback.ModelPool, error)
func CreateProviderClientFromModel(modelCfg config.ModelConfig) (providers.Client, error)

// Session selection contract
type SessionManager interface {
    SetLLM(llm providers.Client)
    GetOrCreateByID(sessionID string, opts ...coreSession.Option) (*coreSession.Session, bool, error)
    GetOrCreateDetachedByID(sessionID string, opts ...coreSession.Option) (*coreSession.Session, bool, error)
    CreateIsolated(opts ...coreSession.Option) (*coreSession.Session, string, error)
    CreateTemporary(opts ...coreSession.Option) (*coreSession.Session, string, error)
    GetOrCreateCurrent(opts ...coreSession.Option) (*coreSession.Session, string, bool, error)
}
```

## Behavior invariants

- Client construction order: `WithProviderClient` wins; else `WithModelPool`; else build pool from config. Session-policy fallback comes from the client view (`SessionPolicy()`) or a fresh default.
- Session selection priority: explicit lifecycle (`Current()`) > `WithSessionID` > isolated > temporary > current. When setup owns the session (no lifecycle), `sessionMgr.SetLLM` is called on the built client; with a lifecycle it is skipped.
- MCP ownership: `WithMCPManager` leaves ownership to the caller (typically `actor.Registry`); otherwise setup builds + warms the manager and closes it in `Close`. Build failure degrades to a warning + nil tools.
- Hook order (registration order): `sessionusage.Hook`, planning, mcp, skills, memory (**before** the context manager so projection counts memory messages), contextmgr, refocus, filetools (**after** projection so stable project context is rebuilt and compaction cannot drop it), approved-plan, subagents, collaboration (**last** = highest request-scoped precedence for Plan mode), `planning.TerminalHook`, then optional configTools (stable final position, inherited by forked sessions). `loopHook` registers separately via `sess.RegisterHook`. `replaceSessionHooks` clears existing hooks first.
- System prompt: `coderagents.ComposeSystemPrompt(agents.DEFAULT_SYSTEM_PROMPT, workspace.ComposeSystemPrompt(loaded))`; explorers reuse the same base prompt (shared cache prefix); experts come from disk specs and reject unknown model selections; the main agent is wrapped in `coderagents.NewDynamicRouter` for expert routing.
- Every tool (including hook-injected ones) goes through one shared `tools.NewInvoker` with retry (outermost) + trace middleware; each attempt gets a fresh UUID `InvocationID`, `RetryOfInvocationID` chains retries; trace sink activates only when `cfg.Log.Enabled`.
- Memory hook: memory is re-read at compose time and injected per request only — never persisted into history (`TestNewAgentMemoryInjectedPerRequestNotPersisted`).
- `CommandApprover` is built only when the CWD has a resolvable project root; interactive runs bind it to the actor form prompter; headless stays unbound (denials surface as actionable errors).
- `Close` order: `TaskManager.KillAll()` **before** session/lifecycle teardown (running tasks must not write events to a dead bus); owned MCP closes last.
- The image analyzer caches clients keyed by the full config signature (provider, base_url, key, model, resize params, proxy, effort, split).

## Tests

- `setup_test.go` — image ref appending, workspace loads system prompt, disk agents reuse primary client/prompt + run_task registration, config tools require explicit enable, runtime model/effort restoration, per-request memory injection, managed-session hook installation.
- `provider_test.go` — openai-response provider + `responses` alias, model pool carries runtime metadata.
- `memory_hook_test.go` — fresh memory per request, no-op without memory, trace sink disabled without log config.
- `tool_trace_test.go` — start/fail events, retry evidence, redaction/caps, backoff jitter, default policy constants, no retry after cancellation.

All tests are filesystem/fakes; no network, ports, or git required.
