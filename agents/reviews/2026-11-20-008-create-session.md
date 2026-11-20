# Review: Claude starts a child session

## Scope and coverage

The uncommitted changes on base `f165921` (20 modified files and the plan,
nothing staged), against `agents/plans/2026-11-20-008-create-session.md`, its
settled Check your assumptions entries, and its Requirement changes, which
the user approved as written. Reviewed the store schema and transaction, the
`create_session` tool and settings resolution, `session.destroy` and its
callers, `label`, `workspace/list` and `session/created`, the web client's
sidebar, header, and label, the tests, and the docs.

The implementer's deviations:

- Titles are trimmed before the length check and stored trimmed. The plan
  checks the trimmed title; storing what was checked keeps the label equal to
  the value that passed. Accepted.
- The guard for a parent missing from its workspace during creation is gone.
  The caller's own goroutine runs the operation, the only path that removes a
  session from membership is `destroy` on that same goroutine, and a
  workspace with sessions cannot be removed, so `workspaceOf` cannot return
  nil there. Accepted.
- `workspace/list` stays in creation order and the page groups children
  beneath their parents. A child is always created after its parent, and the
  page's grouping is what **Session order** describes. Accepted.
- The headless Chrome check ran built binaries on a temporary state directory
  and port instead of `make dev`. `make dev` serves the user's own state and
  socket on a fixed port. The change adds fields to a reply and a
  notification but not how the page reaches `oxd`, and `make e2e` exercises
  the new sidebar, header, and reload in Chrome against the real relay and
  `oxd`. Accepted.

Gaps:

- None.

## Summary

| ID | Severity | Category  | Action | Status | Title                                                  |
| -- | -------- | --------- | ------ | ------ | ------------------------------------------------------ |
| F1 | Low      | Ownership | Fix    | Resolved | The tool's result reads the child's settings off its goroutine |

## F1. The tool's result reads the child's settings off its goroutine

Low · Ownership · Fix · Resolved · `internal/server/mcp.go:415`

`createSession` builds its answer on the parent's goroutine from
`created.settings`, a field the child's goroutine owns, after `addSession`
has started that goroutine. `AGENTS.md` says a session's state is reached
only by posting to its goroutine. No race follows today, since the read is
under `s.mu` and every op that could change the child's settings is looked
up under `s.mu` first, but the safety rests on that ordering instead of on
ownership, and the value read is the one the parent just resolved anyway.

### Evidence

Traced: `addSession` → `newSession` starts `run`; the answer then reads
`created.settings` on the parent's goroutine. `record` holds the same values,
resolved by `childRecord` on the parent's goroutine.

### Action

Build the answer's settings from `record` (`agent.Settings{Model:
record.Model, Effort: record.Effort, PermissionMode: record.PermissionMode}`)
instead of `created.settings`. No behavior changes; `TestCreateSession`'s
first result assertion already covers the answer.

## Checks run

- `make test`: passed, run by this review.
- `make e2e`: 36 passed, including "a child session shows beneath its parent
  with its title", run by this review.
- `make live`: passed in the implementer's run, with the new child case. One
  earlier run failed in the existing "an interrupt ends a generating turn"
  case, which runs before any code this change adds, and passed in the two
  other runs. This review's own run was stopped at the user's request.
- Headless Chrome against built binaries with the fake harness, a temporary
  state directory, and port 18093, in the implementer's run: the child
  indented beneath its parent, only the parent without Delete, the header
  showing the child's directory, the parent's message in its transcript, all
  of it after a reload, the parent's Delete back once the child was deleted,
  and a duplicate name refused.
- Removed conditions, one at a time, each restored: the refusal of a child
  caller fails `TestCreateSession`; the shutdown check fails
  `TestCreateSessionDuringShutdown`; `destroy`'s children check fails
  `TestCreateSession` ("destroying a parent returned"); the rollback's
  `destroy` fails `TestCreateSession`.

## Verification 1

The uncommitted tree on base `f165921` after `/ox-work fix`; the only change since the review is the answer in `createSession` (`internal/server/mcp.go:415`), now built from `record`. `make test` passed, run by this review. `make e2e` passed (36), from the implementer's report. `make live` and the headless Chrome check are unaffected by a change that touches neither how Ox drives Claude Code nor the page, and passed on the full change earlier.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The answer takes its ID, harness, and settings from `record`, resolved on the parent's goroutine, and reads nothing the child owns. The finding called for no new test; `TestCreateSession`'s first assertion checks the answer and passes. |

No new findings.

## Verdict

`Ready to commit`: F1 is resolved.
