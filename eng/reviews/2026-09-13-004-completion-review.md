# Completion review: auto mode through the Unix-only platform contract

**Scope:** commits `0660620..56a827c` on `main` — auto mode, turn finalization
for malformed and partial provider completions, catalog freshness bounds, gate
determinism and shipped-process coverage, and the Unix-only platform contract.

**Mode:** general.

**Topics selected:** concurrency, correctness, security, testing, architecture,
documentation, dependencies.

The change adds one new mutex and one new expiry rule reached from concurrent
tool loops, so concurrency and correctness lead. Auto mode removes a permission
prompt, which is a trust-boundary change. Roughly two thirds of the diff is
tests and test harness, so testing is central. Three owned contracts moved, so
documentation applies, and `go.mod` changed, so dependencies applies.

Not reviewed, with reasons: `unsafe` and `reflect` are absent from the diff;
`error-handling` and `readability` see only local, idiomatic changes;
`resources` and `api-design` changes are confined to unexported helpers already
covered under architecture; `performance` is the motivation for the catalog work
rather than a separate risk, and is reported under correctness where it bites.

## Findings

### Medium: a blocked MCP listing makes a concurrent call on the same server uninterruptible

**Location:** `internal/mcp/mcp.go:88-99`

`currentTools` holds `listMu` across `discoverTools`, which is a network round
trip over as many as 256 pages. The primary agent and a child may call the same
server concurrently, and a child's call never waits on the primary elsewhere. A
second caller now blocks on a plain mutex, so its own `ctx` cannot reach it:
when the user cancels the turn, that goroutine stays blocked until the holder's
120-second call deadline expires.

Before the freshness window each call listed under its own context, so
cancellation reached both promptly. `eng/architecture.md` says cancellation
reaches child loops and tool work.

**Suggested fix:** perform the listing outside the lock, or single-flight it
behind a channel a waiter can select on alongside `ctx.Done()`, as
`Client.Catalog` already does for the model catalog.

### Medium: a failed catalog load leaves the in-memory expiry in the past

**Location:** `internal/openrouter/catalog.go:139-168`

`catalogExpires` advances only when `loadCatalog` succeeds. When the process
holds an expired in-memory catalog and the load then fails with no stale disk
fallback, two things follow: `Catalog` returns the error rather than the usable
catalog it still holds, and the expiry stays in the past, so every subsequent
call re-enters the load path and refetches with its full retry budget.

Before this change the memoized catalog was returned for the life of the
process, so both behaviors are new. `catalogRetryInterval` bounds only the
success path, which is the one case that already has a fallback.

Reaching it needs the disk cache to be absent or unwritable and the provider to
be unreachable, so the shipped binary is not exposed by default. The consequence
when it is reached — a fetch storm and a failed activation that a held catalog
could have served — is out of proportion to the fix.

**Suggested fix:** on a failed load with an existing in-memory catalog, serve
that catalog and set `catalogExpires` to `catalogRetryInterval` from now.

### Low: activation writes the mutex-guarded listing fields without the mutex

**Location:** `internal/mcp/mcp.go:243-248`

`connectServer` assigns `value.tools` and `value.listedAt` directly. This is
safe today because the server is not published until `Activate` returns, so the
bundle's construction establishes the happens-before edge. Both fields are now
guarded everywhere else, which makes the exception easy to break without a test
noticing.

**Suggested fix:** either set them through the same path the refresh uses, or
say in a comment that activation owns these fields exclusively until the bundle
is published.

### Low: an unused repeating mock response passes the harness's response check

**Location:** `internal/e2e/model_test.go`

The mock's cleanup counts unconsumed responses but skips those marked `repeat`,
so a `queuePrimaryDefault` that never fires is silent. The one current use
cannot go unexercised, because the fallback is what ends the turn. A future test
could queue a fallback, never reach it, and still pass.

**Suggested fix:** record whether a repeating response answered at least one
request and fail cleanup when it did not.

## Completeness against the todo items

