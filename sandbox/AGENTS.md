# sandbox — Secure command execution, native filesystem tools, and approvals

Friday's tool-safety layer: OS command isolation (macOS Seatbelt, Linux
bubblewrap), allow/deny command permissions, interactive approval with
HOME-side project grants, native `fs_*` tools under a canonical path policy,
persistent background tasks, and the network-restricted image tool. `setup`
constructs these tools and exposes them to agents.

## Files

| File | Responsibility |
|---|---|
| `sandbox.go` | `Sandbox` interface, `ExecOptions`/`Result`; OS backend selection and explicit `NoSandbox` |
| `filesystem_policy.go` | Shared command-start filesystem policy compiler: path/glob expansion, canonicalization, priority, object classification |
| `seatbelt.go`, `seatbelt_*.sb`, `seatbelt_other.go` | macOS deny-default Seatbelt policy assembly/wrapper and non-darwin stub |
| `bwrap.go`, `bwrap_runtime.go`, `bwrap_other.go` | Linux bubblewrap args/probe/runtime mounts and non-linux stub |
| `executor.go` | Permission → timeout → fail-closed isolation → `bash -c`; bounded output, process-group termination, environment scrubbing |
| `permission.go` | Thread-safe allow/deny matching, `DeniedError`, hot `Grant` |
| `parser.go` | Shell AST parsing with mvdan/sh; wildcard pattern matching |
| `approval.go` | Interactive command approval (`CommandApprover` + actor form bridge) |
| `project_overlay.go` | HOME-side per-project grant document loading/validation/atomic append |
| `tool.go` | Agent-facing `bash` tool and denial routing |
| `fs_tool.go` | `FileSystem` backend, seven native fs tools, canonical path policy, deterministic parallel filename/path and content search |
| `bg_task.go`, `bg_task_store.go` | Background task lifecycle/tools and session-record persistence |
| `image_tool.go` | Image-analysis tool, local image resize/compression, remote download |
| `network_policy.go` | Outbound URL host/IP/CIDR/port and redirect policy used by image downloads |
| `config.go`, `defaults.go` | Config loading/validation/runtime defaults and default policies |
| `utils.go` | Home/path expansion and memory-limit parsing |
| `process_group_unix.go`, `process_group_darwin.go`, `process_group_linux.go`, `process_group_windows.go` | Platform session/process-group setup and process-tree termination |

## Key API

```go
type Sandbox interface {
    WrapCommand(cmd string, opts ExecOptions) (wrappedCmd string, cleanup func(), err error)
    IsAvailable() bool
    Name() string
}
func NewSandbox(cfg *Config) Sandbox
func NewExecutor(cfg *Config) *Executor
func (e *Executor) Run(ctx context.Context, cmd string, opts ExecOptions) (*Result, error)
func (e *Executor) CheckPermission(cmd string) (Decision, string, error)
func ValidateWorkdir(workdir string) (string, error)
```

```go
func NewPermission(cfg *Config) *Permission
func (p *Permission) Check(cmdStr string) (Decision, error)
func (p *Permission) CheckWithReason(cmdStr string) (Decision, error)
func (p *Permission) Grant(pattern string)
type DeniedError struct {
    Command      string
    Reason       string
    ExplicitDeny bool
}
```

```go
type FormPrompter interface {
    EmitCustom(name, itemID string, payload any)
    WaitForForm(ctx context.Context, formID string) (coreactor.FormOutcome, error)
}
func NewCommandApprover(perm *Permission, overlayPath string) *CommandApprover
func (a *CommandApprover) Bind(p FormPrompter)
func (a *CommandApprover) Request(ctx context.Context, exec *Executor, command string, opts ExecOptions) (*Result, error)
func ProjectAllowPath(dataDir, workdir string) (string, error)
func AppendProjectAllow(path, command string) error
func ValidateGrantableCommand(cfg *Config, command string) error
```

```go
type FileSystem interface {
    Resolve(context.Context, string, FileAccessMode) (string, error)
    Stat(context.Context, string) (os.FileInfo, error)
    ReadFile(context.Context, string) ([]byte, error)
    ReadDir(context.Context, string) ([]os.DirEntry, error)
    WriteFile(context.Context, string, []byte) error
    Remove(context.Context, string) error
    Mkdir(context.Context, string) error
}
func NewBashTool(exec *Executor, workdir string, approver *CommandApprover) *tools.Tool
func NewFsTools(exec *Executor, workdir string) []*tools.Tool
func NewFsToolsWithFileSystem(fs FileSystem, workdir string) []*tools.Tool
func NewLocalFileSystem(exec *Executor, workdir string) FileSystem
func NewBackgroundTaskTools(tm *TaskManager, workdir string) []*tools.Tool
```

