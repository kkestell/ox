# Review: deliver child reports whenever the family's facts change

## Scope and coverage

Reviewed the uncommitted changes in the `child-report-delivery` worktree
against base `74e5cbe` (`git merge-base origin/main HEAD`, also `HEAD`), with
the plan `agents/plans/2026-11-25-child-report-delivery.md`, which resolves
#127 and #110. The working tree held the 15 modified files `/ox-work` listed
and the untracked plan.

Read the plan, the open `bug` issues, the **Child reports** and **Message
delivery** requirements, `docs/protocol.md` ("Child reports", `session/send`,
`session/interrupt`, session updates), `docs/architecture.md` ("Child sessions
and reports", the locking table), and the harness doc's MCP and interrupt
sections. Traced every writer of the facts `readyLocked` reads (published
state, reports, membership through `toParent`, `archived`, `closed`), every
caller of Claude's `end`, the fake's and Ur's `Stop`, and the page's handling
of `prompt_cancelled` and `parked`.

Gaps:

- The new tests' failures on the base tree were taken from `/ox-work`'s Tests
  proved report and not re-run.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                     |
| -- | -------- | ------------- | -------- | ------ | --------------------------------------------------------- |
| F1 | Low      | Documentation | Rejected | —      | An ending session's release carries no message ID         |
| F2 | Low      | Simplicity    | Rejected | —      | Every state change scans all sessions for a family check  |

## F1. An ending session's release carries no message ID

Low · Documentation · Rejected · `docs/protocol.md:587`

`docs/protocol.md` says the `state` or `exited` update that releases the
session names the latest prompt Claude took or dropped. A Claude agent that
ends while configuring or timed out, with held prompts and no exit reason, used
to publish an `idle` state naming the last dropped prompt. It now publishes
only the `prompt_cancelled` updates and then `parked`, which names no prompt.

### Evidence

`end` in `internal/claude/agent.go` names no input in the `*configuring` and
`timedOut` phases and calls `Tracker.End`, which publishes no state. The page
releases a prompt on its own `prompt` or `prompt_cancelled`
(`cmd/ox/web/src/ox.tsx:717`), not on a state update.

### Action

Rejected: the sentence lists which updates carry `message_id`, and every
`state` or `exited` update the change still publishes follows it. No reader of
the release depends on the removed `idle`, and `make e2e` passed.

## F2. Every state change scans all sessions for a family check

Low · Simplicity · Rejected · `internal/server/session.go:619`

`update` calls `familyChangedLocked` for the session and its parent on every
`state` and `parked` update, and `readyLocked` calls `familyLocked`, which
walks `s.sessions` through `children` and sorts the result, under `Server.mu`.

### Evidence

`Server.children` in `internal/server/server.go:210` iterates every session.

### Action

Rejected: an unmeasured scaling cost. Session counts are small, and the one
check in one place is what makes delivery correct by construction.

## Checks run

- `go test -race -count=10 -run 'TestChildReports|TestTrackerEnd|TestChildStartHoldsUpNoOtherSession' ./internal/server ./internal/agent`:
  passed all ten runs, so the new delete, unarchive, and ended-`tool_use` cases
  are stable.
- `make test`, `make e2e`, and `make live` (including `TestLive/child_reports`)
  passed per `/ox-work`'s final response; these are the checks `AGENTS.md`
  requires, since the change alters how Ox drives Claude Code and the states
  the page sees.
- Traced that each issue resolves:
  - #127 and sweep F9: `destroy` clears the child's `toParent` under the lock
    before the stop, so its `exited` makes no report and it leaves the family;
    `setArchivedLocked` wakes each member, so unarchive delivers.
  - Sweep F10: `Tracker.End` publishes no `idle` between a child's last state
    and `parked`, and the exit report is appended before `parked` makes the
    child count as finished.
  - Sweep F11: `told` keys on the call's own `tool_use` ID from
    `_meta["claudecode/toolUseId"]`, so a late acceptance matches no later
    step.
- Confirmed every caller of Claude's `end` runs from `Handle` or `Stop`, so
  `parked` follows on the same operation, and Ur sends no MCP calls, so its
  children lose nothing without the `_meta` ID.

## Verdict

Ready to commit. No `Fix` or `Record` findings; F1 and F2 are rejected.
