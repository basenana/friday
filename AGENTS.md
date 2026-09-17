# Friday repository guidance

## Logging and terminal output

- Use `github.com/basenana/friday/core/logger` for runtime diagnostics. Create a named logger with `logger.New("component")`; do not use the standard-library `log` or `log/slog` packages.
- Code reachable from the TUI must never write logs or incidental diagnostics directly to stdout or stderr. This includes `fmt.Print*`, `fmt.Fprint*(os.Stdout/os.Stderr)`, built-in `print`/`println`, and libraries or child processes that inherit terminal output.
- Present user-visible TUI messages through Bubble Tea messages/model state. Return errors to the TUI update loop instead of printing them.
- Capture stdout and stderr from non-interactive child processes. Use `tea.ExecProcess` only for an intentional interactive handoff where Bubble Tea suspends and restores the terminal.
- Explicit stdout/stderr output is allowed for non-TUI CLI commands in `cmd/` when it is the command's documented user-facing output, not diagnostic logging.
- The application logger is file-backed. If its file cannot be opened, keep logging silent rather than falling back to a terminal stream.

When changing logging or TUI-reachable execution paths, run `go test ./core/logger ./core/actor ./core/providers/fallback ./tui` and verify that fallback/error paths produce no terminal output.
