# Review: archive idle sessions automatically, and pin sessions to keep them

## Scope and coverage

Reviewed the uncommitted changes on `auto-archive-and-pin` against base
`a2f1421` (`HEAD` and `git merge-base origin/main HEAD`), against the plan
`agents/plans/2026-11-28-auto-archive-and-pin.md`, whose requirement changes
the user approved. Covered: the configuration field and its checks, the
`pinned` table and the stored `ActiveAt`, `activeAt` and `pinned` under
`Server.mu`, `familyIdleLocked`, the sweep and its shutdown, `archive(auto)`,
`session/pin` and `session/unpin`, the page's projection, order, row style, and
menu, the e2e and Go tests, and the docs and requirements. The requirements
diff matches the plan's Requirement changes word for word.

Gaps:

- The implementer's Checks were not passed to the review. `make test` and the
  three pin and menu specs were run here; the full `make e2e` and the headless
  Chrome check of Pin and Unpin from both menus, in light and dark mode, are for
  the implementer to report.
- The re-check of `familyIdleLocked` inside `archive(true)`, which settles a pin,
  prompt, or unarchive that lands between the sweep and the archive, was traced
  but has no test; the plan does not ask for one.

## Summary

| ID | Severity | Category      | Action   | Status   | Title                                                        |
| -- | -------- | ------------- | -------- | -------- | ------------------------------------------------------------ |
| F1 | Low      | Testing       | Fix      | Resolved | No test keeps a family with a background task from archiving |
| F2 | Low      | Testing       | Fix      | Resolved | No test counts a setting change as use                       |
| F3 | Low      | Documentation | Fix      | Resolved | The owner table omits pinning and `activeAt`                 |
| F4 | Low      | Requirements  | Rejected | —        | A pin in flight briefly disables the session's composer      |

## F1. No test keeps a family with a background task from archiving

Low · Testing · Fix · `internal/server/archive.go:105`

`familyIdleLocked` requires every member's published state to be `parked`. That
check is the plan's guarantee that a session with a background task, a
schedule, or a waiting approval is never archived. Removing it leaves every
test passing. `archive`'s own `free()` check does not stand in for it: an agent
is `Idle` while a background task runs (`fake.Agent.Idle` ignores tasks, and
`Parkable` is what counts them), so without the state check a session whose
background task outlives the period is archived and its task stopped.

### Evidence

Replaced `if m.state != agent.StateParked || m.deleting != nil` with
`if m.deleting != nil` and ran `go test -race -run TestAutoArchive
./internal/server/`: it passed. The waiting-approval step in `TestAutoArchive`
is caught by `free()` instead, so it does not observe the state check.

### Action

Add a step to `TestAutoArchive`: send a session `background <ms>` with a task
that runs well past `archiveAfter`, wait for its `idle`/`background` state, then
create an unused session and wait for that session's `session/archived`, and
check that the background session received none, as the pinned-session step
does with `c.received`.

## F2. No test counts a setting change as use

Low · Testing · Fix · `internal/server/session.go:507`

The new requirement Archiving idle sessions counts setting changes as use. A
parked session's `session/configure` reaches `oxd` only through `adopt`, which
publishes no update, so `adopt`'s write of `activeAt` is the only thing that
counts it. Removing that write leaves every test passing.

### Evidence

Deleted the three lines in `adopt` that lock `Server.mu` and set `activeAt`, and
ran `go test -race ./internal/server/`: it passed.

### Action

Add a step to `TestAutoArchive`, beside "unarchiving gives a session the full
period": take a parked, unpinned session that has been idle for most of the
period, send `session/configure` with a changed setting at time t, and check
with `archivedAfter` that its archive comes no sooner than t + `archiveAfter`.
Choose the session so that it would otherwise be archived well before then,
for example one created before the configure by most of `archiveAfter`.

## F3. The owner table omits pinning and `activeAt`

Low · Documentation · Fix · `docs/architecture.md:117`

