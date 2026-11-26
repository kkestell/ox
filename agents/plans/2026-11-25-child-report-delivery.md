# Deliver child reports whenever the family's facts change

## Goal

A parent receives its children's reports only when some child's update happens
to trigger delivery, and a child's reports are made as a side effect of
whichever update arrives. So a deleted running child wakes its parent with
`Exited: stopped`, a report kept through an archive waits after unarchive, a
child's failed start splits its siblings' reports into two messages, and a
`send_message` whose turn ended before the handler read it suppresses the next
turn's report.

After this change, every change to a fact that decides delivery goes through one
step that checks the affected parent under `Server.mu` and wakes it when its
family is ready. A child being deleted leaves the family when its delete starts.
An ending harness publishes no state between its last state and `parked`, so no
child looks finished without its exit report. A `send_message` to the parent is
bound to its own `tool_use` by the ID Claude sends with the call, on the child's
goroutine.

Resolves [#127](https://github.com/kkestell/ox/issues/127), from
[structural audit F1](../audits/2026-11-25-structural-audit-2026-11-25.md#f1-child-report-delivery-is-triggered-by-updates-not-derived-from-family-state),
and [#110](https://github.com/kkestell/ox/issues/110), from
[sweep F9](../audits/2026-11-25-ox-sweep.md#f9-unarchiving-leaves-a-childs-report-undelivered),
[F10](../audits/2026-11-25-ox-sweep.md#f10-a-childs-failed-start-splits-its-siblings-reports),
and
[F11](../audits/2026-11-25-ox-sweep.md#f11-a-late-send_message-capture-suppresses-the-next-turns-report).

## Related code

- `internal/server/session.go` — `toParent`, `turn`, `told`; `update`, which
  sets the published `state` under `Server.mu` and calls `report` and
  `pingFamily`; `report`; `pingFamily`; `deliverReports`; `takeReports`;
  `destroy`, which stops the agent before it sets `deleting`; `finishDelete`;
  `unarchiveLocked` and `setArchivedLocked`.
- `internal/server/mcp.go` — `handleMCP` (`tools/call` decoding), `tool.call`,
  and `sendMessage`, which reads `caller.toParent.turn` through `ask` after the
  call arrives.
- `internal/agent/tracker.go` — `DropFrom`, which publishes the state change
  dropping inputs causes.
- `internal/claude/agent.go` `end`, `internal/fake/fake.go` `Stop`,
  `internal/ur/ur.go` `Stop` — each ending path drops its inputs with
  `DropFrom(0)`, publishing `idle` before the exit.
- `internal/server/mcp_test.go` `TestChildReports`, `sendGateAgent`,
  `reportsTaken`; `internal/server/client_test.go` `client.tool`.
- `internal/claude/claude_test.go` `startLog.dropped`;
  `internal/claude/prompt_test.go` and `configure_test.go`, the
  `cancelledToIdle` checks of ending paths; `internal/agent/tracker_test.go`.

## Decisions

- **One step for every fact change.** Delivery reads facts `Server.mu` guards:
  each child's published `state` and untaken reports, membership in the family,
  the parent's published `state` and `archived` flag, and `closed`. A new
  `readyLocked(parent)` decides delivery in one place: not `closed`, the parent
  not archived and its published state `idle`, `background`, or `parked`, no
  child in the family `running`, and some child in the family holding a report.
  A new `familyChangedLocked(parent)` wakes the parent when `readyLocked` holds,
  by `go parent.post(parent.deliverReports)`. Every writer of those facts calls
  it while still holding the lock: `update`, as it sets a session's published
  state, for the session's parent and for the session itself; `report`, as it
  appends; `setArchivedLocked`, for each member; and `destroy`, as the child
  leaves the family. `pingFamily` goes. `deliverReports` runs on the parent's
  goroutine and takes the batch in `takeReports` under the lock only when
  `readyLocked` still holds, so the take stays serialized with the parent's own
  archive, prompts, and starts. On that goroutine the published state is the
  parent's current state, so `deliverReports` drops its separate `free` check.
- **Membership in the family.** A child is in its family while its `toParent`
  is set. `toParent` becomes a pointer guarded by `Server.mu` and written only
  by the child's `run`, so `run` reads it without the lock. `destroy` of a child
  clears it under the lock, after the children check and before the stop, and
  calls `familyChangedLocked` for the parent. The stop's `exited` then makes no
  report, the child's untaken reports are dropped, and its state no longer holds
  back its siblings' reports. `readyLocked` and `takeReports` count only
  children whose `toParent` is set. A delete that fails in `finishDelete` gives
  the child a fresh `toParent`, so it reports again.
- **An ending harness publishes no state.** A new `Tracker.End` drops every
  untaken input with `prompt_cancelled` and publishes no state change, for a
  harness that has ended; the session's `parked` update follows on the same
  goroutine. Claude's `end`, the fake's `Stop`, and Ur's `Stop` call it instead
  of `DropFrom(0)`. A child's published state therefore goes from its last state
  to `parked`, and its exit report is appended before `parked` makes it count as
  finished. `DropFrom` keeps publishing for interrupts and Claude's failed
  writes, where the harness goes on.
- **`send_message` bound to its `tool_use`.** Claude Code sends each MCP
  `tools/call` with `_meta["claudecode/toolUseId"]`, the ID of the call's
  `tool_use` (observed live on 2.1.331; recorded in the harness doc below).
  `handleMCP` decodes it and passes it to every tool's `call` as `toolUseID`.
  When the parent accepts a child's message and the call has an ID,
  `sendMessage` posts to the child, which records the ID as told and answers the
  call from there, so the mark precedes the call's `tool_result` as before.
  `toParent` replaces `turn` and the `told` states with, owned by the child's
  `run`: `step`, the `tool_use` IDs of the turn's latest step; `answered`, set
  by a `tool_result` so the next `tool_use` begins a new step; and `told`, the
  IDs of calls the parent accepted. At a `result`, the child makes no report
  when an ID in `step` is in `told`; a `result` or `exited` clears all three.
  The handler no longer reads the child's turn, so a call accepted after its
  turn ended matches no later step and suppresses nothing. A call without the
  ID marks nothing, and the turn's report is sent.
- **Requirements.** **Child reports**: reports arrive in the cases that now
  strand or split them, and none comes from a deleted child, as
  `docs/protocol.md` ("Child reports") already says. A parked parent still
  starts for its reports; a parent archived before its delivery runs keeps them
  until unarchive, which now delivers them. **Message delivery**, **Archiving a
  session**, and **Deleting a session** keep their behavior. A page sees an ending
  session's state go straight to `parked`, with the same `exited` text and
  dropped prompts.
- **Settling.** No new wait. A wake is an asynchronous post that ends when the
  parent's goroutine runs it or the session has ended. `sendMessage`'s post to
  the child answers the call when the child has ended, as today.
- **Construction.** One function decides readiness for both the wake and the
  take. Each fact it reads is written only by code that calls
  `familyChangedLocked` in the same critical section. A child outside the family
  has no `toParent`, so no reader can count its state or reports.
- **Ownership.** No new lock, timer, or stored field. `toParent`'s pointer moves
  from fixed at creation to guarded by `Server.mu`, with `run` its only writer;
  its new fields `step`, `answered`, and `told` are owned by `run` and cleared
  at each `result` and `exited`. A late acceptance's ID stays in `told` until
  the next `result` or `exited`.
- **Claude Code.** Interrupt ("Interrupt"): an interrupted `send_message`'s
  `tool_result` arrives with `is_error: true` before the `result`, so a call the
  parent accepted before the result still counts in that step. Failed starts
  ("Startup failures") reach `end` through every recorded variant, and `end`
  drops the held inputs without a state change in each. The `_meta` ID is the
  one new assumption, settled by the live check above.

## Check your assumptions

None.

## Test plan

- Deleting a running child sends its parent no report and releases a sibling's
  waiting report alone — server, new case in `TestChildReports`: a sibling
  finishes while the child runs `wait 60000`; `session/destroy` on the child;
  `reportsTaken` wants only the sibling's report. Fails today on the deleted
  child's `Exited: stopped`.
- Unarchive delivers a report kept through an archive — server, new case in
  `TestChildReports`: an operation posted to the parent's goroutine waits until
  the child's `idle` arrives, then runs the parent's `archive`, so the wake's
  delivery runs after it and takes nothing; `session/unarchive` on the parent;
  `reportsTaken` wants the child's report. Hangs today.
- An ending agent drops its inputs without a state change, before `parked` —
  Claude harness: `startLog.dropped` and the `cancelledToIdle` checks of ending
  paths (`Stop`, a failed start, `processExit`) in `prompt_test.go` and
  `configure_test.go` expect `cancelled` alone; interrupt and failed-write
  checks keep `cancelledToIdle`. `TestTrackerEnd` in `tracker_test.go` for
  `Tracker.End`. These fail today on the `idle` state.
- A `send_message` marks only the step holding its `tool_use` — server,
  `TestChildReports`: the told case sends the call during `approve Bash {}` with
  `_meta` holding that `tool_use`'s ID, through a `client.tool` that takes the
  ID. The gated late case is replaced by a call during the child's second
  `approve Bash {}` turn carrying the first turn's `tool_use` ID; both turns'
  reports arrive. Fails today, since the handler marks the turn in progress.
  `sendGateAgent` stays for `TestChildStartHoldsUpNoOtherSession`.
- `make e2e` runs, since the page sees an ending session skip `idle`.

## Implementation plan

1. `internal/agent/tracker.go`: add `End`; add `TestTrackerEnd`.
2. `internal/claude/agent.go` `end`, `internal/fake/fake.go` `Stop`,
   `internal/ur/ur.go` `Stop`: call `tracker.End()` in place of `DropFrom(0)`.
   Update the ending-path checks in `internal/claude` tests.
3. `internal/server/session.go`: replace `turn` and `told` with `toParent`'s
   `step`, `answered`, and `told`; rewrite `report` for them and have it call
   `familyChangedLocked` as it appends; add `readyLocked` and
   `familyChangedLocked`; call it from `update` where the published state is set
   and from `setArchivedLocked`; remove `pingFamily`; make `deliverReports` and
   `takeReports` use `readyLocked` and skip children without `toParent`; add the
   `told(toolUseID)` operation; in `destroy`, clear a child's `toParent` and
   wake its parent before the stop; in `finishDelete`, restore it on failure.
   Update the comments on `session`, `toParent`, and `update`.
4. `internal/server/mcp.go`: decode `_meta["claudecode/toolUseId"]` in
   `tools/call` and pass it to `tool.call`; drop the turn capture from
   `sendMessage` and post `told` with the ID once the parent accepts.
5. `internal/server/client_test.go` and `mcp_test.go`: give `client.tool` the
   ID, and add or replace the `TestChildReports` cases above.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md` ("Child reports"): the step is matched by the call's own
  `tool_use`, through the ID Claude sends with the MCP call; delivery is tried
  whenever a fact it depends on changes, including unarchive and a child's
  delete; a child's reports are dropped when its delete starts.
- `docs/architecture.md` ("Child sessions and reports", and `Server.mu` in the
  locking table): the readiness step and its callers, membership through
  `toParent`, and the `step` and `told` IDs in place of the `told` state
  machine and its diagram.
- `docs/harnesses/claude-code.md`: MCP `tools/call` carries
  `_meta["claudecode/toolUseId"]`, the ID of the call's `tool_use` (2.1.331).
