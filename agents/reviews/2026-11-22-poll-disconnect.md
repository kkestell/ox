# A POST that cannot reach the relay drops the page's connection

## Scope and coverage

Uncommitted changes on `poll-disconnect` against base `f0580d5`, checked against `agents/plans/2026-11-22-poll-disconnect.md`: `cmd/ox/web/src/connection.ts`, `cmd/ox/web/src/poll.ts`, `cmd/ox/web/e2e/workspace.spec.ts`, `docs/architecture.md`. Proportionate review at the parent session's request: no child sessions, no full suite reruns.

The CI failure behind PR #40 (run 45109619327, e2e shard 4) timed out clicking `lib` at `workspace.spec.ts:427`: the root listing had been replaced by an error. A root poll whose POST was cut off by `restartRelay` before the stream reported the drop rejected with `Error("Could not reach ox")`, which `poll.ts` shows in place of the listing. The change maps every rejected `fetch` to `Disconnected` in `connect`, and `poll.ts` keeps the last listing on `Disconnected`. The test's outcome no longer depends on whether the stream or a POST notices the restart first. The race is gone at its cause.

When a POST notices first, the page opens a fresh `EventSource` while `/events` is aborted. A probe confirmed Chrome keeps such a stream in `CONNECTING` and retries every 3 s, so the flaky test's later reconnect still happens on that path.

"a draft keeps its permission mode through a relay restart" (one failure in 860 runs, no error text): no path in this change leads to that failure. On the restart, a failed poll POST now replaces the stream at once instead of leaving the old stream to retry. Either stream reconnects only after the new relay is up (`restartRelay` waits for the old relay to exit), fires one `onConnect`, and sends one `workspace/list` for the test's `hold`. A stale POST cannot drop the new connection because of the `sentOn` guard. Before the change, the same failed POST showed "Could not reach ox" in the file tree, which that test does not assert. The cause of that failure is not determined. It is not attributable to this change.

Gaps:

- The `make dev` check did not exercise a poll. The changed path is covered in real Chrome by e2e.

## Summary

| ID | Severity | Category     | Action | Status | Title                                                    |
| -- | -------- | ------------ | ------ | ------ | -------------------------------------------------------- |
| F1 | Low      | Testing      | Fix    | Open   | New test asserts an alert that shows for about 5 ms      |
| F2 | Low      | Testing      | Fix    | Open   | No test guards the stale-POST check                      |
| F3 | Low      | Construction | Fix    | Open   | A POST failing after `close()` reopens the stream        |

## F1. New test asserts an alert that shows for about 5 ms

Low · Testing · Fix · `cmd/ox/web/e2e/workspace.spec.ts:414`

In "a lost connection keeps the last listing", the aborted POST replaces a healthy stream. The replacement connects at once, and `reload` clears the page error as its first step. The "Lost the connection" alert is therefore visible only between `lose` and the next `connection` event. The test asserts it is visible inside that window. It passes because Playwright's poll happens to land there, so on a slower runner it can become the next flake.

### Evidence

I added temporary timestamps to `lose` and the `connection` listener and ran the test 3 times: `lose` → alert seen +2–3 ms → `connect` +5 ms in every run. The test also passed in 30 of 30 unmodified repeats, so the assertion holds for now but depends on timing.

### Action

Before aborting the POST, route `**/events` to `route.abort()`. The replacement stream then stays in `CONNECTING`; I confirmed this with a probe. Assert the alert, `a.txt`, and no "Could not reach ox". Then unroute both and expect the alert gone and Send enabled. This keeps the test's purpose and makes the alert's lifetime depend on the test, not on reconnect latency.

## F2. No test guards the stale-POST check

Low · Testing · Fix · `cmd/ox/web/src/connection.ts:99`

`if (connection !== sentOn)` keeps a POST that was sent on an earlier connection from dropping the current one. No test rejects a POST after its connection was replaced: the flaky test's held listing is released into a 503, not a network error. Removing the guard leaves every test green, and a stale rejection would then tear down a healthy stream and reload the page.

### Evidence

Traced every test that aborts a `/rpc` POST. Only the new case in "a lost connection keeps the last listing" does, and it aborts on the current connection.

### Action

Add an e2e case, in "a lost connection keeps the last listing" or a sibling: `hold` a `src` listing, `restartRelay`, wait for the reconnect (Send enabled), then abort the held route (extend `hold` with an abort, or route it directly). Expect no "Lost the connection" alert and no further `workspace/list`. Confirm that it fails with the guard removed.

## F3. A POST failing after `close()` reopens the stream

Low · Construction · Fix · `cmd/ox/web/src/connection.ts:106`

`close()` closes `source` and fails pending requests but leaves `connection` set. A POST in flight at `close()` that later rejects still sees `connection === sentOn`. It then calls `lose`, which runs `onError` on a disposed owner, and `open()`, which starts an `EventSource` that nothing closes. Its `onConnect` then runs `reload` into disposed state. Before this change, a rejected POST only failed its own request. A closed connection reopening a stream is a state the design forbids. Reaching it takes a dev HMR teardown while a POST is in flight and the relay is unreachable.

### Evidence

Traced from source: `close()` at `connection.ts:106-109` and the `catch` at `:95-103`.

### Action

Set `connection = undefined` in `close()`, so a stale rejection takes the `Disconnected`-only path. No test: only HMR teardown reaches it.

## Checks run

- From `/ox-work`: `make test` passed, `make e2e` passed (127 passed, 4 skipped), and `workspace.spec.ts` ×20 twice (859/860, 860/860). `make live` was not run and is not required, because the change does not alter how Ox drives Claude Code. The `make dev` check in headless Chrome loaded the page cleanly.
- "a lost connection keeps the last listing" ×30: 30 passed.
- Temporary instrumentation (timestamps, `readyState` in `onerror`) with `/events` aborted before the POST abort: the replacement stream errors with `readyState` 0 every 3 s and connects once unrouted. Instrumentation reverted.
- The implementer reports that the new test fails on the base. This matches the source: the base shows "Could not reach ox" in place of the listing.
- CI log of run 45109619327: timeout clicking `lib` at `workspace.spec.ts:427`, consistent with the root poll's error replacing the listing.

## Verification 1

Uncommitted tree on base `f0580d5` after the fixes: `connection.ts` `close()` clears `connection`; `ui.ts` `hold` gains `abort()`; `workspace.spec.ts` aborts `**/events` before the POST abort and adds "a request cut off after its connection was replaced leaves the new connection alone".

| ID | Status   | Evidence                                                                                                                                  |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `**/events` is aborted before the POST abort and unrouted after the alert assertion, so the alert stays up until the test releases the stream |
| F2 | Resolved | New test fails in every run of ×5 with the `sentOn` guard replaced by `false` (`workspace/list` called after the stale abort), passes with it |
| F3 | Resolved | `close()` sets `connection = undefined`, so a rejection after close takes the `Disconnected`-only path                                     |

No new findings.

Checks: both changed tests ×10 each, 20 passed. From `/ox-work`: `make test` passed, `make e2e` passed (128).

## Verdict

Ready to commit. F1, F2, and F3 are resolved, and the flake's cause is fixed.
