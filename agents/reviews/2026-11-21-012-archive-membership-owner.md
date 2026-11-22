# Review: archive on the session's goroutine, unarchive under Server.mu

## Scope and coverage

Reviewed the uncommitted changes on `fix-f4` at base `27efb42` against
[the plan](../plans/2026-11-21-012-archive-membership-owner.md) for
[Deslop F4](../audits/2026-11-20-002-deslop.md#f4-archive-and-unarchive-act-outside-the-owner-of-session-membership).
Changed files: `internal/server/session.go`, `internal/server/methods.go`,
`internal/server/mcp_test.go`, `docs/architecture.md`, `docs/protocol.md`,
`docs/todo.md`. The settled product decision: a successful archive reply means
every member has stopped.

Traced `archive`, `hold`, `unarchiveLocked`, `setArchivedLocked`, `onSession`,
`Server.Close`, and every `post` site in `internal/server` for a session
goroutine that posts to another. Only `archive` does, and only from an
ancestor to its descendants, so the plan's deadlock argument holds. `Close`
interleaved with an archive releases every held member: a member that ends
first fails its `hold` and is skipped, and `setArchived` refuses with
`daemon is shutting down`, so every `done` the archive waits on closes.

The implementer's two deviations:

- `unarchiveLocked` has no `closed` check of its own; see F1.
- The step that archives the parent no longer calls `c.update(id, "parked")`.
  The predicate on the response already asserts a `parked` update for `a`,
  `k`, and `g`, and no later step reads `parked` for them, so the calls would
  only repeat that assertion. Accepted.

Skipping `make live` and the headless Chrome check is correct: `stop` calls
`agent.Stop` as before, and the page's requests and the protocol's messages
are unchanged; only the reply's position relative to `parked` moves.

`docs/functional-requirements.md` is unchanged, as the plan's Requirement
changes ("None") require.

Gaps: none.

## Summary

| ID | Severity | Category | Action   | Status | Title                                                         |
| -- | -------- | -------- | -------- | ------ | ------------------------------------------------------------- |
| F1 | Low      | Intent   | Rejected | —      | Unarchive during shutdown can answer with another error first |

## F1. Unarchive during shutdown can answer with another error first

Low · Intent · Rejected · `internal/server/session.go:289`

The plan's Decisions say `unarchiveLocked` returns `daemon is shutting down`
when `closed`. It checks the ID and the parent first and leaves `closed` to
`setArchivedLocked`. `Close` keeps every session in `s.sessions`, so during
shutdown an unknown ID answers `-32001` `session not found` and a child of an
archived parent answers `-32000` `parent session is archived`, where the old
handler answered `daemon is shutting down`.

### Evidence

Traced from the source: `Server.Close` (`server.go:140-155`) sets `closed` and
posts `end` without touching membership; `unarchiveLocked` returns before
`setArchivedLocked` on either refusal.

### Action

Rejected. Every outcome refuses the request and changes nothing, and the
protocol fixes no order between refusals for session methods. Keeping the
`closed` rule only in `setArchivedLocked`, the one place that changes the
flags, is the simpler design.

## Checks run

- `make test`: passed.
- `make e2e`: 42 passed, 3 skipped.
- `go test -race -count=20 -run TestArchive ./internal/server/`: passed.
- The new `TestArchive` against the base revision's code: fails with
  `the archive replied before … parked; parked map[]`.
- The wait on each `held.done` removed: `TestArchive` fails three times out of
  three with `the archive replied before … parked`, `parked` holding only the
  target.
- `!sess.free()` removed from `archive`'s busy check: `TestArchive` fails at
  the configuration step with `got <nil>, not the refusal -32002`.
- `make live` and the headless Chrome check: not required, as above.

## Verdict

Ready to commit.
