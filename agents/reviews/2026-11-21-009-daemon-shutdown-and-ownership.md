# Review: store reply text at once, own the database, and stop the dev stack cleanly

## Scope and coverage

The uncommitted changes on base `539d62a` for
[the plan](../plans/2026-11-21-009-daemon-shutdown-and-ownership.md):
`internal/store`, `internal/server/history.go`, `cmd/oxd`, `cmd/ox/web/stack.mjs`,
`dev.mjs`, `stack.test.mjs`, `e2e/services.ts`, `e2e/tsconfig.json`,
`docs/architecture.md`, `docs/protocol.md`, and `docs/todo.md`. The user's edits
under `.claude/skills/` are outside the review.

Gaps:

- `/ox-work`'s final response with its Tests proved and Checks lists was not
  passed on. The tests were proved here instead, as Checks run lists.
- The F2 fix was not run under a real `make dev`. It would bind the default
  socket and database, and an Ox daemon is running on this machine. A probe ran
  the real `runStack` in its own process group instead.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                |
| -- | -------- | -------- | ------ | -------- | ---------------------------------------------------- |
| F1 | Low      | Testing  | Fix    | Resolved | e2e `stop` leaves `oxd` running when the relay fails |

## F1. e2e `stop` leaves `oxd` running when the relay fails

Low · Testing · Fix · Resolved · `cmd/ox/web/e2e/services.ts:108`

`stop` awaits `stopProcess(relay)` before it stops the daemon. `stopProcess`
can now reject. A relay that exited with status 66 from a race report during a
test, or that exits other than on `SIGTERM`, rejects the first await. Then the
daemon is never signalled, and the temporary directories are never removed.
The test fails as it should, but `oxd` keeps running and holds its socket and
database after Playwright finishes. Before the change, `stopProcess` never
rejected, so `stop` always stopped both processes.

### Evidence

From the source: `stop` runs `if (relay) await stopProcess(relay); await
stopProcess(daemon);`. `stopProcess` judges an already-exited process by its
recorded `exitCode`, and rejects on any status other than 0
(`stack.mjs:13-23`). The e2e environment sets `GORACE=halt_on_error=1` for both
binaries.

### Action

In `stop`, signal both processes before awaiting either. For example, use
`Promise.allSettled([relay && stopProcess(relay), stopProcess(daemon)])` and
throw the first rejection after both have exited. Keep the check that `oxd` did
not exit during the tests. No new test: the failure needs a relay race, and
`stopProcess`'s rejection is covered by `stack.test.mjs`.

## Checks run

- `go test -race -count=1 ./...`: pass. `make test`: pass, including the 12
  stack tests.
- `make e2e`: 41 passed, 3 skipped.
- `npx tsc -p e2e` and `biome check` on the changed web files: clean.
- `TestHistoryMergesTextAndKeepsConversation` against the base `history.go`:
  fails at the mid-reply check. It also fails when `entries` never merges, and
  when it merges across message IDs.
- With `flock` removed from `store.Open`: `TestStoreHoldsDatabaseUntilClose`
  fails, and `TestDaemonOwnsSocketAndDatabaseUntilShutdown` fails because the
  daemon on `two.sock` was not refused.
- With `stop(130, null)` changed to `stop(130)`: "one terminal Ctrl-C signals
  each service once" fails in 3 of 3 runs.
- With `restart` swallowing the `stopProcess` rejection: "a retiring service
  that fails stops the stack" never finishes, because the stack never resolves.
  It hangs rather than fails, like the other stack tests that await the stack.
- Process-group probe: `runStack` with `signals: process` ran in its own
  session, with two services that log what they trap and a no-handler service
  standing in for the relay. A `SIGINT` sent to the group gave 130 and `INT`
  alone in each log, in 15 of 15 runs. This confirms the plan's ordering claim
  for a service that dies from the same `SIGINT`.
- `make live` was not run: the change does not alter how Ox drives Claude
  Code. No headless Chrome check: the change does not alter how the page
  reaches `oxd`.

## Verification 1

Checked the working tree on base `539d62a`. The only change since the review is
`stop` in `cmd/ox/web/e2e/services.ts`. Checks: `make test` passes, `make e2e`
passes (41 passed, 3 skipped), and `npx tsc -p e2e` and `biome check` are
clean.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `stop` creates both `stopProcess` promises before awaiting, so both processes are signalled. It waits for both with `Promise.allSettled`, then throws the first rejection. As planned, no new test. |

No new findings.

## Verdict

Ready to commit.
