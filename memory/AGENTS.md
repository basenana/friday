# memory — Daily memory logs, long-term memory, and session-to-memory processing

Manages friday's memory files: daily logs (`memory/YYYY-MM-DD.md`), the
curated long-term `MEMORY.md`, workspace ENVIRONMENT.md access, and an
Ebbinghaus-style forgetting model. `Processor` sends a session transcript to
an agent to distill memories (used by `cmd/sunrise.go`). `MemoryFiles` is
consumed by `setup`'s memory hook path.

## Files

| File | Responsibility |
|---|---|
| `types.go` | `Memory` record + `SessionHistory` structs |
| `memory.go` | `MemorySystem`: ensure/create today's daily log, load recent logs, substring search of MEMORY.md, append daily/curated entries |
| `files.go` | `MemoryFiles`: direct file CRUD for daily memories, MEMORY.md, and workspace ENVIRONMENT.md |
| `forgetting.go` | `ForgettingSystem`: time-decay × frequency-boost memory-strength evaluation |
| `processor.go` | `Processor`: sends a formatted session transcript to an agent to distill memories; `FormatConversation` |
| `prompts.go` | The 5-step session-processing prompt template + `buildPrompt` placeholder substitution |

## Key API (verbatim signatures)

```go
func NewMemorySystem(basePath string, days int) *MemorySystem
func (m *MemorySystem) EnsureTodayMemory() error
func (m *MemorySystem) LoadRecentLogs() ([]string, error)
func (m *MemorySystem) Search(query string) ([]string, error)
func (m *MemorySystem) Write(content string, memType MemoryType) error

func NewMemoryFiles(memoryPath, workspacePath string) *MemoryFiles
func (f *MemoryFiles) ListRecentDailyMemories(days int) ([]string, error)
func (f *MemoryFiles) AppendLongTermMemory(content string) error
func (f *MemoryFiles) ReadEnvironment() (string, error)

type ForgettingSystem struct {
    HalfLifeDays      float64
    FrequencyWeight   float64
    DeletionThreshold float64
    MaxUsageCount     float64
}
func (fs *ForgettingSystem) Evaluate(record *Memory) EvaluationResult
func DefaultCheckMemoryNeedToForget() func(memory *Memory) bool

type Agent interface {
    Chat(ctx context.Context, message string) *api.Response
}
func NewProcessor(agent Agent, config ProcessorConfig) *Processor
func (p *Processor) ProcessSession(ctx context.Context, history *SessionHistory) (string, error)
func FormatConversation(messages []types.Message) string
```

`MemoryTypeDaily = "daily"`, `MemoryTypeCurated = "curated"`.

## Behavior invariants

- Daily writes append `## <RFC3339 timestamp>\n\n<content>\n\n` to `memory/YYYY-MM-DD.md` (created with a `# YYYY-MM-DD` header if missing); curated writes append `## <YYYY-MM-DD>` sections to `MEMORY.md`.
- Recent-log listing excludes MEMORY.md, only parses `2006-01-02`-named `.md` files, and uses integer day diff `[0, days)`; `ListRecentDailyMemories` sorts newest-first while `MemorySystem.LoadRecentLogs` does **not** sort (relies on ReadDir order).
- `MemoryFiles` daily/long-term `Write*` methods are full-file overwrites while `Append*` methods append — easy to confuse.
- `Search` is a case-insensitive substring match over the whole MEMORY.md only (returns the whole file or empty).
- Forgetting model: `timeDecay = e^(-days/HalfLife)`; `freqBoost = log(1+usage)/log(1+MaxUsageCount)` clamped [0,1]; `strength = timeDecay * (1 + FrequencyWeight*freqBoost)` clamped [0,1]; `Forget = strength < DeletionThreshold`. Defaults: half-life 30d, weight 0.6, threshold 0.1, max usage 100. Guards: HalfLife ≤ 0 → decay 1.0; MaxUsage ≤ 0 or usage ≤ 0 → boost 0.
- The session-processing prompt instructs the agent to update daily memory, review the last 3–5 days, update/prune MEMORY.md, sync ENVIRONMENT.md, then archive the session; placeholders are substituted via `strings.ReplaceAll` (not text/template).
- `FormatConversation` maps roles: `USER:` / `ASSISTANT [thinking]:` (Reasoning) / `ASSISTANT:` / `ASSISTANT TOOL CALL: name(args)` / `TOOL RESULT:`; every message followed by a blank line.

## Tests

Only `processor_test.go` — `TestFormatConversation` (8 table cases). Note:
`forgetting.go`, `memory.go`, and `files.go` currently have no tests. No
environment-constrained tests in this package.
