# cmd — Cobra CLI entry points for the friday binary

This package implements the `friday` executable: the root Cobra command tree,
persistent config/session bootstrap, and every user-facing subcommand (chat,
sessions, TUI, daemon, heartbeat, sunrise, init, skills, mcp, sandbox). All
commands share state initialized in `root.go`'s `PersistentPreRunE`.

## Files

| File | Responsibility |
|---|---|
| `main.go` | `main()`: init file-backed logging (`config.LogPath()`), detect external sandbox (`IS_SANDBOX=1` logs a warning that all guards are disabled), set `corelogger.SetRoot`, run `rootCmd.Execute()` |
| `root.go` | `rootCmd` + `PersistentPreRunE`: load config (`config.LoadForDir`), resolve `-w/--workspace` override, derive TTY (via `tty` subprocess or `FRIDAY_TTY` env), build `sessions.Manager`; `PersistentPostRun` syncs/closes loggers |
| `chat.go` | `friday chat [message]`: combine args + piped stdin, mutual-exclusion flag validation (`--session` vs `--isolate`/`--temporary`), `--image` ref normalization (URL passthrough; local file → abs path + MIME probe for jpeg/png/gif/webp), stream via `setup.PrintResponse` |
| `session.go` | `friday sessions` subtree: `list`, `new`, `current`, `use`, `show`, `alias`, `archive`, `unarchive`, `archived`, `delete`, `compact` (ID or alias resolution) |
| `tui.go` | `friday tui`: Git directories launch the main worktree through `tui.RunWorktree`; non-Git directories retain `tui.RunProject` and `--session` |
| `project.go` | Opens the logical project shared by all Git worktrees and migrates legacy worktree metadata |
| `worktrees.go` | `friday worktrees list/remove`: inspect or remove project worktrees; removal archives the associated session and optionally deletes the branch |
| `daemon.go` | `friday daemon`: session catalog + actor registry (`AgentPlanEntry=true`, `ConfigTools=true`, `Catalog=sessMgr`), runs `fridaydaemon.Server` on `--port` (default 8999), graceful shutdown on SIGINT/SIGTERM with 10s timeout |
| `heartbeat.go` | `friday heartbeat`: reads workspace `HEARTBEAT.md`, exits if empty, else sends to current session's chat |
| `sunrise.go` | `friday sunrise`: daily bootstrap — lists active sessions, processes pre-today sessions into memory (`memory.Processor.ProcessSession` on a `setup.WithTemporary(true)` agent), creates a fresh isolated session as current |
| `init.go` | `friday init`: creates `.friday/` + seed `config.json` + `agents/` if absent; project init creates workspace + `skills/` + `mcp/` dirs (HOME files inherited when missing); HOME init writes full template workspace (`workspace.InitWithParams`) |
| `skills.go` | `friday skills` subtree: `list`, `delete <name>` (writable-layer only via `ensureSkillDeletionAllowed`), `install --url/--file` (zip/tar.gz, size-limited HTTP download, temp-dir extraction) |
| `mcp.go` | `friday mcp` subtree: `list`, `inspect`, `test`, `refresh`, `reconnect`, `trust`, `untrust`; builds a Manager from workspace MCP roots + cache + trust file; 30s timeouts (except untrust) |
| `sandbox.go` | `friday sandbox allow <command>`: `sandbox.ValidateGrantableCommand` (rejects deny-listed commands like sudo), then `sandbox.AppendProjectAllow` — the headless counterpart of the interactive approval form |

## Key wiring (root.go)

```go
var (
    cfgFile      string
    workspaceDir string
    cfg          *config.Config
    sessMgr      *sessions.Manager
    currentTTY   string
)

func getTTYName() string
func currentFilePath(cfg *config.Config) string
func configuredWorkspace(c *config.Config) *workspace.Workspace
```

Chat flags: `--session/-s`, `--isolate/-i`, `--temporary/-t`, `--verbose/-v`, `--image`. Root persistent flags: `--config/-c`, `--workspace/-w`.

## Behavior invariants

- `PersistentPreRunE` runs for every command except `init` (skipped so `friday init` bootstraps without existing config).
- Config discovery follows `config.LoadForDir`: explicit path → `cwd/.friday` → HOME; parent directories are never searched.
- TTY isolation: the "current session" pointer file is keyed per TTY (`current_<tty>` with `/` → `_`), so session switching in one terminal does not affect another. `FRIDAY_TTY` env wins; non-TTY stdin (pipes) falls back to the shared `current` file.
- `chat.go` arg/stdin combination: args only → args; stdin only → stdin; both → `args + "\n\n" + stdin`; neither → usage error.
- `--image` local files are validated (MIME allowlist) before being sent to the agent; URLs pass through unchanged; `~/` is expanded.
- `skills delete` deliberately fails for skills installed outside the writable workspace layer (e.g. HOME-installed skills while running in a project).
- `friday tui` classifies the current directory before launch: Git metadata selects the worktree runtime and the main checkout; non-Git paths retain the session runtime. Git mode rejects `--session` and uses `/select` for worktree navigation.
- `main.go` prints command errors to stderr and logs via the file logger; when `IS_SANDBOX=1` (friday itself is sandboxed) it logs that all sandbox guards are disabled.
- `sunrise` compares dates on local-timezone `time.Now().Truncate(24h)`; no timezone normalization.

## Tests

- `chat_test.go` — message combination logic, `normalizeImageRef` (pure functions).
- `daemon_test.go` — default port 8999; `daemon` command registered.
- `init_test.go` — minimal project workspace creation; HOME full-template creation.
- `main_test.go` — external-sandbox warning goes to the file logger exactly once per process, never stdout.
- `sandbox_test.go` — allow persists project grants; deny-listed commands rejected.
- `skills_test.go` — deletion layer guard.
- `sunrise_test.go` — `filterOldSessions`.

No cmd test requires network, ports, or git (`go test ./cmd/...` runs everywhere).