Every finding the two source reviews raised for these items is addressed, and
both of the rough-edges review's open suspicions are now settled: the platform
contract is stated, and the MCP catalog-refresh cost is bounded.

- Auto mode: the mode is advertised, validated, persisted, and applied; one
  predicate serves the primary and child gates; shipped-process tests cover
  execution, child inheritance, reload, and a recovered code-mode wait; the
  specification, architecture, and README are updated.
- Turn finalization: both the malformed-ID and the cancelled-with-fragment paths
  are fixed and each has a regression test that fails against the old code.
- Catalog freshness: both halves are bounded, and the prefix-stability property
  the change depends on is pinned by an end-to-end test.
- Gate and coverage: the flaky assertion is replaced with a deterministic one,
  and resume, both remaining language tools, two-child coordination, and the
  evaluation relative output path all run through the shipped artifact.
- Platform: the Windows surface is gone, the contract is stated in three places,
  the trailing-terminator rule covers CRLF, and the four documentation drifts
  are repaired.

## Unresolved suspicions

- The MCP listing lock's practical effect on cancellation latency was reasoned
  from the code rather than reproduced. A test that starts two concurrent calls
  against a server that never answers `tools/list`, cancels the turn, and
  asserts prompt return would settle it.
- `listMaxAge` and `catalogRetryInterval` are judgement calls, not measurements.
  Neither has been observed against a large live MCP server or a real provider
  outage.

## Checks run

- `make check` and `make test-eval` at each commit; both green, including the
  race-enabled suite.
- `go test ./internal/agent/ -run TestExclusiveTool -count=60` and
  `-count=40 -race`; no failures, against 26 of 50 before the replacement.
- `go test ./internal/e2e/ -run 'TestConcurrentSubagentsCoordinate|TestParentCompletionCancels' -count=10 -race`;
  no failures.
- Reverted each of the three behavior fixes in turn and confirmed its new test
  fails: the turn-finalization pair, and the evaluation output-path
  normalization.
- `GOOS=windows go build ./...` and `go vet ./...` before the platform change,
  confirming both failures the platform finding described.
- Traced `p.received` to a single synchronous reader, confirming
  `assertNoPermissionRequest` reads it without a race.

## Topic verdicts

- **Concurrency:** one medium finding; the catalog single-flight and the frozen
  turn configuration are otherwise sound.
- **Correctness:** one medium finding on the catalog failure path. Mode
  application, tool-call validation, cancelled-exchange persistence, and the
  trailing-terminator rule are correct at their boundaries.
- **Security:** nothing to report. Auto mode removes only the prompt;
  confinement, read evidence, validation, and output bounds are below the gate
  and unchanged, and auto authorization creates no grant and no durable
  decision.
- **Testing:** one low finding on harness strictness. Coverage is otherwise
  proportionate and the new tests were verified to fail against the old code.
- **Architecture:** nothing to report. The mode predicate, the listing window,
  and the three mock primitives each name one concept rather than adding a
  framework.
- **Documentation:** nothing to report. Each moved fact has one owner, and the
  stale exclusion-lock and Windows claims are gone.
- **Dependencies:** nothing to report. `golang.org/x/sys` correctly became
  indirect; no version moved and nothing was added.

## Follow-up

All four findings were implemented in `9d9d622`, `e49c41b`, and `7500630`, and
re-reviewed for completeness and simplification. No new findings.

The MCP fix narrows the critical section rather than single-flighting the
refresh, which was the suggestion here. Two callers arriving together after an
expiry now both list, which is what happened before the window existed;
single-flight machinery would coordinate a duplicate that is rare and harmless.
Publishing through one method also resolved the separate field-ownership finding
without a comment naming an exception.

Each fix was confirmed to fail against the behavior it replaces. The MCP test
needed care: cancelling the call that holds the listing passes against the old
locking, because releasing the holder releases the mutex. The defect is that a
_second_ call's own cancellation cannot reach it, so the test cancels a
different call under a different context while the first is still listing.
