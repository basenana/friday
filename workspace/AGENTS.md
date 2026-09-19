# workspace — Layered markdown workspace for agent context files

Loads the markdown workspace (AGENTS.md, SOUL.md, IDENTITY.md, memory logs,
…) into system prompts and conversation history. Supports a HOME→project
layered overlay where the project layer overrides HOME per file and only one
layer is writable; also resolves resource roots (skills/, mcp/) through the
same layers. Consumed by `setup` (system prompt + memory history), `skills`
(`SkillsPaths`), and `mcp` (`MCPRoots`).

## Files

| File | Responsibility |
|---|---|
| `types.go` | `FileRole`/`FileSpec`/`LoadedContent` value types; `Paths`/`SystemInfo`/`TemplateParams` |
| `workspace.go` | `Workspace` with overlay `Layer`s (lowest→highest priority; exactly one writable layer), read/write path resolution, resource roots, init |
| `loader.go` | `Load` composing system prompts + memory history; daily memory-log windowing |
| `prompts.go` | `ComposeSystemPrompt` (joins non-empty prompts with a blank line) |
| `states.go` | `FileState`: JSON-file implementation of `core/state.State` (app/user scopes) |
| `defaults.go` | `DefaultContents` map (7 file templates) + `RenderTemplate` (text/template) |

## Key API (verbatim signatures)

```go
type FileRole string // "system_prompt" | "guidance" | "optional" | "memory"

type FileSpec struct {
    Name     string
    Role     FileRole
    Required bool
}

type LoadedContent struct {
    SystemPrompts []string
    MemoryHistory []types.Message
}

func NewWorkspace(workspacePath, memoryPath string, fallbackPaths ...string) *Workspace
func NewFromConfig(config Config) *Workspace
func (w *Workspace) Load(opts ...LoadOption) (*LoadedContent, error)
func WithMemoryDays(days int) LoadOption
func (w *Workspace) Layers() []Layer
func (w *Workspace) ResourceRoots(relativePath string) []ResourceRoot
func (w *Workspace) MCPRoots() []ResourceRoot
func (w *Workspace) WritablePath(relativePath string) (string, error)
func (w *Workspace) InitWithParams(params *TemplateParams) ([]string, error)
func ComposeSystemPrompt(content *LoadedContent) string
func RenderTemplate(tmpl string, params *TemplateParams) (string, error)
func NewFileState(basePath string) state.State
```

Also: `type Layer struct { Root string; Scope Scope; Writable bool }` with
`Scope` = `home|project`; `ErrReadOnlyWorkspace`; the `Config` interface is
`WorkspacePath() / WorkspaceFallbackPaths() / ProjectScoped() / MemoryPath()`.

## Behavior invariants

- Fixed spec set: AGENTS.md, SOUL.md, IDENTITY.md (system_prompt roles; AGENTS.md is the only Required file); ENVIRONMENT.md, TOOLS.md (guidance — **not** loaded into context); MEMORY.md (memory); HEARTBEAT.md (optional). SystemPrompts preserve spec order: AGENTS.md, SOUL.md, IDENTITY.md.
- Reads walk layers from highest→lowest priority; writes/deletes always target the single writable layer. `Ls` merges names across layers, sorted, and returns `os.ErrNotExist` only if no layer has the dir.
- `NewFromConfig` builds the HOME→project overlay; if the project's active workspace **is** the HOME workspace (shared), that layer stays read-only — `WritablePath` then returns `ErrReadOnlyWorkspace` so project operations cannot mutate inherited global resources. A project-path symlink to HOME keeps project scope.
- A missing file loads as empty (`("", nil)`); only `Required: true` (AGENTS.md) errors.
- Memory window: `DefaultMemoryDays = 2` (today + yesterday); dates parsed from filenames (`2006-01-02`); logs whose content is empty or just `# <date>` are skipped; MEMORY.md empty/`# MEMORY.md` is skipped. Memory lands in `MemoryHistory` as `types.Message{Role: RoleAgent}` under `[Long-Term Memory]` / `[Recent Memory Context]` headers.
- `SkillsPaths()` returns low→high priority, directly consumable by `skills.NewLoader` (later dirs override); `MCPRoots()` retains each dir's trust `Scope`.
- `FileState` keys live in `app_state.json` / `user_state_<userID>.json` as one JSON map per scope; `Get` on a missing key returns a plain error (not a sentinel); `SetRoot` is deprecated.

## Tests

`workspace_test.go` (12 tests): constructor, layered project-override + fallback, scoped layer build, shared-HOME read-only, symlink scope, `Ls` merge, idempotent init (only missing files written), load roles/order, long-term + recent memory inclusion, `WithMemoryDays` (incl. <1 fallback), prompt compose, template render. Pure `t.TempDir` filesystem tests — no environment constraints.
