# Model catalog freshness

## Goal

The OpenRouter model catalog cache has no notion of age, so a cache written
months ago is served as authoritative. A cache that parses but holds no models
is accepted the same way, and once memoized it answers every model lookup for
the process with an empty catalog.

The refresh meant to cover that runs in a detached goroutine nobody owns. It
writes the file and stops there, so the process keeps serving what it already
loaded, and neither its lifetime nor its failures are tied to anything. Both the
cache file and the catalog response are read without a size limit.

## Desired outcome

A cache is used while it is fresh, refetched once it is not, and never accepted
empty. Reads are bounded. No detached goroutine owns any part of this.

## Summary of approach

Give the cache a maximum age measured from the file's modification time. A fresh
cache is served as it is now. A stale or unreadable one is refetched on the
spot, which removes the background refresh and the question of who owns it; if
that fetch fails, a stale cache is still better than no catalog, so it is served
with a warning. Reject a cache that decodes to no models, and bound both the
cache file and the HTTP response.

## Related code

- `internal/openrouter/catalog.go` - `loadCatalog`, `refreshCatalog`,
  `fetchCatalogAttempt`, and the memoized `Catalog`.
- `internal/openrouter/cache.go` - `readCatalogCache`, `writeCatalogCache`.

## Current state

- Relevant existing behavior: `Catalog` memoizes the loaded catalog for the
  client's lifetime, which stays as it is; an Ox process serves one client
  connection.
- Existing patterns to follow: `maxErrorBodySize` already bounds the error body
  read on the streaming path.
- Constraints from the current implementation: the cache file holds only the
  model envelope, so its age comes from the filesystem rather than its contents.

## Test plan

- **Key behaviors to verify:** a fresh cache is served without a request, a
  stale cache is refetched and rewritten, a stale cache survives a failed fetch,
  an empty cache is a miss, and an oversized response is refused.
- **Test levels:** unit, in `internal/openrouter`.
- **Edge cases and failure modes:** a cache file at exactly the maximum age is
  still fresh.
- **What not to test:** the maximum age's value, which is a judgment rather than
  a provider fact.

## Implementation plan

- Report a cache entry's age and reject an empty one.
- Bound the cache file read and the models response.
- Serve a fresh cache, refetch a stale one, and fall back to stale on failure.
- Delete the detached refresh.
- Replace the tests that asserted background refresh behavior.

## Documentation updates

- Todo list item "Repair model-catalog caching and refresh ownership (F14)".

## Impact assessment

- Code paths affected: catalog loading at session activation.
- Data, protocol, or schema impact: none. The cache file format is unchanged.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the openrouter, agent, and
  integration suites.
- Static checks: `make check-go`.
