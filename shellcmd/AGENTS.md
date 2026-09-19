# shellcmd — Safe Bash argument quoting without sandbox import cycles

This tiny package builds auditable shell command strings with Bash-safe
argument quoting. It was extracted from `sandbox` so packages such as TUI code
can construct commands without importing the sandbox package and creating an
import cycle. Current consumers include `sandbox/bwrap.go`,
`sandbox/seatbelt.go`, and `tui/editor.go`.

## Files

| File | Responsibility |
|---|---|
| `quote.go` | Bash argument quoting (`QuoteArg`) and command + argument joining (`Join`) |

## API

```go
func QuoteArg(arg string) string
func Join(command string, args ...string) string
```

## Behavior invariants

- Quoting uses `mvdan.cc/sh/v3/syntax` with `syntax.LangBash`; callers must not concatenate unquoted user-controlled arguments themselves.
- If the syntax printer unexpectedly fails, `QuoteArg` falls back to POSIX-style single quoting and replaces each embedded `'` with `'\''`.
- `Join` quotes every argument but leaves the command name as the first token; pass a trusted executable name as `command`.
- The returned value is a shell command string, not an argv slice. Prefer direct `exec.Command` argv when a shell is not required; use this package where a backend intentionally emits Bash text.

## Tests

`quote_test.go` runs a real `bash -c` command with shell metacharacters
(including command substitution and mixed quotes) and verifies that every
argument arrives literally. It requires Bash but no network, port, or git.
