# coder — Project-aware coding agent engine

The `coder` tree assembles disk-defined expert agents, slash commands, guarded configuration and file tools, autonomous coding loops, and project-scoped sessions. It is used by the TUI's project mode and by coder-specific E2E tests.

## Package map and files

### `agents`

- `builtins.go` names built-in agent specifications.
- `spec.go` defines the disk `AGENT-SPEC` schema.
- `loader.go` loads and merges specifications.
- `registry.go` keeps the current valid specification set.
- `factory.go` creates provider clients and agents from specs.
- `expert_provider.go` exposes specs as subagent experts.
- `router.go` routes requests between the primary and experts.
- `policy.go` filters tool access by agent role.
- `tools.go` resolves named tools.
- `explorer.go` defines the read-only explorer role.
- `prompts.go` and `prompts_compose.go` build coder prompts.

### Other subpackages

- `commands/command.go` defines command requests, results, and handler contracts.
- `commands/registry.go` registers and resolves slash commands.
- `commands/builtin.go` assembles the built-in command set.
- `commands/agents.go` implements agent-related commands.
- `commands/info.go` implements informational commands.
- `commands/session.go` implements session commands.
- `commands/ui.go` implements UI-directed command actions.
- `configtools/store.go` validates and persists controlled agent/model/MCP configuration.
- `configtools/hook.go` exposes configuration operations as tools.
- `filetools/hook.go` injects project instructions and protects instruction-file mutations.
- `loop/state.go` defines loop state, phase, and transition validation.
- `loop/manager.go` owns autonomous loop execution and cancellation.
- `loop/hook.go` integrates loop state with agent turns.
- `loop/prompts.go` defines autonomous-loop prompts.
- `project/project.go` defines canonical project identity and paths.
- `project/file_store.go` persists project metadata.
- `project/lock_unix.go` implements Unix project-file locking.
- `project/lock_windows.go` implements Windows project-file locking.
- `project/lock_other.go` provides fallback locking on other platforms.
- `project/manager.go` opens root and forked project sessions.
- `project/user_history.go` stores project-local input history.

## Key constructors

```go
// coder/agents
func NewLoader(paths ...string) *Loader
func NewClientFactory(primary providers.Client) *ClientFactory
func NewExpertProvider(specs SpecProvider, factory *ClientFactory, workspacePrompt string, allTools []*tools.Tool, validate SpecValidator) (*ExpertProvider, error)
func NewDynamicRouter(primary coreagents.Agent, provider subagents.AgentProvider) *Router

// coder/filetools
func New(exec *sandbox.Executor, root string, opts ...Option) (*Hook, error)

// coder/loop
func NewManager(b *eventbus.Bus, opts ...ManagerOption) *Manager
func (m *Manager) Start(ctx context.Context, sess *session.Session, task string) error

// coder/project
func NewManager(project *Project, manager *sessions.Manager) *Manager
func (m *Manager) OpenRoot(ctx context.Context, id string, client providers.Client, opts ...coresession.Option) (sessions.SessionLifecycle, error)
```

## Agent specifications

- Agent specs are loaded from the user's Friday home first and then from the project, so the project definition overrides the same named home definition.
- Reloading is transactional: parse and validate the new set before replacing the last valid registry.
- Tool policy is enforced when building an expert; a prompt declaration alone is not a security boundary.
- Explorer remains read-only. Do not add mutating tools to its policy casually.
- Dynamic routing reads the current provider so hot reload does not require rebuilding the primary agent.

## Project instruction handling

- `filetools` walks from project root toward the target directory and injects the applicable instruction files.
- Within one directory, `AGENTS.md` takes precedence over `CLAUDE.md`. The latter remains supported because coder operates on external projects.
- Instruction context is informational for reads but mutations of an instruction file require the existing read/acknowledgement guard.
- Resolve and validate paths through the sandbox/project root. Do not trust lexical prefix checks.

## Loop and project invariants

- Loop state and phase transitions use compare-and-swap style validation; stale transitions must fail rather than overwrite newer state.
- Only one active loop owns a session. Cancellation and `Close` must unblock waiters and release subscriptions.
- Project roots are canonicalized before deriving identity or storage paths.
- File-backed project metadata is protected by process and OS locks and written atomically.
- Root sessions and forks are opened through `sessions.Manager`; preserve lifecycle release behavior.

## Tests

Run all coder packages with:

```bash
go test ./coder/...
```

Most tests are hermetic. Tests that exercise external-project instruction discovery intentionally cover both `AGENTS.md` and legacy `CLAUDE.md` behavior.
