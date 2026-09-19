# sessions — Persistent root-session storage, lifecycle, and usage accounting

Persistent storage for root sessions: `Manager` (global current-session
tracking per TTY), the `RootCatalog`/`SessionLifecycle` scoped capabilities
(fork, temporary, associated children), the `Store` interface with optional
capabilities (events, metadata, planning, relations), the file-backed
implementation, and usage aggregates. Constructed in `cmd/root.go`; the
catalog path is consumed by `actor.Registry` and the daemon.

## Files

| File | Responsibility |
|---|---|
| `store.go` | `Store` interface + optional capability interfaces (`EventStore`, `MetadataStore`, `PlanningStore`, `RelationStore`) + metadata types (`SessionMeta`, `SessionRuntime`, `SessionMetaPatch`, `Relation`) |
| `manager.go` | `Manager`: current-pointer file, TTY-based alias, legacy get-or-create APIs, `RootCatalog` create/open, runtime setters (model/effort/mode), rename/archive/delete-root |
| `lifecycle.go` | `SessionLifecycle`/`RootCatalog` interfaces + `lifecycle` impl (forks/temporary/associated-child tracking, cross-process associated-session creation) |
| `file/store.go` | File-backed store implementing the base store and optional event, metadata, planning, and relation capabilities |
| `file/hooks.go` | History load/persist session hooks for the file store |
| `file/metadata_lock_unix.go` | Unix cross-process metadata locking |
| `file/metadata_lock_windows.go` | Windows cross-process metadata locking |
| `file/metadata_lock_other.go` | Fallback metadata locking on other platforms |
| `usage/usage.go` | Session usage aggregation and reporting |

## Key API (verbatim signatures)

```go
type Store interface {
    EnsureDir() error
    Create(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error)
    Load(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error)
    Delete(sessionID string) error
    List() ([]SessionMeta, error)
    ListActive() ([]SessionMeta, error)
    GetMeta(sessionID string) (*SessionMeta, error)
    UpdateAlias(sessionID, alias string) error
    Archive(sessionID string) error
    Unarchive(sessionID string) error
    LoadMessages(sessionID string) ([]types.Message, error)
}

type RelationStore interface {
    GetRelation(rootID, key string) (*Relation, error)
    PutRelation(Relation) error
    DeleteRelation(rootID, key string) error
    ListRelations(rootID string) ([]Relation, error)
    AcquireRelationLock(rootID, key string) (unlock func(), err error)
}

func NewManager(store Store, currentFile string, tty string) *Manager
func (m *Manager) GetOrCreateCurrent(opts ...coresession.Option) (*coresession.Session, string, bool, error)
func (m *Manager) CreateRoot(_ context.Context, client providers.Client, opts ...coresession.Option) (SessionLifecycle, error)
func (m *Manager) OpenRoot(_ context.Context, id string, client providers.Client, opts ...coresession.Option) (SessionLifecycle, error)
func (m *Manager) CreateIsolated(opts ...coresession.Option) (*coresession.Session, string, error)
func (m *Manager) CreateTemporary(opts ...coresession.Option) (*coresession.Session, string, error)
func (m *Manager) CollaborationMode(sessionID string) collaboration.Mode
func (m *Manager) ResolveActiveSession(target string) (*SessionMeta, error)
func (m *Manager) Rename(sessionID, requested string) (string, error)
func (m *Manager) DeleteRoot(sessionID string) error
func (m *Manager) LoadLatestPlan(sessionID string) (*planning.Artifact, error)

type SessionLifecycle interface {
    Current() *coresession.Session
    RootID() string
    Fork() (*coresession.Session, error)
    CreateTemporary(opts ...coresession.Option) (*coresession.Session, error)
    GetOrCreateAssociated(ctx context.Context, spec AssociatedSpec, opts ...coresession.Option) (*coresession.Session, bool, error)
    Release(child *coresession.Session) error
    Close() error
}
func BindLifecycle(root *coresession.Session, client providers.Client, store Store) SessionLifecycle
```

## Behavior invariants