The state-ownership table's Unarchive row says it owns "Archived flags only".
Unarchive now also sets each member's `activeAt`, and `session/pin` and
`session/unpin` work the same way, under `Server.mu` without posting to a
session, writing `pinned` and `activeAt`, but have no row.

### Evidence

`unarchiveLocked` (`internal/server/archive.go:175`) and `setPinnedLocked`
(`internal/server/methods.go:660`) write those fields in the critical section
that broadcasts and replies.

### Action

Make the row cover Unarchive, Pin, and Unpin: they own the archived and pinned
flags and `activeAt` under `Server.mu`, and look the session up, refuse what
they refuse (a child with an archived parent; a child for pin and unpin), set
the flags, and queue the response in one critical section without posting to a
goroutine.

## F4. A pin in flight briefly disables the session's composer

Low · Requirements · Rejected · `cmd/ox/web/src/ox.tsx:119`

`busy` counts every session list request in flight on a session or its
ancestors, and now that includes `pin` and `unpin`. While one is in flight, the
session's and its descendants' Send, Stop, and settings are disabled, and the
descendants' menu items say "Wait until Ox answers", though a pin cannot
conflict with any of them.

### Evidence

`sessionControl.ts:122` gates `free()` on `busy()`, which `ox.tsx` builds from
`listing.sessions` over the session's line.

### Action

Rejected: the window is one round trip of a request that touches no process,
started from the sidebar menu, and it clears itself. A user does not reach Send
or Stop inside it in normal use, and a second busy rule that tells pins apart
costs more than it saves.

## Checks run

- `make test`: passed (Go with the race detector, lint, and the Node suites).
- `go test -race -count=5 -run 'TestAutoArchive|TestArchive$'
  ./internal/server/`: passed all five runs.
- `npm --prefix cmd/ox/web run e2e -- session.spec.ts` for "a pinned session
  lists first, also before oxd answers and after a reload", "a child session's
  menu offers no Pin", and "a menu opened near the bottom edge stays on screen
  as it grows": passed.
- Spot-checked removals, each reverted:
  - `top.pinned` from `familyIdleLocked`: `TestAutoArchive` fails ("the pinned
    session was archived").
  - `activeAt` on unpin in `setPinnedLocked`: `TestAutoArchive` fails (archived
    29ms after its last use).
  - `activeAt` in `adopt`: no test fails (F2).
  - The `parked` state check in `familyIdleLocked`: no test fails (F1).
- Not run here: the full `make e2e` and the headless Chrome check through
  `make dev`; see Gaps. `make live` cannot observe the change.

## Verification 1

Checked the working tree after `/ox-work fix`, on base `a2f1421`. Since the
review, only `internal/server/mcp_test.go` and `docs/architecture.md` changed.

| ID | Status   | Evidence                                                                                                                                                                                                         |
| -- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `TestAutoArchive` step "a session with a background task is kept" fails with the `parked` check removed from `familyIdleLocked` ("the session with a background task was archived") and passes with it.          |
| F2 | Resolved | `TestAutoArchive` step "a setting change gives a session the full period" fails with `adopt`'s `activeAt` write removed (archived 30ms after the configure) and passes with it.                                  |
| F3 | Resolved | The owner table's row is now "Unarchive, Pin, and Unpin", owning the archived and pinned flags and `activeAt` under `Server.mu`, and states each method's refusals.                                               |

No new findings. The F2 step keeps the parent's family busy with a child waiting
on an approval, then deletes the child; a deleted session sends no report, so
the configure is the parent's only use, and the lower-bound timing assertion
cannot fail without a defect.

Checks run: `make test` passed; `go test -race -count=5 -run TestAutoArchive
./internal/server/` passed all five runs; `dprint check` on the docs passed;
both removals above, each reverted; `make e2e` passed (254 passed, 4 live
specs skipped). The look of a pinned row in light and dark mode is left to the
user's view of the session list.

## Verdict

Ready to commit: F1, F2, and F3 are resolved, and F4 is rejected.
