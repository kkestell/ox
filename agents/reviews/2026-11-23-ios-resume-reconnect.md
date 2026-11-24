# Review: the page reopens its event stream itself

## Scope and coverage

Uncommitted changes on `ios-resume-reconnect` against base `6f6443f`, the merge base with `origin/main` and `HEAD`: `cmd/ox/web/src/connection.ts`, `cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/e2e/workspace.spec.ts`, and `docs/architecture.md`, checked against `agents/plans/2026-11-23-ios-resume-reconnect.md`. The plan makes no requirement changes, and `docs/functional-requirements.md` is unchanged.

The request named two departures from the plan. Both are judged here:

- **`route.fallback()` behind a `stalled` flag instead of `unroute`.** Correct and necessary. Unrouting the only `**/events` handler turns off request interception, which releases the held requests. On the base, the browser's own retry then completes and the test passes. With the flag, interception stays on and the held requests stay held. Confirmed below. The test does not say why, so a later edit could put `unroute` back and make the test hollow (F3).
- **`showPage` in the three clocked tests in `workspace.spec.ts`.** These tests pause the page's clock, so the new one-second reconnect timer never fires there. The implementer reconnects them by firing `visibilitychange`. That works, but it takes the visibility path instead of the timer that every real dropped connection takes, and `page.clock.runFor(1000)` reconnects them through the timer just as well (F2).

Gaps:

- `/ox-work`'s final response was not passed with the request, so its Tests proved and Checks lists were not available. This review ran the new test on the base, spot-checked removals, and ran `make test` and `make e2e`. The required headless Chrome check through `make dev` could not be confirmed (see Checks run).
- A stream that iOS leaves open on a dead socket is out of scope, as the plan's Decisions settle.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                               |
| -- | -------- | -------- | ------ | -------- | ------------------------------------------------------------------- |
| F1 | Medium   | Testing  | Fix    | Resolved | No test keeps a connected page's stream when the page is shown      |
| F2 | Low      | Testing  | Fix    | Resolved | Clocked workspace tests reconnect through visibility, not the timer |
| F3 | Low      | Testing  | Fix    | Resolved | The stalled test does not say why it falls back instead of unroutes |

## F1. No test keeps a connected page's stream when the page is shown

Medium · Testing · Fix · `cmd/ox/web/src/connection.ts:96`

`onVisible` leaves a `connected` stream alone. That guard is what keeps a desktop tab switch or an iOS resume on a healthy connection from replacing the stream. If the guard is lost, the page closes its stream every time it is shown, and the reply to any request in flight arrives on the closed stream. That request then never shows its result. This breaks "Every request settles" in normal use. No test fails when the guard is removed.

### Evidence

Changing line 94 to also close and reopen a `connected` stream leaves all 144 tests in `session.spec.ts` and `workspace.spec.ts` passing. A probe in `workspace.spec.ts` under `clocked` holds `workspace/list_folder` for `src`, clicks `src`, waits for the request to arrive, fires `visibilitychange`, releases the request, and expects `a.txt` to be visible with no alert. The probe passes on the change. With the mutation, it fails with `a.txt` not found. The probe has been removed.

### Action

Keep that probe as an e2e test in `workspace.spec.ts`, beside "a request cut off after its connection was replaced leaves the new connection alone": "a page shown while connected keeps a request in flight". It guards the `connected` branch of `onVisible`. Move `showPage` to `e2e/ui.ts` and use it in this test and in "a stalled reconnect resumes when the page is shown", in place of that test's inline `page.evaluate`.

## F2. Clocked workspace tests reconnect through visibility, not the timer

Low · Testing · Fix · `cmd/ox/web/e2e/workspace.spec.ts:452`

"a lost connection keeps the last listing", "a request cut off after its connection was replaced leaves the new connection alone", and "a lost connection keeps the tree and lists nothing until the page has reloaded" cover a dropped connection. They now reconnect through `showPage` at lines 452, 475, and 523, which is the iOS resume path. A real dropped connection reconnects through the page's one-second timer, and the plan's Test plan says these tests cover that timer. They do not: with the timer disabled, all three still pass. The showPage comment ("so the tests reconnect through it") is also unclear about what "it" is.

The timer is well guarded elsewhere: disabling it fails 13 tests in `session.spec.ts`. So the cost here is a misleading scenario and a lost check of the timer under these listing cases, not an unguarded branch.

### Evidence

