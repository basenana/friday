# types — Shared messages, deltas, events, and session data

This package defines the low-level data structures shared by API, session, agent, and provider packages. These types cross persistence and provider boundaries, so field and role changes require compatibility care.

## Files

- `session.go` defines message roles, content, tool calls/results, deltas, images, usage, and session-facing data.
- `event.go` defines core session event types and payload structure.
- `session_image_test.go` verifies image compatibility/serialization behavior.

## Data contracts

Messages represent system, user, agent/assistant, and tool traffic. Preserve role semantics when converting to provider-specific formats.

- Tool calls carry stable IDs, names, and arguments.
- Tool results must refer to the originating call ID.
- Images may be inline or referenced depending on the provider path; retain MIME/type metadata.
- Deltas are incremental stream records, not complete messages by default.
- Usage/token fields are observability and context-budget inputs; do not silently reinterpret their units.
- Event type strings are consumed by session hooks and UIs and should be treated as compatibility constants.

## Change rules

- Prefer additive fields with safe zero values.
- Keep JSON tags stable for persisted records and protocol conversions.
- Avoid methods with filesystem, provider, or runtime dependencies; this package should remain data-focused.
- Do not embed provider SDK types in shared structures.
- When introducing a new role or content variant, update every provider adapter and history validator in the same change.
- Ensure copied messages do not accidentally alias mutable slices/maps when isolation is required.
- Keep zero-value behavior useful because these structures are assembled by many packages.

## Tests

Run:

```bash
go test ./core/types
```

The package is intentionally small and should remain dependency-light. Add serialization and round-trip tests for any new field, image form, role, event, or tool payload.
