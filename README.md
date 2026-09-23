# Friday

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-%3E%3D1.21-00ADD8?logo=go)](https://golang.org/)

**A Unix-philosophy AI Agent for your terminal.**

Text in, text out. Pipe-friendly. No GUI, no cloud dependency, no account required.

---

## Features

- **Pipeline-first design** — `cat log.txt | friday chat "summarize errors" | mail -s "Report" team@company.com`
- **Multiple input modes** — Arguments, stdin, or combine both
- **Project-aware configuration** — Use a local `.friday/` with HOME fallback
- **Session management** — Persistent conversations with history and memory
- **Multi-provider support** — OpenAI, Anthropic, Ollama, Google Gemini

---

## Installation

```bash
git clone https://github.com/basenana/friday.git
cd friday
make build
```

The binary will be placed in the `bin/` directory.

---

## Quick Start

### Initialize

```bash
friday init
```

This creates `.friday/` in the current directory. In a project it creates a
configuration plus empty `agents/`, `workspace/`, `workspace/skills/`, and
`workspace/mcp/` directories; missing workspace files, skills, and MCP
servers are inherited from `~/.friday/`. Running
the command from your HOME directory initializes the global workspace with all
default files.

Configuration lookup is `--config`, current-directory `.friday/config.json`,
current-directory `.friday/friday.yaml`, then the equivalent files under HOME.
Friday checks only the current directory, not its parents. JSON wins over YAML
within the same directory.

### 2. Configure

Create `~/.friday/config.json` (or `friday.yaml`):

```json
{
  "model": {
    "provider": "openai",
    "key": "$OPENAI_KEY",
    "model": "gpt-4o"
  },
  "data_dir": "~/.friday",
  "workspace": "~/.friday/workspace",
  "memory": {
    "enabled": true,
    "days": 7
  }
}
```

`model` is the preferred chat model. Optional `models` entries are the
remaining fallback endpoints, in order:

```yaml
model:
  provider: openai
  base_url: https://a.example/v1
  key: $A_KEY
  model: model-1
models:
  - provider: openai
    base_url: https://b.example/v1
    key: $B_KEY
    model: model-1
  - provider: anthropic
    key: $ANTHROPIC_KEY
    model: claude-sonnet
```

If `model` is omitted, is `null`, or does not contain a non-empty nested
`model` name, the first valid `models` entry becomes the primary model. The
same rule makes an unnamed `image_model` inactive.

Endpoints are deduplicated by provider, effective `base_url`, and model name.
The same model name may therefore be served by several providers or servers;
selecting it moves every matching endpoint to the front while preserving their
relative order. `image_model` remains the dedicated image-analysis model.

<details>
<summary>More provider examples</summary>

**Ollama (local)**

```json
{
  "model": {
    "provider": "openai",
    "base_url": "http://localhost:11434",
    "key": "",
    "model": "qwen2.5"
  }
}
```

**Anthropic**

```json
{
  "model": {
    "provider": "anthropic",
    "key": "$ANTHROPIC_KEY",
    "model": "claude-3-5-sonnet-20241022"
  }
}
```

**Google Gemini (OpenAI-compatible)**

```json
{
  "model": {
    "provider": "openai",
    "base_url": "https://generativelanguage.googleapis.com/v1beta",
    "key": "$GEMINI_KEY",
    "model": "gemini-2.0-flash"
  }
}
```

</details>

### MCP servers

Place one or more JSON files in `~/.friday/workspace/mcp/`. Project-specific
servers can be added under `.friday/workspace/mcp/`; a project declaration
with the same server name overrides the HOME declaration.

```json
{
  "mcpServers": {
    "chrome-devtools": {
      "command": "npx",
      "args": ["-y", "chrome-devtools-mcp@latest"]
    },
    "remote-search": {
      "url": "https://example.com/mcp",
      "headers": {"Authorization": "Bearer $MCP_TOKEN"},
      "includeTools": ["search"]
    },
    "legacy-events": {
      "type": "sse",
      "url": "https://example.com/sse"
    }
  }
}
```

`command` selects stdio, `url` selects Streamable HTTP, and explicit
`"type": "sse"` selects legacy SSE. Environment references in command
arguments, env values, URLs, and headers are expanded when connecting.
`excludeTools` is also supported and takes precedence over `includeTools`.

HOME servers are trusted automatically. Project servers remain blocked until
their exact configuration is approved with `friday mcp trust <server>` or
`/mcp trust <server>` in the TUI; changing the configuration invalidates that
approval. Use `friday mcp list`, `inspect`, `test`, `refresh`, `reconnect`, and
`untrust` to manage them. Tool schemas are cached under
`~/.friday/caches/mcp/`, so cached tools are available while Friday refreshes
connections in the background.

### Chat

```bash
friday chat "Write a Go HTTP server"
```

### Interactive TUI

```bash
friday tui
# Resume a specific session in a non-Git directory
friday tui --session <id>
```

In a Git repository, the TUI opens the main worktree and keeps every
non-removed worktree available as a tab. Use `/worktree [requirement]` to
create a linked worktree and `/select [name|branch]` to switch worktrees. In a
non-Git directory, the TUI keeps the session-based behavior and `/select`
chooses a session; `--session` is supported only in this mode.

The TUI restores transcripts, renders reasoning, tools, rich cards, and
interactive forms, and supports Codex-style follow-ups:

- `Enter` sends a prompt; while a task is running it steers the task immediately.
- `Tab` completes slash commands; while running it queues the prompt for the next turn.
- `Shift+Tab` toggles Default/Plan Mode while idle.
- `Ctrl+J` inserts a newline, `Ctrl+G` opens `$VISUAL`/`$EDITOR`, and `Ctrl+R` searches prompt history.
- `Esc` cancels the current task, while `Ctrl+C` exits.
- Type `/` for the command menu. Disk-defined Agents and installed Skills also appear there. Invoke an Agent with `/agent-name <task>` or a Skill with `/skill-name [task]`. Name precedence is built-in command, Agent, then Skill. Use `/open <card-id>` for a confirmed external artifact preview and `/show <tool-id>` for complete tool output.

#### TUI commands

| Area | Commands |
|------|----------|
| Session | `/clear`, `/resume [id\|name]`, `/rename <name>`, `/archive [id\|name]`, `/delete [id\|name]`, `/quit` |
| Worktree | `/worktree [requirement]`, `/select [name\|branch]`, `/vscode` |
| Collaboration | `/plan [task]`, `/plan off` |
| Model and context | `/model [name]`, `/effort [default\|none\|low\|medium\|high\|xhigh\|max]`, `/status`, `/context`, `/compact` |
| MCP | `/mcp`, `/mcp inspect <server>`, `/mcp trust <server>`, `/mcp untrust <server>`, `/mcp refresh [server]`, `/mcp reconnect <server>` |
| Working tree and output | `/diff`, `/copy [n]`, `/open <card-id>`, `/show <tool-call-id>` |
| Background tasks | `/tasks`, `/stop <task-id\|all>` |
| Help | `/help [command]` |

`/clear` is the single new-conversation command: it creates a session, clears
the transcript, and inherits the current runtime mode, model, and effort.
`/model` lists unique model names and changes fallback order without rebuilding
the running actor. `/effort default` explicitly requests the provider default
and emits no provider reasoning field. Choices are stored per session.
`/resume` lists active sessions only; archived sessions remain available
through the external `friday sessions` CLI.

Outside the TUI, use `friday worktrees list` to inspect project worktrees and
`friday worktrees remove <id>` to archive its session and remove its checkout.
The branch is retained unless `--delete-branch` is supplied.

#### Plan Mode

Plan Mode is a persistent collaboration mode on the current session. It
inspects the repository and resolves design decisions, but its read-only
behavior is prompt-enforced rather than a separate filesystem sandbox. The
model submits a versioned plan artifact only after the design is complete.

```text
/plan, Shift+Tab, or enter_plan_mode
                    |
                    v
 inspect context -> ask material questions -> submit_plan
                                              |
                              +---------------+----------------+
                              |                                |
                Approve + compact + implement           Request changes
                              |                                |
                 current session + Default Mode              Plan Mode
```

An approved plan remains attached to its session. Before each subsequent
model call, Friday prepends the accepted plan Markdown to the first user
message in the request. This request-only injection survives session reloads
and history compaction without duplicating the plan in persisted history.

The implementation is split across `core/collaboration` (mode instructions
and reasoning override), `core/planning` (artifacts and terminal submission),
`core/actor` (planning tools and events), `sessions` (runtime and artifact
persistence), `coder/commands` (command contracts), and `tui` (selectors,
handoff, rendering, and queued-command coordination).

The agent may enter Plan Mode itself when a task has material ambiguity or
needs design approval. While a turn is active, the TUI shows wall-clock
elapsed time and its current activity; every completed, cancelled, failed, or
plan-producing turn ends with a visible duration marker.

#### Project instruction files

The native `fs_read`, `fs_list`, `fs_find`, and `fs_search` tools lazily
discover project instructions. For directory-oriented operations, discovery
starts at the requested directory; for `fs_read`, it starts at the file's
containing directory. Friday then walks toward the project root, reads at most
one instruction file per directory (`AGENTS.md` first, then `CLAUDE.md`), and
returns newly discovered Markdown in the tool result's `fyi` field. Directories
already checked by the current session are not returned again, including after
history compaction or session reload. Forks inherit the parent snapshot and
then track their own directories independently.

Use `fs_find` to locate files or directories by a relative-path glob, for
example `{"pattern":"**/*.py"}`; `directory` defaults to `.`. It returns stable,
path-sorted results and stops after 1000 matches. Use `fs_search` instead to
search text-file contents.

Instruction discovery is limited to the native filesystem tools; shell,
background, and external MCP tools do not participate. Editing an instruction
file through a native filesystem tool invalidates that directory so its new
contents are discovered on the next read-only filesystem operation.

Plan Mode uses the session's selected model and defaults to `medium` reasoning
effort. Override it in JSON or YAML:

```yaml
collaboration:
  plan:
    reasoning_effort: high
```

The alternate screen defaults to `auto` (disabled under Zellij). Override it
with `tui.alternate_screen: always` or `never` in JSON/YAML configuration.
Rich transcript events are retained in a bounded recent window; older plain
conversation content is rebuilt from the session message history.

---

## Usage

### Basic

```bash
# Direct message
friday chat "Explain this error: connection refused"

# From file
friday chat < todolist.txt

# From stdin pipe
cat error.log | friday chat "What's the root cause?"

# Combine message with stdin
cat error.log | friday chat "Analyze this error log"
```

### Pipeline Composition

```bash
# Chain multiple friday calls
cat report.txt | friday chat "Summarize in 3 bullet points" | friday chat "Translate to Chinese"

# Integrate with other Unix tools
friday chat "Generate a random UUID" | xargs -I {} curl "https://api.example.com/{}"
```

### Sessions

```bash
# List sessions
friday sessions list

# Create new session
friday sessions new

# Switch session
friday sessions use <id>

# Show session history
friday sessions show <id>

# Archive old session
friday sessions archive <id>
```

### Heartbeat

Run periodic tasks defined in `HEARTBEAT.md`:

```bash
friday heartbeat
```

### Local daemon

Run Friday as a local daemon for UI and TUI clients:

```bash
friday daemon                 # ws://127.0.0.1:8999/ws
friday daemon --port 9000
```

The daemon binds only to `127.0.0.1`, has no authentication in v1, and exposes
only the `/ws` WebSocket endpoint. JSON frames use a Friday routing envelope;
`run` payloads and streamed actor events retain AG-UI semantics. A single
connection can subscribe to multiple persisted sessions.

### Tool execution limits

Foreground tool invocations have a 30-minute absolute wall-clock limit. Tools
that expose a `timeout` argument accept at most 15 minutes; larger values are
returned as `invalid_argument` instead of being silently truncated. Retries
and retry backoff consume the same invocation budget. Use `background_task`
for longer commands: starting, listing, waiting for, or stopping a task is
still bounded, while the background process itself may continue past 30
minutes.

### OS sandbox contract

Friday uses Seatbelt on macOS and bubblewrap on Linux. `sandbox.enabled`
controls only that OS isolation layer; command authorization, native `fs_*`
path checks, image URL protection, environment filtering, timeouts, and output
limits remain active independently.

Both native backends compile the same filesystem policy when each command
starts. The precedence is `deny` over `protected`/`readonly`, then the workdir
and explicit `write` roots, with everything else read-only. Relative paths are
rooted at the command workdir, `~/` uses the execution HOME, and symlinks are
resolved to their physical target. Dangling symlinks fail closed. Filesystem
patterns use Go `filepath.Match` semantics: `*` does not cross a directory
separator. Rules cover only objects that exist when the command starts;
missing literals and zero-match globs are ignored and do not reserve future
pathnames.

`sandbox.network.isolation: false` retains the host IP network, including
connect, bind, listen, and accept. `true` blocks access to host and external IP
networks. Linux still has namespace-local networking, while Seatbelt has no
network namespace, so local bind behavior under isolation is not a portable
contract. `sandbox.network.allow` is only the allow policy for remote image
URLs; it is not a domain firewall for shell commands. A Linux network namespace
does not by itself hide filesystem-reachable Unix sockets.

Seatbelt is deny-by-default and limits process inspection/signalling to the
same sandbox plus a small device and system-service set. macOS has no PID or
device namespace and no reliable equivalent of bubblewrap's parent-death
behavior; Friday uses an independent process session and inherited Seatbelt
policy but cannot promise immediate orphan removal if Friday itself is killed.
Bubblewrap uses private PID/IPC/network namespaces, drops all capabilities,
creates a private `/dev`, starts a new session, and dies with its parent.
Setting `FRIDAY_SANDBOX_PROC_BIND` on Linux is an explicit degraded mode that
binds the host `/proc` instead of mounting a private procfs and expands process
visibility.

`IS_SANDBOX=1` is stronger than `sandbox.enabled: false`: it trusts an outer
sandbox and disables Friday's command, filesystem, and network policy layers to
avoid nesting. Set it only when the outer environment supplies the complete
security boundary.

### Sandbox command authorization

Commands executed by the `bash` tool must be in the sandbox allow list
(`sandbox.permissions.allow` in the configuration). When an interactive
session runs a command that is denied only because it is missing from the
allow list — and not matched by a deny rule — Friday automatically shows an
approval form with three choices:

- **Allow for this project (recommended)** — the command is persisted to the
  project's grant file and never asked for again in this project.
- **Allow just this once** — the command runs for the current session only.
- **Deny** — the command stays blocked.

After approval the command is retried immediately inside the same tool call,
so the agent receives the real command output without an extra round trip.
An approval form waits at most one minute; an unanswered form denies the
command by default so a prompt nobody sees cannot stall the agent. Commands
matched by deny rules (for example `sudo` and `su`) never prompt
and can never be granted.

Project grants are stored on the HOME side, in
`~/.friday/projects/<project>/sandbox.json`, where `<project>` is derived
from the canonical (symlink-resolved) project root — opening the same
repository through different paths or symlinks resolves to one shared grant
file. The file contains only an allow list:

```json
{"version": 1, "allow": ["gofmt", "staticcheck"]}
```

Grants are merged into the session allow list at startup and take effect
immediately when approved. Deny rules always come from the base
configuration; the grant file cannot add or remove them. The file
deliberately lives outside the repository: the agent has write access to
the project directory, so storing grants inside it would let the agent
escalate its own permissions. Friday validates the file's ownership,
permissions, size, and version, and refuses to load files that fail those
checks.

Headless runs (for example `friday chat`, heartbeat, or CI) have no approval
form; denied commands fail with an actionable error instead. Persist a
project grant ahead of time from the project directory:

```bash
friday sandbox allow gofmt
```

## Data Structure

```
~/.friday/
├── config.json          # Configuration (or friday.yaml)
├── sessions/            # Conversation history
├── memory/              # Daily memory logs
│   └── 2024-01-15.md
├── caches/              # Namespaced reusable caches
│   └── mcp/             # Cached MCP tool schemas
├── log/                 # Application logs
├── agents/              # Named Agent definitions
│   └── reviewer/
│       └── AGENT-SPEC.md
└── workspace/           # Agent context files
    ├── SOUL.md          # Persona and tone
    ├── ENVIRONMENT.md   # Machine and execution environment
    ├── AGENTS.md        # Behavior guidelines
    ├── IDENTITY.md      # Agent name and style
    ├── TOOLS.md         # Tool usage guidance
    ├── HEARTBEAT.md     # Periodic checklist
    ├── MEMORY.md        # Long-term memory
    ├── skills/          # Installed skills
    └── mcp/             # MCP server JSON configurations

<project>/.friday/
├── config.json          # Independent project configuration
├── agents/              # Project Agents; same-name Agents override HOME
└── workspace/           # Project overrides for HOME workspace files
    ├── skills/          # Project skills; same-name skills override HOME
    └── mcp/             # Project MCP configs; explicit trust required
```

Workspace files are resolved one by one: a project file overrides the
same-named HOME file, while a missing project file falls back to HOME. Skills
and MCP server definitions are merged similarly. In project scope, skill
installation and deletion only modify project skills; inherited HOME skills
cannot be deleted there.
If a project explicitly points `workspace` at the HOME workspace, that shared
layer remains readable but is treated as HOME and read-only from the project;
set `workspace` to `workspace` to create project-local overrides.

Sessions, daily memory, state, and projects remain under the
configured data directory (`~/.friday` by default), even when a project config
is active.

**Portability**: Copy `~/.friday/` to another machine to transfer the global
agent data, and copy a project's `.friday/` with the project for its overrides.

### Agent definitions

Each named Agent is defined by `agents/<agent-name>/AGENT-SPEC.md`. The file
uses YAML frontmatter for metadata and the Markdown body as the Agent-specific
system prompt:

```markdown
---
name: reviewer
description: Reviews changes for correctness and maintainability
model: claude-sonnet
effort: high
max_loop_times: 100
---
You are a focused code reviewer. Inspect the requested changes and report
concrete findings with file references.
```

`name` defaults to the directory name, `description` defaults to the name, and
`max_loop_times` defaults to `100`. Optional `model` is an exact configured
model name and optional `effort` is one of `default`, `none`, `low`, `medium`,
`high`, `xhigh`, or `max`. Unknown frontmatter fields are ignored with
a warning so definitions created by newer Friday versions remain usable after
a downgrade. Invalid description or loop-count values fall back to their
defaults; malformed YAML, invalid model/effort values, unsafe or mismatched
names, and an empty Markdown body stop startup.

HOME Agents are loaded before project Agents, so a project definition with the
same name overrides its HOME definition. Every loaded Agent is available both
through `run_task` and as `/agent-name <task>` in the TUI. Slash invocation
applies only to that one root-session turn; the following turn returns to the
primary Agent.

An Agent's stable system prompt is the composed workspace prompt (`AGENTS.md`,
`SOUL.md`, and `IDENTITY.md`) followed by a blank line and the Markdown body of
`AGENT-SPEC.md`. Request hooks continue to add Skills, Memory/history, project
instructions, MCP, and subagent/collaboration context. Agent client views share
the Session's configured model pool and rate limiters. In Default Mode the
runtime priority is Session override, then Agent frontmatter, then model
configuration. In Plan Mode, `collaboration.plan.reasoning_effort` supplies the
default when the normally selected Session, Agent, or model effort is empty or
`default`; an explicit non-default effort continues to take priority.

---

## How It Works

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   stdin     │────▶│   Friday    │────▶│   stdout    │
│  / args     │     │    Agent    │     │             │
└─────────────┘     └──────┬──────┘     └─────────────┘
                           │
                    ┌──────┴──────┐
                    │             │
              ┌─────▼─────┐ ┌─────▼─────┐
              │ Workspace │ │  Memory   │
              │  Context  │ │  System   │
              └───────────┘ └───────────┘
```

1. **Input**: Message from arguments, stdin, or both
2. **Context**: Loads workspace files (SOUL.md, ENVIRONMENT.md, etc.) into system prompt
3. **Memory**: Prepends recent memory logs to conversation history
4. **Agent**: Executes ReAct-style reasoning with tool support
5. **Output**: Streams response to stdout

---

## Philosophy

Friday follows the Unix philosophy:

- **Do one thing well** — AI assistance for terminal workflows
- **Text streams** — Everything is text, composable with pipes
- **Local first** — Your data stays on your machine
- **Simple configuration** — One JSON file, one directory

---

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

---

## License

[Apache License 2.0](LICENSE)
