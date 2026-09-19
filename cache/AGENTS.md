# cache — Durable JSON cache with TTL and cross-process locking

This package provides a small filesystem-backed cache for reusable metadata. It combines process-local locking, OS file locks, defensive path handling, and atomic replacement so multiple Friday processes can share a cache root.

## Files

- `store.go` defines `FileStore`, options, status values, entry validation, and cache operations.
- `time.go` provides the replaceable clock used by TTL logic and tests.
- `lock_unix.go` and `lock_windows.go` implement platform-specific cross-process locks.
- `replace_unix.go` and `replace_windows.go` implement platform-specific atomic destination replacement.
- `store_test.go` covers hits, misses, expiration, pruning, validation, and persistence.

## Public API

```go
func NewFileStore(root string, opts ...Option) *FileStore
func (s *FileStore) Get(ctx context.Context, namespace, key string, target any) (Status, error)
func (s *FileStore) Put(ctx context.Context, namespace, key string, value any, ttl time.Duration) error
func (s *FileStore) Delete(ctx context.Context, namespace, key string) error
func (s *FileStore) Clear(ctx context.Context, namespace string) error
func (s *FileStore) Prune(ctx context.Context, namespace string) error
```

`Status` distinguishes a miss, a fresh hit, and a stale entry. Callers must handle stale data explicitly instead of treating every decoded value as fresh.

## Storage contract

- Values are JSON encoded in namespace/key entries under the configured root.
- Every operation takes a namespace-scoped in-memory lock and the matching cross-process file lock.
- Writes use a temporary file, flush/sync steps, and platform-specific replacement. Do not replace this with direct truncating writes.
- Namespace and key validation forbids traversal and unsafe names.
- Existing symlinks are rejected; cache operations must not follow links out of the root.
- An encoded entry larger than 16 MiB is rejected.
- A non-positive TTL follows the semantics defined in `store.go`; preserve those semantics when adding callers.
- `Get` decodes only validated entries and reports malformed data as an error rather than silently inventing a hit.
- `Prune` removes expired entries in one namespace. `Clear` removes all entries in that namespace while preserving store isolation.
- Context cancellation is checked around lock and filesystem work.

## Platform code

Keep Unix and Windows locking/replacement behavior equivalent. Any storage-format or durability change must be reviewed on both implementations even if development occurs on one OS.

## Tests

Run:

```bash
go test ./cache
```

Tests use temporary directories and a controllable clock. Add focused tests for path validation, stale status, corruption, and interrupted-write behavior when changing the format.
