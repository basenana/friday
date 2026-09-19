# skills — SKILL.md loading, registry, toolset, and hook injection

Loads markdown skills with YAML frontmatter from layered directories
(HOME→project), exposes them to agents as progressive-disclosure tools
(`load_skill`, `list_skill_files`, `read_skill_file`) plus a sorted catalog in
the system prompt, with hot reload. Wired by `setup` via
`skills.NewLoader(ws.SkillsPaths()...) → NewRegistry → NewHook`.

## Files

| File | Responsibility |
|---|---|
| `skill.go` | `Frontmatter`, `Skill`, `Resource` types |
| `loader.go` | `Loader`: multi-directory loading, SKILL.md parsing, sandboxed resource access, deletion |
| `registry.go` | `Registry`: thread-safe cached snapshots + hot reload via file stamps |
| `provider.go` | `Provider` and `Catalog` interfaces |
| `toolset.go` | The three agent-facing tools (`load_skill`, `list_skill_files`, `read_skill_file`) |
| `hook.go` | Session hooks injecting the tools and the skill system prompt |
| `prompts.go` | `SKILL_SYSTEM_PROMPT` template + XML-escaped catalog builder |

## Key API (verbatim signatures)

```go
type Frontmatter struct {
    Name         string         `yaml:"name"`
    Description  string         `yaml:"description"`
    AllowedTools string         `yaml:"allowed_tools,omitempty"`
    Metadata     map[string]any `yaml:",inline"`
}
type Skill struct {
    Name         string
    Description  string
    Frontmatter  *Frontmatter
    Instructions string
    BasePath     string
}
func NewLoader(skillsPaths ...string) *Loader
func (l *Loader) Load() error
func ParseSkillFile(content []byte) (*Frontmatter, string, error)
func (l *Loader) Get(name string) (*Skill, error)
func (l *Loader) LoadSkillFromDir(dirName string) (*Skill, error)
func (l *Loader) ListFiles(skillName, subPath string) ([]fs.DirEntry, error)
func (l *Loader) ReadFile(skillName, filePath string) ([]byte, error)
func (l *Loader) LoadResource(skillName, resourcePath string) ([]byte, error)
func (l *Loader) ListResources(skillName string) ([]*Resource, error)
func (l *Loader) Delete(skillName string) error
func NewRegistry(provider Provider) *Registry
func NewSkillTools(provider Provider) []*tools.Tool
func NewHook(catalog Catalog) *Hook

type Provider interface {
    List() []*Skill
    Get(name string) (*Skill, error)
    LoadResource(skillName, resourcePath string) ([]byte, error)
    ListResources(skillName string) ([]*Resource, error)
    ListFiles(skillName, subPath string) ([]fs.DirEntry, error)
    ReadFile(skillName, filePath string) ([]byte, error)
}
```

## Behavior invariants

- Precedence: directories load in order and **later directories override earlier ones** (`workspace.SkillsPaths()` documents low→high priority); `LoadSkillFromDir` searches back-to-front for a match.
- Hot reload: `Registry` re-checks a `skillFilesStamp` (sha256 over sorted path+mtime+size of every SKILL.md) rate-limited to 500ms — the TUI slash menu calls `List()` on every keystroke. If any skill definition is invalid, the last valid snapshot is kept.
- Security: `resolveWithinSkill` runs `filepath.EvalSymlinks` on both base and target paths (a lexical prefix check is insufficient — symlinks installed later can point anywhere), rejects `..` traversal, symlink escapes, and dangling links; `ListFiles` skips symlinked entries. The absolute `BasePath` is computed once at load.
- Frontmatter: unknown YAML keys are captured inline into `Metadata` and logged as warnings (typos are not silently ignored); a missing `name` is an error. Warnings go through the friday logger, never stderr.
- `allowed_tools` is **informational only**: the field is surfaced in `load_skill` results but no toolset gating on it exists anywhere in the repository.
- Hook behavior: `BeforeAgent` appends the 3 tools; `BeforeModel` appends a name-sorted skill catalog (name + description + catalog path). Full instructions stay lazy-loaded via `load_skill`, which returns the absolute `dir_path` so relative references resolve.
- `Registry.List` returns a defensive copy; `Delete` removes from disk first, then memory; `Delete` on a non-Loader provider returns `ErrDeleteUnsupported`. `SkillsPath()` (singular) is deprecated.

## Tests

`hot_reload_test.go` (reload, rate-limit contract, keep-last-valid),
`loader_files_test.go` (10 tests: file listing, traversal/symlink-escape/
dangling-link rejection, later-override, unknown-field logging),
`toolset_test.go` (absolute dir + allowed_tools passthrough), `registry_test.go`
(defensive copy, non-Loader delete), `prompts_test.go` (sorting, no duplicate
discovery). All pure filesystem tests — no environment constraints.
