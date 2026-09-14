# A held catalog on failure, and unused repeating model responses

## Goal

Keep an unreachable provider from failing a session the process could already
serve, and keep an end-to-end test from passing on a model response it never
reached.

## Desired outcome

When a catalog refresh fails and the process still holds a catalog, callers get
that catalog and the next attempt is bounded. A test that queues a repeating
model response and never reaches it fails.

## Summary of approach

`loadCatalog` already prefers an outdated disk cache to no catalog when the
provider is unreachable. Apply the same rule to the catalog held in memory,
which the freshness expiry made reachable: on a failed load, serve what the
process holds and push the expiry out by the retry interval. A cancelled load is
not an unreachable provider, so it keeps returning its own error.

The model mock marks a repeating response used when it answers a request.
Because such a response is never consumed, being used is the only thing its
cleanup check can assert.

## Related code

- `internal/openrouter/catalog.go` - `Catalog` and the disk stale fallback whose
  rule this mirrors.
- `internal/e2e/model_test.go` - The queued endpoint and its cleanup check.

## Current state

- `catalogExpires` advances only on a successful load, and a failed load returns
  its error even when the process holds a usable catalog. Before the in-memory
  expiry existed, the first catalog was returned for the life of the process, so
  neither behavior was reachable.
- The cleanup check skips repeating responses entirely, so one that never
  answers a request is silent.

## Test plan

- **Key behaviors to verify:** A failed refresh serves the held catalog, and the
  attempt after it is bounded rather than per request; a cancelled load still
  returns its error; an unused repeating response fails cleanup.
- **Test levels:** Focused `internal/openrouter` tests; the harness change is
  proved by the existing coordination test plus a temporary unused fallback.
- **What not to test:** The disk stale fallback, which is unchanged.

## Implementation plan

- Serve the held catalog on a failed load and bound the next attempt.
- Record whether a repeating response answered a request and assert it.

## Documentation updates

- None. `eng/architecture.md` already describes falling back to stale entries
  when the provider is unreachable and bounding the next attempt.

## Impact assessment

- Code paths affected: Model-catalog loading; the end-to-end model mock.
- Data, protocol, or schema impact: None.
- Dependency or API impact: None.

## Validation

- Tests to write and run: The tests above, then `make check`.
