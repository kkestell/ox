# Review: Let Claude start top-level sessions in any workspace

## Scope and coverage

Reviewed the uncommitted changes on branch `agent-top-level-sessions` against
base `57aad1e` (`git merge-base origin/main HEAD`), with the plan
`agents/plans/2026-11-27-agent-top-level-sessions.md`. The user approved the
plan's four assumptions and its requirement changes word for word, and did not
approve its AGENTS.md paragraph.

Checked: the requirements diff word for word (_Session labels_, _Claude can
start top-level sessions_, _Session reports_); `AGENTS.md` unchanged; the
`createTopLevel` extraction and `session/create`'s reply order and lost-reply
path; `create_top_level_session` from argument checks through the first
message; `reporting`, `reportsTo`, `reportsToLocked`, `reportersLocked`, and
`report` through a creator's archive, delete, and an `oxd` restart; the store's
`creators` and `titles` rows; the docs in `protocol.md` and `architecture.md`;
and each departure the implementer reported.

Departures checked and accepted:

- The tool's title shows in the transcript through `OX_SUBJECT_FIELD` in
  `toolInput.ts`. The plan's schema-key order could not hold, since Go encodes a
  `map` with sorted keys; the subject map is the page's existing mechanism for
  `create_session`.
- `addSession` returns only an error: its only callers needed nothing else.
- The bad-`workspace` refusal reads `workspace must be the path of an Ox
  workspace`, and `protocol.md` lists it in order.
- The line in `internal/claude/claude.go` that points agents to
  `create_session` for handing off work is unchanged, as intended: that line is
  about delegating within a task, and the tool's own description says when to
  use `create_top_level_session`.
- `session/create` replying after `s.mu` and the `daemon is shutting down` text
  are F2 and F3.

Gaps:

- No concern-reviewer child sessions were started; the lifecycle, boundary, and
  construction concerns were checked in this session.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                                   |
| -- | -------- | ------------- | -------- | ------ | ----------------------------------------------------------------------- |
| F1 | Low      | Documentation | Fix      | Resolved | Reports are still named as children's in the page and comments          |
| F2 | Low      | Conventions   | Rejected | —      | `session/create` queues its reply after releasing `s.mu`                |
| F3 | Low      | Correctness   | Rejected | —      | A session deleted before its first message reports a shutdown           |
| F4 | Low      | Intent        | Rejected | —      | A creator's deletion lets its started session start others after restart |

## F1. Reports are still named as children's in the page and comments

Low · Documentation · Fix · `cmd/ox/web/src/SessionView.tsx:290`

Reports now come from children and from started sessions, but three places
still describe them as children's only:

- `cmd/ox/web/src/SessionView.tsx:290`: the reports article's accessible name is
  `Child reports`, so a screen reader announces a started session's report as a
  child's. `cmd/ox/web/e2e/session.spec.ts:1911` locates the article by that
  name.
- `internal/agent/agent.go:253`: `Report is a child session's account of one of
  its turns`.
- `cmd/ox/web/src/protocol.ts:42`: the same sentence.

### Evidence

`grep -rn "Child reports\|child session's account"` over `cmd/ox/web/src`,
`cmd/ox/web/e2e`, and `internal/agent`.

### Action

Name the article `Reports` (or `Session reports`, matching the requirement's
title) and update the locator in `session.spec.ts`. Change both comments to
describe a report as the account of a session that reports to another, a child
or a session another session's Claude started. The existing e2e test `a child's
reply reaches its parent as a report …` carries the renamed locator; no new test
is needed.

## F2. `session/create` queues its reply after releasing `s.mu`

Low · Conventions · Rejected · `internal/server/methods.go:404`

`createTopLevel` returns its result, and `createSession` queues the reply after
`s.mu` is released, where it was queued inside the publishing section before.
`AGENTS.md` says a response is queued by the owner of the state it reports, when
it reads that state.

### Evidence

`publishLocked` queues `session/created` to every connection under `s.mu`, and
the reply goes to the same connection's FIFO queue (`conn.queue`) afterwards, so
the page still receives `session/created` before the reply, and `post(prompt)`
finds the session. The lost-reply path is unchanged: a repeated `session/create`
with the same `session_id` answers `existing` from `existingLocked`, read under
`s.mu`, and a repeat that races the first creation's worktree still makes and
discards its own worktree at publication, as on the base. Only another event
about the new session queued in the gap between unlock and reply, such as
another page deleting the session within that gap, can now precede the reply.

### Action

Rejected: the reply carries only the session's ID and whether it already
existed, which no later state changes except deletion, and the reordering needs
a contrived sequence. Moving the reply back under the lock would make
`createTopLevel` take a reply callback that the tool's caller does not need.

## F3. A session deleted before its first message reports a shutdown

Low · Correctness · Rejected · `internal/server/mcp.go:614`

When the started session is gone between publication and the post of its first
message, the tool's error reads `session <id> was created, but refused its first
message: daemon is shutting down`, though `oxd` is running.

### Evidence

Traced: `sess == nil || !sess.post(...)` sends `errShuttingDown` for both a
closed server and a session deleted in that window.

### Action

Rejected: the window is the time between two lock sections on the call
goroutine, and reaching it needs another page to delete a session the moment it
appears. In normal use the only cause is shutdown, which the text names.

## F4. A creator's deletion lets its started session start others after restart

Low · Intent · Rejected · `internal/store/store.go:104`

The `creators` row cascades when the creator is deleted. Until `oxd` restarts,
the started session still names its deleted creator, so
`create_top_level_session` refuses it; after a restart it reads no creator and
the tool accepts it.

### Evidence

Traced `reportsTo()` against the cascade; the store test confirms `Creator` reads
empty after the creator's deletion.

### Action

Rejected: the plan's Ownership decision settles this cascade and the
before-and-after-restart difference. A session whose orchestrator the user
deleted is one the user now directs, and the refusal exists to stop chains of
started sessions, which a single orphan cannot form unattended.

## Checks run

- Requirements diff compared word for word with the plan: matches.
  `git diff --quiet -- AGENTS.md`: unchanged.
- Spot-check removals, each restored afterwards:
  - Dropping `sess.title != ""` from `labelFrom`: `TestCreateTopLevelSession`
    fails (`a prompt relabeled the started session`).
  - Narrowing the caller refusal to `caller.child != nil`:
    `TestCreateTopLevelSession` fails (a started session was allowed).
  - Creating `reporting` only for children in `newSession`:
    `TestCreateTopLevelSession` fails (no report after restart).
- From the implementer: `make test` and `make e2e` passed. `make live` is not
  required: how Ox drives Claude Code does not change.

## Verification 1

Checked the working tree after the fixes for F1, on the same base. The changes
since the review touch only `cmd/ox/web/src/SessionView.tsx`,
`cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/src/protocol.ts`, and
`internal/agent/agent.go`.

| ID | Status   | Evidence                                                                                                                                                                                                  |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The article is named `Session reports`, and the e2e test locates it by that name and passes; the `Report` comments in `agent.go` and `protocol.ts` describe children and started sessions. No other changes. |

No new findings. Check run: `npm --prefix cmd/ox/web run e2e -- -g "report
headed with its title"` passed.

## Verdict

Ready to commit.
