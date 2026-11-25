# Take child reports in the same critical section that checks the family

## Goal

A parent today checks under `Server.mu` that none of its children is running,
releases the lock, then visits each child's goroutine to take its reports. A
child can start after the check, and a sibling can finish and add a report
before the parent visits it, so the parent takes a report while a child is
running. That breaks **Child reports** and can split one group of reports into
separate parent turns.

After this change, a child's untaken reports are guarded by `Server.mu`, the
lock that already guards every child's published state. The parent reads its
children's states and takes their reports in one critical section, so every
report it takes existed at a moment when none of its children was running.

Resolves [#85](https://github.com/kkestell/ox/issues/85), from
[audit finding F2](../audits/2026-11-24-structural-audit-2026-11-24.md#f2-a-child-report-can-reach-its-parent-while-a-sibling-is-still-running).

## Related code

- `internal/server/session.go` — `session.state` (guarded by `Server.mu`,
  written by `update`); `toParent` and `turn`; `report`, which appends a report
  on the child's goroutine; `pingFamily`; `deliverReports`, which checks
  `children` and their `state` under `Server.mu`, then calls `ask` on each child
  to drain `toParent.reports`.
- `internal/server/mcp.go` `sendMessage` — reads `caller.toParent.turn` through
  `ask` on the child's goroutine; `turn` and `told` stay there.
- `internal/server/server.go` `children` — family membership, read under
  `Server.mu`.
- `internal/server/mcp_test.go` `TestChildReports` and `reportsTaken` — the
  existing report cases and the helper that waits for a parent's reports.
- `docs/architecture.md` — "State ownership and locking" and "Child sessions
  and reports".

## Decisions

- **Owner of the queue.** `toParent.reports` is guarded by `Server.mu`.
  `toParent.turn` stays owned by the child's `run`. The `toParent` pointer is
  set in `newSession` and never changes, so it moves to the "Fixed at creation"
  group, and the parent may follow it under the lock. The `toParent` comment
  names each field's guard. Keeping the queue on the child, rather than moving
  it to the parent, keeps each child's reports in order, keeps the delivery
  order (children oldest first, each child's reports oldest first), and keeps
  a deleted child's untaken reports dropped with it.
- **Report.** `report` appends to `toParent.reports` while holding
  `Server.mu`. The child's goroutine still decides whether a turn reports and
  builds the text, and still moves `told`.
- **Delivery.** `deliverReports` still returns first unless the parent is
  `free`. Then, in one critical section under `Server.mu`, it returns unless
  the server is open, the parent is not archived, and no child's `state` is
  `running`; otherwise it sorts the children by `createdAt` and takes and
  clears each child's `toParent.reports`. It releases the lock before
  `acceptMessage`. The `ask` calls in `deliverReports` go; `ask` stays for
  `sendMessage`.
- **Requirements.** **Child reports** now holds in every interleaving: a report
  is taken only together with a reading of every child's state that shows none
  running, so a child that starts after a check either holds the report (it
  started before the take) or the take preceded the report. Reports a child
  makes while its own published state is still `running` (a `result` precedes
  the harness's non-running `state`) stay until the next non-running `state`
  or `parked` ping, as today. **Message delivery** and **Only the user sends**
  hold unchanged: a free parent still has no untaken input, so messages go
  first, and delivery is still one input through `acceptMessage`.
- **Settling.** The change removes waits: `deliverReports` no longer waits on
  any child's goroutine. Archive's hold is the only post from one session
  goroutine to another that waits.
- **Construction.** Invariant: every report a parent takes existed at a moment
  when none of its children was running. `Server.mu` guards both the published
  `state` and `toParent.reports`; each child writes both only on its own
  goroutine under the lock, and the parent reads states and drains queues
  inside one critical section. Only a child has a `toParent`, so only a child
  holds reports.
- **Ownership.** No new state. `toParent.reports` moves from the child's
  goroutine to `Server.mu`. User-visible behavior: a parent receives its
  children's reports together, only once none of them is working. Races: the
  window between the family check and the drains is gone; a child that starts
  after the take races an independent request, and both outcomes are valid.
  Cleanup: unchanged; the parent takes the reports, and they are dropped when
  the child is deleted or `oxd` stops. `Server.mu` is still never held while
  handing input to an agent. The commit body repeats this.
- **Claude Code.** No new assumption. Delivery still relies on a turn's
  `result` reaching the session before its non-running `state`, as the current
  delivery does.

## Check your assumptions

None.

## Test plan

- Server, a new last case in `TestChildReports` (`internal/server/mcp_test.go`):
  "a child that starts after the parent's check holds a sibling's later
  report". It is last so the earlier history count stays 9.
  1. Reattach `fromP` to the restarted server and create child `three`
     (title `Three`); wait for its first report with `reportsTaken`.
  2. Hold child `one`'s goroutine: look it up in `s.sessions` under `s.mu`,
     then `go` post it an operation that closes `entered` and waits on a
     `hold` channel, released by a `sync.OnceFunc` also registered with
     `t.Cleanup`. Wait on `entered`. `one` is the oldest child, so the current
     `deliverReports` visits it first and blocks there after its family
     check.
  3. Send the parent `short` and wait for its `idle` state, which pings the
     parent to deliver while no child runs and none has reports.
  4. Send `three` `wait 60000` and wait for its `prompt`; send `two` `short`
     and wait for its result `You said: short` and its `idle` state.
  5. Release `one`, interrupt `three`, and call `reportsTaken` for
     `{two: 1, three: 1}`. Assert exactly one reports prompt, equal to
     `[{two, "Two", "You said: short"}, {three, "Three", "Turn failed: Interrupted"}]`.

  On the current code the parent takes `two`'s report alone while `three`
  runs, so the case sees two prompts. On the new code the parent never waits
  on `one`, and the assertion holds in every order. The held goroutine is the
  injected fault from the audit's probe, through an operation posted to the
  session.
- The existing `TestChildReports` cases cover report order, batching, the
  `requires_action` rule, `told`, messages first, and history; they pass
  unchanged.

## Implementation plan

1. `internal/server/session.go`: move `toParent` to the fixed fields and
   document its fields' guards; lock `Server.mu` around the append in
   `report`; rewrite `deliverReports` to check and take in one critical
   section and drop its `ask` calls; update its comment, which no longer
   describes waits on children.
2. `internal/server/mcp_test.go`: the new `TestChildReports` case.
3. `docs/architecture.md`: the updates below; `make docs`.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, "State ownership and locking": `Server.mu` owns
  children's untaken reports alongside membership; the session goroutine row
  drops "child reports" and keeps the turn's `told` state; the diagram's
  synchronous post is the archive hold alone.
- `docs/architecture.md`, "Child sessions and reports": each child keeps its
  turn's `told` state on its goroutine and appends its reports under
  `Server.mu`; delivery reads the children's published states and takes their
  reports in one critical section, then hands them to the parent's agent after
  releasing the lock.
