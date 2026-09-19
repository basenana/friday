# config — CLI configuration loading, model catalog, and path resolution

Loads the effective friday config from JSON or YAML (explicit path → project
`.friday` → HOME), applies env expansion and validation, resolves every data
path, and exposes an ordered catalog of chat/image models for the fallback
client. Consumed by `cmd` (root bootstrap), `setup`, and every entry point
that needs model or path information.

## Files

| File | Responsibility |
|---|---|
| `config.go` | `Load`/`LoadForDir` discovery + decoding, env expansion, relative-path resolution, validation, defaults; project sandbox allowlist merge; path getters; `WriteConfig`/`WriteDefaultConfig`; `LogPath` |
| `types.go` | `Config` struct + nested types (`ModelConfig`, `MemoryConfig`, `SessionConfig`, `LogConfig`, `TUIConfig`, `CollaborationConfig`); `DefaultConfig`, `applyRuntimeDefaults` |
| `model.go` | Model catalog: `ModelIdentity`, `CanonicalProvider`, `EffectiveBaseURL`, `ChatModels` (ordered, deduped), `PreferModel`, `PrimaryModel`, `ResolveImageModel`, optional-model normalization |

## Key API (verbatim signatures)

```go
func Load(configPath string) (*Config, error)
func LoadForDir(explicitPath, cwd string) (*Config, error)
func (c *Config) ResolvePath(path string) string
func (c *Config) DataDirPath() string
func (c *Config) WorkspacePath() string
func (c *Config) WorkspaceFallbackPaths() []string
func (c *Config) AgentPaths() []string
func (c *Config) ProjectScoped() bool
func (c *Config) ConfigPath() string
func (c *Config) SessionsPath() string
func (c *Config) ProjectsPath() string
func (c *Config) MemoryPath() string
func (c *Config) StatePath() string
func (c *Config) CachesPath() string
func LogPath() string
func WriteDefaultConfig(path string) (bool, error)
func WriteConfig(path string, cfg *Config) (bool, error)
```

```go
// model.go
type ModelIdentity struct { Provider, Server, Model string }
func CanonicalProvider(provider string) string
func (m ModelConfig) EffectiveBaseURL() string
func (m ModelConfig) Identity() ModelIdentity
func (c *Config) ChatModels() []ModelConfig
func (c *Config) ModelNames() []string
func (c *Config) HasModelName(name string) bool
func (c *Config) PreferModel(name string) []ModelConfig
func (c *Config) PrimaryModel() ModelConfig
func (m ModelConfig) IsConfigured() bool
func (m ModelConfig) HasInput(kind string) bool
func (c *Config) ResolveImageModel(modelOverride string) (ModelConfig, error)
```

## Behavior invariants

- Discovery precedence: explicit path > `cwd/.friday/config.json|friday.yaml` > HOME `~/.friday/…` > built-in defaults. Only the current directory is checked — parents are never searched (`TestLoadForDirDoesNotSearchParents`).
- Format is chosen strictly by `.json` suffix; everything else parses as YAML.
- Env expansion (`os.Expand` semantics) is applied to Key/BaseURL/Input/Model/Proxy and DataDir/Workspace **before** path resolution; names that expand to an unset env var become unconfigured in `normalizeOptionalModels`.
- A missing model name means an unconfigured model: `loadFile` clears default model names before decode, and `normalizeOptionalModels` drops unnamed entries.
- Catalog order: `model` (first choice) then `models`; exact provider/server/model duplicates are dropped keeping the first; the same model name on different servers stays as fallback candidates.
- Project-scoped configs: `Workspace` defaults to `workspace` relative to the project config dir; workspace files fall back to the HOME workspace file-by-file (`WorkspaceFallbackPaths`); agent paths become HOME agents + project agents (project overrides HOME by name).
- Project allowlist: `applyProjectSandboxAllow` merges `<DataDir>/projects/<ProjectID(cwd)>/sandbox.json` grants into the sandbox allow list. The file lives on the HOME side so a sandboxed agent cannot escalate itself; deny rules are unaffected; an invalid file fails loud (never silently ignored). Skipped when the process itself is sandboxed (`IS_SANDBOX=1` exact value).
- Validation: `tui.alternate_screen` ∈ {auto, always, never}; reasoning effort validated against `providers.IsValidReasoningEffort` (default/none/low/medium/high/xhigh/max); plan-mode effort defaults to medium.
- Provider normalization: `""`/`openai` → openai; `openai-response`/`openai-responses` → openai-responses; `anthropic` → anthropic; missing base_url defaults to `https://api.openai.com/v1` / `https://api.anthropic.com`.
- `WriteConfig` is create-or-noop (never overwrites); `LogPath` is always `/tmp/friday-<date>.log`.
- macOS gotcha covered by test: project allowlist follows symlinked CWD (`TestLoadForDirProjectAllowFollowsSymlinkedCWD`).

## Tests

- `config_test.go` — LoadForDir precedence, empty project falls back to HOME, relative paths, no parent search, corrupt project config fails loud, sandboxed-process disables project allow, allowlist merge/invalid/symlink behavior.
- `model_test.go` — `HasInput`, required model name, endpoint dedup vs same-name-different-server, nil primary model, optional models, `PreferModel` stable partition, alternate_screen validation, plan effort default+validation, `ResolveImageModel` selection paths, env expansion in image model.

All tests are filesystem/pure-function; no network, ports, or git required.
