# Friday

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-%3E%3D1.21-00ADD8?logo=go)](https://golang.org/)

**A Unix-philosophy AI Agent for your terminal.**

Text in, text out. Pipe-friendly. No GUI, no cloud dependency, no account required.

---

## Features

- **Pipeline-first design** — `cat log.txt | friday chat "summarize errors" | mail -s "Report" team@company.com`
- **Multiple input modes** — Arguments, stdin, or combine both
- **Local data** — Everything stored in `~/.friday/`, portable and private
- **Session management** — Persistent conversations with history and memory
- **Multi-provider support** — OpenAI, Anthropic, Ollama, Google Gemini

---

## Installation

```bash
go install github.com/basenana/friday@latest
```

---

## Quick Start

### Initialize

```bash
friday init
```

This creates `~/.friday/` with default configuration and workspace files.

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

### Chat

```bash
friday chat "Write a Go HTTP server"
```

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

---

## Data Structure

```
~/.friday/
├── config.json          # Configuration (or friday.yaml)
├── sessions/            # Conversation history
├── memory/              # Daily memory logs
│   └── 2024-01-15.md
├── log/                 # Application logs
└── workspace/           # Agent context files
    ├── SOUL.md          # Persona and tone
    ├── USER.md          # User preferences
    ├── AGENTS.md        # Behavior guidelines
    ├── IDENTITY.md      # Agent name and style
    ├── TOOLS.md         # Tool usage guidance
    ├── HEARTBEAT.md     # Periodic checklist
    └── MEMORY.md        # Long-term memory
```

**Portability**: Copy `~/.friday/` to another machine to transfer your agent.

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
2. **Context**: Loads workspace files (SOUL.md, USER.md, etc.) into system prompt
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