- Two creation paths, deliberately: **legacy** `GetOrCreateCurrent`/`GetOrCreateByID` mutate the global current pointer (TTY-keyed `current_<tty>` file); **catalog** `CreateRoot`/`OpenRoot` never touch it — agents receive only the resulting `SessionLifecycle`.
- `CreateTemporary` never persists; `CreateIsolated` never sets current and cleans up the created session if alias assignment fails.
- `DeleteRoot` deletes all `associated` relation children first, then the root.
- `GetOrCreateAssociated`: per-key memoization + cross-process `AcquireRelationLock`; on `PutRelation` failure the created session is deleted; returns `(child, created, err)`.
- `lifecycle.Close()` detaches all children then closes the root exactly once — a child `Close` would close the shared root event bus.
- `ResolveActiveSession` matches exact ID or Name first, otherwise a **unique** ID prefix (ambiguous prefix → error).
- `Rename` normalizes names (control chars → space, whitespace collapse, 200-rune cap) and dedupes with ` (n)` suffixes.
- `SetEffort` validates against `providers.IsValidReasoningEffort`; optional capabilities degrade explicitly (e.g. `UpdateMeta` errors "session store does not support mutable metadata"), **but** `LoadLatestPlan` returns `(nil, nil)` when unsupported (asymmetric with Save/Propose which error).
- `CollaborationMode` falls back to `ModeDefault` on any meta/parse error.

## Subpackage `sessions/file`

File-backed store implementing `Store` **and** all optional capabilities
(`store.go`), plus `LoadHistoryHook`/`PersistHook` adapters (`hooks.go`) and
cross-process metadata flocking (`metadata_lock_unix|windows|other.go`).

Disk layout `basePath/<sessionID>/`: `session.json` (meta, atomic 0644),
`history.jsonl` (one JSON message per line), `events.jsonl` (bounded actor
event log, 0600), `state/<namespace>.json` (session records), 
`session_memory.json`, `plans/<planID>.json`, `related/<sha256(key)>.json`,
`.session.lock` + `related/.locks/` (flocks).

Key signatures:
```go
func NewFileSessionStore(basePath string) *FileSessionStore
func (s *FileSessionStore) OpenEventSink(_ context.Context, id string) (actorsink.EventSink, error)
func (s *FileSessionStore) AppendMessages(sessionID string, msgs ...types.Message) error
func (s *FileSessionStore) ReplaceMessages(sessionID string, msgs ...types.Message) error
func (s *FileSessionStore) SavePlan(sessionID string, plan planning.Artifact) error
func (s *FileSessionStore) ProposePlan(sessionID string, plan planning.Artifact) (*planning.Artifact, error)
func (s *FileSessionStore) AcquireRelationLock(rootID, key string) (func(), error)
```

Invariants: the event log grows to 8MB then compacts to 6MB; a torn final
JSONL record (missing terminating newline) is repaired by truncation — earlier
malformed records surface as decode errors. `ReplaceMessages` renames the old
history to `history_origin_<timestamp>.jsonl` before rewriting. Metadata
mutations serialize through a per-session mutex plus `.session.lock` flock;
all writes are atomic temp-file+fsync+rename. `ProposePlan` commits
`meta.LatestPlanID` only after the artifact exists (a crash leaves an orphan
artifact; the previous plan stays authoritative); loading a superseded
proposed plan reports `ArtifactSuperseded`.

## Subpackage `sessions/usage`

Durable usage aggregates stored as the session record `state/usage.json`:
`Snapshot{Version, TrackingSince, UpdatedAt, Models []ModelUsage, Turns TurnUsage}`
with `ModelUsage` keyed by (model, endpoint) — calls, failures, prompt/
cached/completion tokens — and `TurnUsage` buckets (Count, EndTurn,
PlanCompleted, Failed, Cancelled, DurationMs). `Hook` implements
`coresession.AfterModelCallHook`; forked sessions inherit it, so fork calls
roll into the root snapshot. Writes go through the root session's record
store; only version 1 decodes.

```go
const RecordNamespace = "usage"
func RecordTurn(ctx context.Context, sess *coresession.Session, stopReason string, durationMs int64) error
func Read(ctx context.Context, sess *coresession.Session) (Snapshot, error)
```

## Tests

`sessions/file/store_test.go` (14 tests): concurrent metadata updates across
store instances, concurrent plan proposals, plan round-trip + status
transitions, event round-trip + truncated-tail repair, event compaction,
`ReplaceMessages` backup, session-memory round-trip, record serialization.
`sessions/usage/usage_test.go` (4 tests). No environment-constrained tests.