Replacing each of the three `showPage(page)` calls with `page.clock.runFor(1000)` passes all three tests with `--repeat-each 3`. With `setTimeout(open, 1000)` replaced by a no-op timer, all three still pass on the implementer's version.

### Action

At lines 452, 475, and 523, reconnect with `await page.clock.runFor(1000)`, which fires the page's reconnect timer on the paused clock the way the file already advances the page's other timers. After F1, `showPage` lives in `ui.ts`. Its comment says only that it fires the event a browser sends when the page is shown again.

## F3. The stalled test does not say why it falls back instead of unroutes

Low · Testing · Fix · `cmd/ox/web/e2e/session.spec.ts:422`

"a stalled reconnect resumes when the page is shown" keeps its `**/events` handler installed and switches it to `route.fallback()`. This is needed because removing the last route turns off interception, which releases the held requests, and the test then passes on the base. The test's comment explains what is held but not this. `unroute` is the obvious-looking simplification, and it would silently make the test pass without the fix.

### Evidence

The test fails on the base `connection.ts`: "Reconnecting…" is still shown after `visibilitychange`. The same test with `page.unroute("**/events", hold)` in place of the flag passes on the base.

### Action

Add a one-line comment at the `stalled` flag: the handler stays installed because removing the route releases the requests it holds. This is an external rule of Playwright that a reader would not guess.

## Checks run

- The new test "a stalled reconnect resumes when the page is shown" on the base `connection.ts`: fails as intended.
- The same test with `unroute` in place of the `stalled` flag, on the base: passes, which confirms the implementer's reason.
- Spot-check, reconnect timer replaced by a no-op: 13 session tests fail, including "a reconnecting stream shows as a status, not an error"; the three clocked workspace tests pass (F2).
- Spot-check, the `rpc()` guard on a stale or missing connection removed: "a request cut off after its connection was replaced leaves the new connection alone" fails.
- Spot-check, `onVisible` reopening a `connected` stream: no test fails (F1).
- The three clocked workspace tests with `page.clock.runFor(1000)` in place of `showPage`: pass, repeated 3 times.
- `make test`: passes. `make e2e`: 144 passed.
- Not confirmed: the headless Chrome check through `make dev` that `AGENTS.md` ("Verification") requires, because this change alters how the page reaches `oxd`. The implementer reports it with the fixes. `make live` is not required, because the change does not alter how Ox drives Claude Code.

All edits made for these checks have been reverted.

## Verification 1

Checked the uncommitted tree on base `6f6443f` after `/ox-work fix`. The changes since the review are all in the e2e specs: `showPage` moved to `e2e/ui.ts`, the new test, the clocked tests' reconnects, and the comment at the `stalled` flag. `connection.ts` and `docs/architecture.md` are unchanged.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | "a page shown while connected keeps a request in flight" (`workspace.spec.ts:482`) fails when `onVisible` reopens a `connected` stream (`a.txt` not found), and passes on the change. "a stalled reconnect resumes when the page is shown" uses the shared `showPage`. |
| F2 | Resolved | The three clocked tests reconnect with `page.clock.runFor`. All three fail when the reconnect timer is a no-op, and pass on the change. |
| F3 | Resolved | The comment at the `stalled` flag says the handler stays installed because removing the route releases the requests it holds. |

In "a lost connection keeps the tree and lists nothing until the page has reloaded", `runFor(1000)` now repeats until the held `workspace/list` arrives. That test runs the clock for 3 s while the stream is down. A reconnect attempt can still be failing on its aborted request when the route is removed, and only the attempt after it gets through. That is how the page behaves: it retries each second until a retry succeeds. The loop waits on the reload request, which is the event the test asserts on, so it hides nothing. No new findings.

Checks run:

- The four reconnect tests and the new test with `--repeat-each 10`: 50 passed, none flaky.
- `make test`: passes. `make e2e`: 145 passed.
- `make dev` in this worktree, driven in headless Chrome: the page loads and connects with no "Reconnecting…" status and no alert, and stays connected when `visibilitychange` fires. Offline emulation did not cut Chrome's existing localhost stream. Dropped and stalled streams are therefore checked by the e2e suite's relay restarts against the real `ox` and `oxd` binaries.
- `make live`: not required, because the change does not alter how Ox drives Claude Code.

All edits made for these checks have been reverted.

## Verdict

`Ready to commit`. F1, F2, and F3 are resolved, and every required check passed.
