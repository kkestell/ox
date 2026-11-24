# Review: bind each reload to the stream that started it

## Scope and coverage

Uncommitted changes in the `reload-retry-stream` worktree against base
`a38aced` (`git merge-base origin/main HEAD`), checked against
`agents/plans/2026-11-24-reload-retry-stream.md`. The tree holds changes to
`cmd/ox/web/src/connection.ts`, `cmd/ox/web/src/ox.tsx`,
`cmd/ox/web/e2e/{session.spec.ts,ui.ts,workspace.spec.ts}`, and
`docs/architecture.md`, plus the untracked plan. `docs/functional-requirements.md`
is unchanged, as the plan's Requirement changes (none) require.

The change resolves [#72](https://github.com/kkestell/ox/issues/72). No other
open `bug` issue touches this code.

Gaps:

- `/ox-work`'s final response, with its Tests proved and Checks, was not passed
  to this review. The checks below were run here instead.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                   |
| -- | -------- | -------- | ------ | -------- | ------------------------------------------------------- |
| F1 | Low      | Testing  | Fix    | Resolved | No test binds a `session/created` load to its stream    |
| F2 | Low      | Testing  | Fix    | Resolved | The Send-disabled assertion passes with the bug present |

## F1. No test binds a `session/created` load to its stream

Low · Testing · Fix · `cmd/ox/web/src/ox.tsx:689`

The plan's second Decision makes the load that `session/created` starts belong
to the stream that was live when it started, through `live()`'s `rpc`. No test
protects it. Passing `connection.rpc` instead, which reads the current stream
on each attempt, lets a load whose retry sleeps across a reconnect post
`session/get` on the new stream before that stream's reload has caught up:
the same escape #72 describes, through a different path.

### Evidence

Replacing `current.rpc` with `connection.rpc` at `ox.tsx:689` left the full
e2e suite passing (161 passed).

### Action

Add an e2e test under `clocked` in `session.spec.ts`, beside "a retry from a
dropped stream does not use the next stream", or extend that test: create a
session, then hold and fail the `session/get` that its `session/created`
starts, so its retry sleeps on the paused clock. Restart the relay, show the
page, and hold the new stream's `harness/list`. Count `session/get` posts from
that point, run the clock 1000 ms, release `harness/list`, wait for Send, and
expect exactly the reload's own `session/get` for that session. Confirm it
fails with `connection.rpc` at `ox.tsx:689`.

## F2. The Send-disabled assertion passes with the bug present

Low · Testing · Fix · `cmd/ox/web/e2e/session.spec.ts:1304`

After `page.clock.runFor(1000)` wakes the old retry, the test asserts
`await expect(send(page)).toBeDisabled()`. The assertion passes as soon as
Send is disabled, before the old retry's request could have reached `oxd` and
enabled it, so it does not wait for the event it claims to check. Only the
`workspace/list` count at the end guards the fix.

### Evidence

With `connection.ts` and `ox.tsx` reverted to the base, the test passed the
`toBeDisabled` assertion and failed only at `expect(lists).toHaveLength(1)`
(received 2).

### Action

Delete the `toBeDisabled` assertion and the comment above `runFor` that
describes it, leaving the count, which fails on the base, as the test's check.
A disabled Send has no event to wait for on the fixed code, because the old
retry fails without a request.

## Checks run

- `make test`: passed.
- `make e2e`: 161 passed, 4 skipped.
- The new test with `connection.ts` and `ox.tsx` at the base: fails at the
  `workspace/list` count (2, expected 1).
- Spot check, `send` without `stream.connection !== sentOn`: the new test fails
  (2 `workspace/list` posts).
- Spot check, `onError` without `setLive(undefined)`: 6 e2e tests fail,
  including "Archive and Unarchive are unavailable while not connected" and "a
  lost connection keeps the tree and lists nothing until the page has
  reloaded".
- Spot check, `session/created` through `connection.rpc`: full suite passes
  (F1).
- Headless Chrome against this worktree's `bin/oxd` and `bin/ox` on its
  `.state`: with a session in the store, stopping and restarting `ox` showed
  "Reconnecting…", then the page posted `harness/list`, `workspace/list`, and
  `session/get` on the new stream, with no alert and no page error.
- `make live`: not required; the change does not alter how Ox drives Claude
  Code.

## Verification 1

Checked the working tree on base `a38aced` after the fixes. Since the review,
only `cmd/ox/web/e2e/session.spec.ts` changed; the source is as reviewed.
`make test` passed; `make e2e` passed (162 passed, 4 skipped). Biome finds
nothing in the changed spec.

| ID | Status   | Evidence                                                                                                                                                                                                            |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | "a session's load from a dropped stream does not use the next stream" fails with `connection.rpc` at `ox.tsx:689` (an extra `session/get` before `workspace/list`) and on the base source, and passes with the fix |
| F2 | Resolved | The `toBeDisabled` assertion is gone; "a retry from a dropped stream does not use the next stream" still fails on the base source at the `workspace/list` count and passes with the fix                            |

No new findings.

## Verdict

Ready to commit.
