# mcp — Multi-server MCP client manager: config, trust, tools, hot reload

Manages MCP (Model Context Protocol) server connections: layered JSON
configuration (the de-facto shape used by Claude/VS Code/Cursor), digest-bound
project trust, cached tool definitions, connection lifecycle with a status
machine, and hot config reload. Tools are adapted to internal `tools.Tool`
and injected per-turn via `NewHook`.

## Files

| File | Responsibility |
|---|---|
| `manager.go` | `Manager`: server lifecycle, status machine, connections, tool adaptation, hot reload |
| `config.go` | `ServerConfig`, layered JSON loading, stamp computation, transport normalization |
| `trust.go` | Digest-bound project trust store (atomic writes) |
| `server.go` | Deprecated single-server Streamable HTTP facade + the MCP↔internal tool converters |
| `hook.go` | `BeforeAgent` hook injecting the latest MCP tool snapshot |
| `logger.go` | Adapts mcp-go transport logging to the friday logger |

## Key API (verbatim signatures)

```go
type ManagerConfig struct {
    ConfigRoots      []workspace.ResourceRoot
    ProjectRoot      string
    Cache            fridaycache.Store
    CacheRoot        string
    TrustPath        string
    HandshakeTimeout time.Duration
    CacheTTL         time.Duration
}
func NewManager(config ManagerConfig) (*Manager, error)
func (m *Manager) Warmup(ctx context.Context)
func (m *Manager) Tools() []*tools.Tool
func (m *Manager) Status() []ServerStatus
func (m *Manager) Inspect(name string) (ServerStatus, error)
func (m *Manager) Refresh(ctx context.Context, name string) error
func (m *Manager) Reconnect(ctx context.Context, name string) error
func (m *Manager) Trust(ctx context.Context, name string) error
func (m *Manager) Untrust(_ context.Context, name string) error
func (m *Manager) Close() error
func (m *Manager) ConfigSourceSummary(name string) (string, error)

type ServerConfig struct {
    Type TransportType; Command string; Args []string; Env map[string]string
    Cwd string; URL string; Headers map[string]string; Disabled bool
    IncludeTools []string; ExcludeTools []string
    Source string `json:"-"`; Project bool `json:"-"`; Digest string `json:"-"`; Name string `json:"-"`
}
func LoadConfigRoots(roots []workspace.ResourceRoot) (map[string]ServerConfig, error)
func ConfigFilesStamp(roots []workspace.ResourceRoot) (string, error)
func (c *ServerConfig) Normalize() error

type ToolProvider interface { Tools() []*tools.Tool }
func NewHook(provider ToolProvider) *Hook
```

Server statuses: `disabled | blocked | cached | starting | ready | degraded |
failed | closed`.

## Behavior invariants

- Trust flow: only project-scoped servers (`root.Scope == workspace.ScopeProject`) start `blocked` without trust. Trust is **digest-bound** — the stored digest (`<dataRoot>/states/mcp_trust.json`, keyed `projects[canonicalProject][name]`) must match the current config digest; a config change re-blocks. HOME/shared-workspace servers are implicitly trusted. `Trust()` moves `blocked → starting` then connects; `Untrust()` closes the client and returns to `blocked`. Trust documents save atomically (temp+rename, 0600, dir 0700).
- Hot reload: every public method calls `reloadConfigIfChanged()`; `ConfigFilesStamp` is an mtime+size hash (cheap). Invalid edits retain the last usable generation and retry on the next stamp change; removed servers retire gracefully — connections close when active calls drain to zero.
- Digest-bound calls: adapted tool handlers capture the server's config digest; if config changed between list and call, the call errors ("retry with the refreshed tool list"). Tool names are namespaced `mcp__<server>__<tool>`.
- Status machine: lost connection → `degraded` (previously listed tools kept); failure with no prior tools → `failed`; `tools/list_changed` notifications trigger a debounced (200ms) tool refresh.
- Cache: tool definitions cached under namespace `"mcp"`, key `safePart(name) + "_" + digest[:16]`, default TTL 24h; a cache hit starts as `cached` (tools usable before background Warmup connects).
- Env expansion: `${VAR}` expanded in command, args, cwd, env values, URL, headers. Error sanitization strips user/query/fragment from URLs and redacts header/env values.
- Transports: `stdio` (optional `cwd`), `streamable-http` (default), `sse` (legacy); aliases `http`, `streamablehttp`, `streamable_http`, `cmd`, `command`; empty type + command → `stdio`. `includeTools`/`excludeTools` filter listed tools.
- `server.go` (`Server`, `MCPSse`) is the deprecated legacy facade — the MCP result/error converters live there; MCP `IsError` results become actionable tool errors with the full response as FYI.
- The hook reads a fresh `Tools()` snapshot every turn, sorted by (name, description), deduped against existing tool names; inherited by forked sessions.

## Tests

`manager_test.go` (httptest servers: tools+cache+auth, digest-bound project
trust, shared HOME stays trusted, stdio, legacy SSE, helper processes, hot
reload, keep-last-valid — **binds local ports and spawns subprocesses**),
`config_test.go` (layering, file vs mcpServers precedence, stamp excludes
source), `server_test.go` (result/schema conversion), `hook_test.go` (sorting,
fresh read per turn), `logger_test.go`. The manager tests are
environment-constrained (local ports); the rest are pure.
