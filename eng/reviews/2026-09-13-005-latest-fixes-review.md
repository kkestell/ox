# Latest fixes review: a cancellable MCP listing, a held catalog, and unused model fallbacks

**Scope:** commits `9d9d622..644f975` on `main` — the fix batch for the
[completion review](2026-09-13-004-completion-review.md): refresh an MCP listing
under the caller's own context, serve a held model catalog when a refresh fails,
and fail an end-to-end test whose repeating model response is never used.

**Mode:** general.

**Topics selected:** concurrency, correctness, error-handling, resources,
testing, readability, documentation.

The batch changes how two pieces of shared state are published: the MCP server's
listing now has its critical section narrowed around a network round trip, and
the catalog's failure path writes the memoized catalog, its expiry, and the
error it returns. Concurrency and correctness lead for that reason. The catalog
branch converts an error into a success, so error-handling applies. The listing
map crosses the lock boundary into callers, so resources applies. Half of the
diff is tests or test infrastructure, and every fix is proved by a test, so
testing applies. The rest of the diff is prose that carries design claims in
code comments and two plans, so readability and documentation apply.

Not reviewed, with reasons: `unsafe` and `reflect` do not appear in the diff;
`performance` is not a separate risk because each changed path runs once per
listing expiry or once per failed load, not per request or per byte;
`api-design` and `dependencies` see no change, since every production edit is to
unexported helpers and no module or exported signature moved.

## Findings

### Low: the catalog failure branch logs while holding the catalog mutex

**Location:** `internal/openrouter/catalog.go:173`

The added `c.logger().Warn` call runs between `c.catalogMu.Lock()` and
`c.catalogMu.Unlock()`. `catalogLoading` stays true until the same critical
section ends, so every `Catalog` caller waiting on `ready` now waits for that
log write as well. A log destination that blocks — the shipped process writes
logs to a stderr an ACP client is reading — keeps the loading flag set and
stalls every catalog lookup rather than delaying one diagnostic line.

The pre-change failure branch held the mutex only for assignments, so this is
introduced by `e49c41b`.

**Suggested fix:** decide the branch under the lock, record the error that was
served, unlock after `close(ready)`, and log afterwards:

```go
c.catalogMu.Lock()
served, loadErr := false, err
switch {
case err == nil:
	c.catalog = catalog
	c.catalogExpires = catalogExpiry(time.Now(), age)
case c.catalog != nil && ctx.Err() == nil:
	catalog, err, served = c.catalog, nil, true
	c.catalogExpires = time.Now().Add(catalogRetryInterval)
}
c.catalogLoading = false
close(ready)
c.catalogMu.Unlock()
if served {
	c.logger().Warn("serving the held OpenRouter catalog", "error", loadErr)
}
return catalog, err
```

## Unresolved suspicions

- **Cold-start outages still refetch per request.** The bound added here applies
  only when a catalog is held, which is what its comment and the todo item
  describe. With no held catalog and no readable disk cache, `c.catalog` stays
  nil, so every caller enters the load path and pays the full retry budget (five
  attempts, 30 seconds) before failing. That is the pre-existing behavior, and a
  negative-cache deadline would need a second expiry field for a case where no
  answer can be produced anyway. Recorded, not proposed.
- **Two concurrent listings can publish out of order.** Two callers that arrive
  together after an expiry both list and both publish; the second publisher's
  `listedAt` is later, so the surviving listing is never older than it claims.
  Both listings are complete, so no caller can observe a mixed pair.

## Checks run

- `go vet ./internal/mcp/ ./internal/openrouter/ ./internal/e2e/` — clean.
  `go vet ./...` failed in `internal/agent` on per-model settings work that was
  uncommitted in the tree at review time (since committed as `6492bae`), which
  is not part of this batch.
- `go test -race -count=1 ./internal/mcp/ ./internal/openrouter/` — pass.
- `go test -race -count=10 ./internal/mcp/ -run TestCallCancellationDoesNotWaitOnAnotherListing`
  — pass, so the new concurrency test is not order- or timing-fragile at that
  repetition.
- `go test -count=1 ./internal/e2e/` at a clean `HEAD` checkout — pass, 24.7s,
  confirming the stricter repeating-response check does not fail an existing
  test.
- Reverted each fix in a scratch worktree to confirm its test fails for the
  right reason: the MCP test failed after 5.01s with the message
  `cancelled call waited on another call's listing` against the old locking; the
  held-catalog subtest failed with
  `catalog = (*openrouter.Catalog)(nil), OpenRouter returned 400 Bad Request`;
  and a temporarily queued, never-reached repeating response failed the new
  cleanup check while the old harness passed the same test. Files were restored.
- `make check` was not run in the workspace because the working tree carries
  unrelated in-progress settings changes; the affected packages and the full
  end-to-end suite were run against `HEAD` in a detached worktree instead.

## Topic verdicts

- **Concurrency:** one low finding, the log write inside the catalog mutex. The
  MCP change is sound: `currentTools` holds `listMu` only to copy the listing or
  to publish one, the published map is never mutated afterwards, and the new
  test fails against the old locking. `publishTools` is the only production
  writer of the two fields.
- **Correctness:** nothing to report. The failure branch serves the held catalog
  only when one exists and the caller is still live, sets the next attempt five
  minutes out, and leaves a cancelled or catalog-less load returning its own
  error. The returned catalog, the error, and the expiry agree on every branch.
- **Error handling:** nothing to report. Cancellation propagates as `ctx.Err()`
  from `fetchCatalog` and is not converted into a stale success; the failure
  branch's message is unchanged and still names the OpenRouter failure.
- **Resources:** nothing to report. `currentTools` hands out a map that no path
  mutates after publication, and `publishTools` stores the map the caller
  already owns; both were true before this batch.
- **Testing:** nothing to report. The new MCP test cancels the call that does
  not hold the listing, which is the only arrangement that distinguishes the
  fix, and the cancelled-load catalog subtest is a guard rather than a fix
  proof: it passes against the pre-fix code and fails if the `ctx.Err()`
  condition is removed. The harness change was proved by injecting an unused
  fallback.
- **Readability:** nothing to report. The comments on `currentTools` and
  `publishTools` state why the critical section is narrow and why the fields
  have one writer; the catalog switch reads as three plain cases.
- **Documentation:** nothing to report. `eng/architecture.md` already owns the
  stale-fallback and bounded-retry rule and the bounded-age listing rule, and
  both plans match what shipped, so no owning document needed an edit.

## Follow-up

The low finding is a small reordering of the failure branch and does not change
the behavior its test asserts. No other follow-up needs evidence outside this
corpus.
