# Keep session reports through a refused delete

## Goal

A delete refused for a dirty worktree loses session reports
([#187](https://github.com/kkestell/ox/issues/187), from
[audit F1](../audits/2026-11-27-main.md#f1-deleting-state-lives-outside-the-server-lock)).
Whether a session is being deleted lives on the session goroutine, where
`readyLocked` cannot see it, so `destroy` stands in for it by clearing
`sess.reporting` and stops the session before it marks the delete. Two losses
follow:

1. A creator whose delete is refused loses the reports waiting for it: its
   stop publishes `parked`, `readyLocked` schedules a delivery, the delivery
   drains the reporters' queues, and `start` then refuses with `errDeleting`.
2. A started session whose delete is refused loses the report it held, because
   `destroy` set `reporting` to nil and `finishDelete` replaced it with a blank
   one.

When the change is done, a session whose delete is refused keeps every report
it held and every report waiting for it, and they are delivered as if no
delete had been tried.

## Related code

- `internal/server/session.go` — `session.reporting` and `session.deleting`;
  `destroy` and `finishDelete`; `update` and `report`; `reportersLocked`,
  `readyLocked`, `familyChangedLocked`, `deliverReports`, `takeReports`;
  `start`, which refuses while deleting.
- `internal/server/mcp.go` — `checkChildLocked` reads `parent.deleting` under
  `Server.mu`, relying on running on the parent's goroutine.
- `internal/server/mcp_test.go` — `TestChildReports` (its "deleting a running
  child sends its parent no report, and releases its sibling's" step guards the
  existing delete behavior), `reportsTaken`, `TestCreateTopLevelSession`.
- `internal/server/worktree_test.go` — `gitRepo`, `worktreeServer`,
  `worktreeSession`, `addWorkspace`, `TestWorktreeDelete` (dirty-worktree
  refusal).

## Decisions

- **Construction.** `deleting` joins the facts `Server.mu` guards, beside
  `archived` and `state`. Only the session's goroutine writes it, always under
  `Server.mu`, so the goroutine reads it without the lock and every other
  reader holds the lock. `readyLocked` reads it directly:
  - A recipient being deleted is not ready: `readyLocked` returns false when
    `sess.deleting != nil`, beside `s.closed || sess.archived`. Because
    `takeReports` checks `readyLocked` in the same critical section as the take,
    a delivery posted before the delete started takes nothing.
  - A reporter being deleted is not a reporter: `reportersLocked` skips a
    session whose `deleting` is set. Its held reports stay in its queue,
    untaken, and its state holds back no sibling's reports.
- **`reporting` is fixed at creation.** `newSession` sets it exactly when
  `reportsTo() != ""`, and nothing replaces or clears it afterward. `destroy`
  no longer sets it to nil and `finishDelete` no longer allocates a new one.
  `reportersLocked` selects by `reportsTo() == sess.id && deleting == nil`; the
  `reporting != nil` test there goes.
- **A session being deleted makes no report.** `destroy` sets `deleting` before
  it stops the agent, and the stop's `exited` update reaches `report`, which
  still resets the turn's step and told IDs but keeps no report while
  `sess.deleting != nil`. The check sits beside `recipient == nil` under the
  lock. This keeps the current behavior that deleting a running session sends
  its recipient no report, whether or not the delete then succeeds.
- **Order in `destroy`.** Under `Server.mu`: refuse a session with children,
  set `sess.deleting`, and call `familyChangedLocked` on its recipient, since
  its reports and state no longer count. Then stop the agent and start the
  delete goroutine as today.
- **`finishDelete`.** On failure, under `Server.mu`: clear `sess.deleting`,
  then call `familyChangedLocked` on the session's recipient (its held reports
  count again) and on the session itself (reports waiting for it can be
  delivered). Then reply with the error. A parked creator that is ready then
  starts and takes its reports, as any parked recipient does. On success,
  `deleting` stays set while the session leaves membership in the existing
  critical section; nothing else needs to be told, because the recipient has
  already been excluded from this session's reports since the delete started.
- **Ownership.** No new state, timer, or lock. `deleting` moves from
  goroutine-only to goroutine-written under `Server.mu`. User-visible behavior
  that needs it: reports survive a refused delete. Races it removes: a delivery
  scheduled by the stop's `parked` update, and a reporter finishing while the
  worktree is being removed, both now see the delete under the lock that
  decides delivery. Cleanup: cleared under the lock on failure; dropped with
  the session on success.
- **Requirements.** *Session reports* already says undelivered reports are
  dropped when the session is deleted; a refused delete deletes nothing, so
  keeping them is that requirement's intended behavior. *Deleting a session*
  is unchanged: the refusal still names the dirty worktree and leaves the
  session parked and listed. A second page deleting the same session still
  gets `session is being deleted`.
- **Shared code with #188.** The peer fix for
  [#188](https://github.com/kkestell/ox/issues/188) (worktree
  `top-level-session-rollback`) works in `createTopLevelSession`,
  `startedRecord`, and `createTopLevel`/`addSession`, and its audit finding
  notes no creation check refuses a caller that is being deleted. If it adds
  that check, it can read `caller.deleting` under `Server.mu` once this change
  lands, as `checkChildLocked` does. Both changes touch `session.go` only where
  `newSession` sets `reporting`; this change leaves that line's behavior as it
  is.

## Check your assumptions

None.

## Test plan

New test `TestRefusedDeleteKeepsReports` in `internal/server/mcp_test.go`.
It needs a creator in its own git worktree and started sessions in worktrees it
can dirty; `TestChildReports` runs without git and
`TestCreateTopLevelSession`'s creator has no worktree. Setup: `gitRepo`,
`worktreeServer`, `connect`, `addWorkspace`, and a creator from
`worktreeSession`, sent a first prompt so it is idle, with `attach` for its
tool calls. Lift `reportPrompts` out of `TestChildReports` to a file-level
helper so both tests share it.

1. **Refused creator delete keeps the reports waiting for it.** Start a
   session with `create_top_level_session` (`worktree: false`) and take its
   first report with `reportsTaken`. Send the creator `wait 60000` and wait
   for its prompt. Send the started session `short` and wait for it to be
   idle, so its report waits. Write an untracked file in the creator's
   worktree; `session/destroy` on the creator returns
   `worktree has uncommitted changes`. Then `reportsTaken` on the creator takes
   one report from the started session, with text `You said: short`. Before
   the change, no report arrives and the test times out in `c.next`.
2. **Refused started-session delete keeps its held report.** From the same
   creator, start `One` and `Two` in worktrees (default) and take their first
   reports. Send `Two` `wait 60000` and wait for its prompt. Send `One` `short`
   and wait for it to be idle, so its report waits behind `Two`. Write an
   untracked file in `One`'s worktree; `session/destroy` on `One` returns
   `worktree has uncommitted changes`. Interrupt `Two`. The creator takes one
   prompt with both reports, oldest first:
   `{One, "One", "You said: short"}` then
   `{Two, "Two", "Turn failed: Interrupted"}`. Before the change, only `Two`'s
   report arrives.

`TestChildReports`' existing delete step keeps guarding that deleting a running
reporter sends no report and releases its sibling's reports. `make test` runs
both.

## Implementation plan

1. `internal/server/session.go`:
   - Move `deleting` into the group guarded by `Server.mu`, with a comment
     that only `run` writes it, under the lock, so `run` reads it without the
     lock.
   - Move `reporting` out of the guarded group: the pointer is fixed at
     creation, non-nil exactly for a session that reports to another; its
     `reports` stay guarded by `Server.mu` and the rest owned by `run`, as the
     `reporting` type's comments say.
   - `destroy`: replace the `reporting = nil` block with setting
     `sess.deleting` and calling `familyChangedLocked(s.reportsToLocked(sess))`
     under the lock, before `stop`. Update its doc comment.
   - `finishDelete`: on failure, clear `deleting` under the lock and call
     `familyChangedLocked` on the recipient and on `sess`; drop the
     `newReporting()` block. Update its doc comment.
   - `report`: keep no report while `sess.deleting != nil`, after resetting the
     turn state.
   - `reportersLocked`: select `reportsTo() == sess.id && deleting == nil`.
   - `readyLocked`: return false when `sess.deleting != nil`; mention it in the
     doc comment's list of facts.
2. `internal/server/mcp.go`: drop the goroutine-only comment in
   `checkChildLocked`; the read is under `Server.mu`.
3. `internal/server/mcp_test.go`: lift `reportPrompts` to file level and add
   `TestRefusedDeleteKeepsReports`. Confirm both cases fail on the code before
   the change.
4. Docs, as below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`: the ownership table moves `deleting` to the
  `Server.mu` row (written only by the session goroutine); *Turn reports* says
  `reporting` is kept from creation and that a session being deleted is no
  reporter, takes no reports, and keeps no report; the *Delivery* bullet lists
  a delete starting or failing among the writers that call
  `familyChangedLocked`; the deletion flowchart sets `deleting` before stopping
  the agent, and its failure step clears `deleting` and delivers held reports.
- `docs/protocol.md` (session reports, around "A session being deleted stops
  reporting"): while a session is being deleted it makes no reports, its
  untaken reports wait and its state holds back no other session's, and
  reports to it wait; a delete that succeeds drops its untaken reports, and one
  that fails keeps both for the next delivery. The trigger list adds a
  reporting session's failed delete.