## Behavior invariants

- Permission order is deny first, then allow, else deny. Every command in a compound shell expression is checked; one denied child rejects the whole string. Parse failure denies.
- An explicit deny (`DeniedError.ExplicitDeny`, e.g. sudo/su) never prompts. Missing-allowlist denials may prompt; approval grants take effect and retry within the same tool call. Project grants persist at `<DataDir>/projects/<ProjectID(canonicalRoot)>/sandbox.json` as `{"version":1,"allow":[...]}`. Invalid ownership/mode/size/version fails loud.
- Approval uses the `questions` form variant, at most three rounds, and a one-minute wait. Timeout emits `form.cancelled` and denies. Unbound/headless mode returns an actionable `friday sandbox allow <command>` suggestion.
- Executor fails closed when configured isolation is unavailable. Tool timeouts must be positive and no greater than `coretools.MaxDeclaredToolTimeout`; timeout exit code is 124. Cancellation/timeout kills the whole process group.
- Each output stream is captured to at most 8 MiB, then rendered as the last 300 lines / 512 KiB with a truncation marker and flags.
- Sandboxed children inherit only PATH, TERM, TZ, LANG, HOME, LC_* plus explicit overrides; isolation-disabled mode inherits `os.Environ()`. `Sandbox.Enabled=false` disables only Seatbelt/bubblewrap. `IS_SANDBOX=1` trusts an outer sandbox and disables all Friday command, filesystem, and network policy layers.
- Both native backends compile the same command-start filesystem snapshot. Priority is deny > protected/readonly > workdir/write > default readonly. Relative paths use workdir, `~/` uses execution HOME, canonical targets deduplicate, dangling symlinks fail closed, and `filepath.Match` globs are non-recursive. Missing literals and zero-match globs produce no rule and do not protect future objects.
- Seatbelt is deny-by-default, restricts process operations to same-sandbox targets, and grants only the standard device matrix. Bubblewrap uses PID/IPC/network namespaces, drops all capabilities, creates a private `/dev`, and overlays nested readonly/protected and deny mounts after writable roots. `FRIDAY_SANDBOX_PROC_BIND` is an explicit degraded mode that expands `/proc` visibility.
- `Network.Isolation=false` allows host IP connect/bind/listen/accept; `true` blocks host/external IP traffic. Namespace-local bind behavior while isolated is not portable. `Network.Allow` applies only to image URL downloads, not arbitrary shell commands; Linux filesystem Unix sockets are not hidden merely by the network namespace.
- macOS has no PID/device namespace or reliable parent-death primitive. The Darwin wrapper starts an independent session and descendants inherit Seatbelt policy, but this is not equivalent to bubblewrap's `--die-with-parent`.
- Native `fs_*` paths are lexically cleaned, fully symlink-resolved (or resolved through the nearest existing ancestor), then checked dynamically against deny/protected/read-only/write roots. Agent-visible list/find/search paths preserve the requested logical project path; physical paths remain internal to safety checks and file access. Writes must be inside workdir or a write root. Deletion cannot target the workdir root or any policy root.
- `fs_find` uses up to 16 directory-reading workers and matches files/directories against slash-separated relative-path globs. It includes hidden/vendor entries, skips `.git`, symlinks, and special nodes, and deterministically returns the lexicographically first 1000 paths within the 512-KiB/shared output budget regardless of worker scheduling.
- `fs_search` uses one sorted walker + up to 16 workers; workers keep local results, then the collector sorts by `(path,line,column)` before applying the 1000-match/512-KiB limits, so output is deterministic.
- Remote images allow only HTTP(S); every redirect is rechecked. IP literals require explicit IP/CIDR permission; sensitive loopback/link-local/metadata/CGNAT addresses are blocked by default.
- Restored running background tasks become `interrupted`; stale PIDs are never reused for signals. Persistence keeps all running plus the 20 newest terminal tasks.

## Tests

Pure/unit coverage: parser, permission, config, approval, project overlay,
filesystem tools (including deterministic search), task store, generated image
handling, Seatbelt/Bwrap argument generation. Executor/bash/background-task
tests run real Bash subprocesses. `network_policy_test.go`'s
`TestDownloadImage*` cases bind `httptest` ports and fail in sandboxes that
disallow listen; skip only those environment-constrained cases there.
