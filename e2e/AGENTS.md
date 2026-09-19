# e2e — Opt-in end-to-end integration suite

This package validates Friday across provider, agent, actor, session, sandbox, planning, skills, MCP, coder, workspace, memory, and context-management boundaries. Its test files are deliberately excluded from ordinary test runs unless the `e2e` build tag is enabled; `config.go` remains an ordinary package file.

## Files

- `config.go` defines `E2EConfig`, config discovery, retry delays, and timeout defaults.
- `e2e_test.go` defines suite-level setup.
- `helpers_test.go` assembles real providers, agents, sessions, sandboxes, and assertions.
- `provider_test.go` covers streaming, structured output, context cancellation, and fallback.
- `react_test.go` covers real model/tool loops and filesystem/bash/background tools.
- `actor_test.go` and `actor_agui_test.go` cover actor and AG-UI event lifecycles.
- `session_test.go`, `contextmgr_test.go`, and `memory_test.go` cover persistence and compaction.
- `planning_test.go` covers TODO, LATS, simple, summarize, and research agents.
- `skills_test.go`, `subagent_test.go`, and `mcp_test.go` cover extension boundaries.
- `coder_test.go` and `loop_test.go` cover coder policy, commands, and autonomous work.
- `sandbox_test.go` covers permissions and Linux bubblewrap isolation.
- `workspace_test.go` covers prompt loading/composition.
- `testdata/` contains suite fixtures.

## Configuration API

```go
func LoadE2EConfig(path string) (*E2EConfig, error)
func FindE2EConfig() string
func (c *E2EConfig) BackoffDuration() time.Duration
func (c *E2EConfig) MaxAttempts() int
func (c *E2EConfig) TestTimeout() time.Duration
func (c *E2EConfig) SuiteTimeout() time.Duration
```

`FindE2EConfig` searches the supported local configuration locations. Keep secrets out of committed fixtures and logs.

## Running

```bash
go test -tags e2e ./e2e
```

Use focused `-run` expressions while developing. These are integration tests, not a substitute for fast package tests.

## Environment requirements

- Provider cases require real API credentials, reachable endpoints, and configured model names.
- Network variability is handled with bounded retries and suite/test timeouts; do not introduce unbounded sleeps.
- MCP tests start a local server and therefore require loopback listen permission.
- Linux isolation cases require `bwrap`; helpers detect availability and unsupported systems should skip appropriately.
- Model-driven assertions should verify durable behavior or broad semantic markers rather than exact prose.
- Every test must clean up sessions, temporary files, servers, and background work via `t.Cleanup`.

## Change policy

When adding a subsystem integration, first cover deterministic behavior in that subsystem's unit tests. Add E2E coverage only for boundaries that mocks cannot validate, and document any new credential, binary, network, or OS requirement here.
