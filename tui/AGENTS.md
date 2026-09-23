# tui — Bubble Tea terminal interface for Friday sessions

This package is the interactive terminal frontend. It projects actor events into chat state, renders tools/cards/forms/TODOs, handles slash commands and editors, and supports both ordinary and project-aware sessions.

## Entry points

```go
func Run(sessMgr *sessions.Manager, cfg *config.Config, sessionID string) error
func RunProject(projectMgr *projectpkg.Manager, cfg *config.Config, sessionID string) error
func RunWorktree(sessMgr *sessions.Manager, cfg *config.Config, cwd string) error
```

`RunWorktree` owns one retained runtime per worktree, initially activates the
main checkout, and keeps background runtimes alive while the foreground tab
changes. `RunProject` preserves the session-oriented non-Git mode. Keep setup
differences behind these entry points rather than exporting internal model
types.

## Files

- `tui.go` creates the Bubble Tea v2 program and owns the main model/update lifecycle.
- `view.go` renders the main layout, transcript, status, and overlays.
- `input.go` handles key bindings, input state, submission, and command completion.
- `messages.go` defines internal Tea messages.
- `projection.go` projects actor/bus events into display state.
- `cards.go` renders persistent actor cards and custom UI content.
- `tool_cards.go` renders tool calls, results, and collapsible details.
- `form.go` renders and submits interactive forms.
- `todo_view.go` renders the current planning TODO list.
- `command_actions.go` implements TUI-side slash-command actions.
- `worktree.go`, `worktree_runtime.go`, and `worktree_tabs.go` own Git worktree startup, retained runtimes, navigation, and tab state.
- `review.go` opens the active checkout in VS Code or renders remote-opening instructions.
- `detail.go` manages detail panes.
- `editor.go` launches and receives external-editor input.
- `clipboard.go` implements platform-aware copy behavior.
- `logging.go` redirects logs away from the alternate screen.

## Event flow

- The TUI consumes `bus.Feed`, which preserves cross-topic serial order.
- Feed events are batched/throttled before rendering to avoid one terminal repaint per token.
- Always observe `Feed.Done`; the event channel itself is intentionally not closed.
- Projection code must be deterministic and side-effect free enough for replay tests.
- Actor event IDs and sequence numbers drive updates and deduplication. Do not key persistent UI state only by display text.
- Terminal events and actor execution run concurrently; all model mutation stays in the Bubble Tea update loop.

## Forms and commands

- Interactive question forms use variant `questions`, one-question navigation, Up/Down selection, and Enter submission.
- Legacy `plan_questions` remains supported where emitted by existing planning flows.
- Other form variants use the generic form renderer and its own navigation/submission behavior.
- Preserve field IDs and option values when changing labels; submissions are contracts with actor tools.
- Slash commands are parsed by `coder/commands` where applicable, while UI-only actions remain here.
- `/worktree [requirement]` creates a linked worktree only in Git mode. `/select` switches worktrees in Git mode and sessions in non-Git mode; both navigation actions bypass a running actor's input queue.

## Terminal constraints

- Do not print directly to stdout or stderr while the alternate-screen program runs; use the configured log destination or Tea messages.
- Width calculations must use display cells, not byte or rune counts.
- Rendering should degrade safely at narrow widths and must not emit invalid control sequences.
- External editor and clipboard operations must report failures in the UI instead of corrupting terminal state.
- Any blocking I/O belongs in a Tea command, never directly in `Update` or `View`.

## Tests

Run:

```bash
go test ./tui
```

The suite includes projection, batching, wrapping, forms, tools, commands, logging, and benchmarks. `TestDiff*` cases invoke a Git subprocess and can fail in restricted sandboxes that deny Git access to `/dev/null`; run those on a normal host. Use `go test -run '^TestName$' ./tui` for focused UI contracts.
