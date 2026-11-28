# Lifecycle guards review

## Scope and coverage

The uncommitted changes on `lifecycle-guards` over base `758ddf3`, checked
against `agents/plans/2026-11-28-lifecycle-guards.md`. The user approved the
plan's "Session reports" text and the page change where a child whose delete
is in flight no longer blocks its parent's Delete. `internal/ur/engine` and
`.claude/skills/ox-work/SKILL.md`, which another session is editing, are out
of scope.

The server change, the requirement and doc edits, the page's type split, and
the rewritten report tests match the plan. Most of the review went to the
question the request raised: does `TestLifecycle` guard lifecycle paths
beyond the three mutations it was tuned on? It was measured with a temporary
test that walks a range of seeds through the same `walk`, under the plan's
three mutations and seven it was not tuned on. The probe and every mutation
were removed afterwards.

Gaps:

- `make e2e` was not rerun here, since one session at a time runs it. The
  implementer reports it passed. The new children spec was read but not run
  without the change.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                           |
| -- | -------- | ------------- | -------- | ------ | --------------------------------------------------------------- |
| F1 | Medium   | Testing       | Fix      | Resolved | TestLifecycle accepts any refusal, so lost deletes and unarchives pass |
| F2 | Medium   | Testing       | Fix      | Resolved | TestLifecycle's seeds were picked to catch the three mutations  |
| F5 | Medium   | Testing       | Fix      | Resolved | `callTool` takes a notification for the attach reply            |
| F3 | Low      | Documentation | Fix      | Resolved | `update`'s comment says the session takes its reports           |
| F4 | Low      | Testing       | Rejected | —        | TestLifecycle walks no concurrent requests                      |

## F1. TestLifecycle accepts any refusal, so lost deletes and unarchives pass

Medium · Testing · Fix · `internal/server/lifecycle_test.go:619`

`unchangedIfRefused` checks that a refused archive, unarchive, or delete left
the list and git unchanged, but nothing checks whether the request should have
been refused. Any refusal passes, so whole classes of lifecycle defects pass
on every seed. The plan says "Archive" and "Delete" are covered by the test's
invariants. Their rules are not checked: a delete is refused only with
children or a dirty worktree, archiving waits until the session and everything
beneath it are idle or parked, and a child cannot be unarchived while its
parent is archived.

### Evidence

Each mutation was applied alone, with seeds 1–50 or 1–100 and the kept list:

- `destroy` refuses every delete: no seed fails, and `TestLifecycle` passes.
- `unarchiveLocked` refuses every unarchive: no seed fails, and
  `TestLifecycle` passes.
- The archived-parent check in `unarchiveLocked` removed: no seed fails.
- The busy check in `archive` removed: no seed fails.

### Action

Have the model predict each request's outcome from its own state, and fail
when the server's answer differs:

- `session/destroy` is refused exactly when the target has a child or a dirty
  worktree.
- `session/archive` is refused exactly when the target or a session beneath it
  has a long turn.
- `session/unarchive` is refused exactly when the target's parent is
  archived.

Keep `unchangedIfRefused`'s check for the refusals the model predicts. After
each step, also check that no unarchived session has an archived parent, and
that every archived session is parked. Show in the work log that each of the
four mutations above now fails.

## F2. TestLifecycle's seeds were picked to catch the three mutations

Medium · Testing · Fix · `internal/server/lifecycle_test.go:26`

The kept list `{2, 3, 7, 14, 15, 20, 22, 64}` was picked until each of the
plan's mutations failed somewhere, and the step weights were tuned until the
#192 mutation did. Shallow paths are caught almost always. Paths that need a
chain of four or five steps are caught only by the seeds picked for them:
holding a report for an archived or running recipient, then dirtying, deleting,
and releasing. That is where #192 lived, and where the next lifecycle defect is
likely to be. New lifecycle paths will mostly not be guarded.

### Evidence

Share of seeds that fail, from a contiguous range walked with the current
weights, 40 steps, and a cap of 5 sessions:

| Mutation                                                    | Tuned on | Seeds failing      | Kept seeds failing |
| ----------------------------------------------------------- | -------- | ------------------ | ------------------ |
| Rec 3: running-reporter condition restored                  | yes      | 196 / 200          | all 8              |
| #191: a session whose first message failed is published     | yes      | 114 / 200          | 5                  |
| #192: reports dropped when a delete starts                  | yes      | 3 / 200 (64, 95, 176) | 64              |
| No delivery on unarchive                                    | no       | 10 / 100           | 20, 64             |
| A report made while deleting                                | no       | 2 / 100            | 2                  |
| A refused delete does not deliver the reports waiting for it | no      | 3 / 100            | none               |
| The four mutations in F1                                    | no       | 0                  | none               |

Raising the walk to 80 steps took the #192 rate from 1.5% to 7 / 100. A cap
of 3 sessions did not help (2 / 100 at 40 steps, 4 / 100 at 80).

### Action

Choose the seeds without reference to the mutations: a contiguous range, plus
pinned seeds for the audit's probes, each named in the comment as seed 64 is
for #192. Spend the plan's 20-second budget under `-race` on longer walks
rather than more of today's 40-step walks, since length is what raised the
deep-chain rate. With F1's outcome predictions in place, report in the work
log the share of a 100-seed range that fails under each mutation in the table
above, and keep the seed count and walk length that, within the budget,
catch the most untuned mutations. Update the doc comment to say that a new
lifecycle path joins the generator and has its failure rate measured the same
way.

## F3. `update`'s comment says the session takes its reports

Low · Documentation · Fix · `internal/server/session.go:446`

The new comment says a state update is broadcast "so ... the session takes the
reports sent to it in the same critical section." Under the lock, `update` only
decides readiness and posts a delivery. `takeReports` takes the reports later,
on the session's goroutine, after checking readiness again.

### Action

Say that the session's readiness for its reports is decided in the same
critical section.

## F4. TestLifecycle walks no concurrent requests

Low · Testing · Rejected · `internal/server/lifecycle_test.go:506`

Each step settles before the next, so the test cannot see two requests
interleave. For example, it cannot catch a refused delete that skips
delivering the reporter's held reports, because at every step boundary those
reports have already reached any ready recipient.

### Action

Rejected: settling after each step is the plan's decision, and it keeps the
test deterministic per seed. Interleavings stay with the targeted tests in
`mcp_test.go`.

## F5. `callTool` takes a notification for the attach reply

Medium · Testing · Fix · `internal/server/lifecycle_test.go:544`

`callTool` opens a connection, sends `mcp/attach` and `tools/call`, and
decodes the first two lines it reads as their replies. A new connection gets
broadcasts until `mcp/attach` takes it off the broadcast list, and those
notifications come before the attach result, as `docs/protocol.md`
("`mcp/attach`") says. When a broadcast lands in that window, `callTool`
reads the notification as the attach reply and the attach reply as the tool's
answer. It then fails with `returned {Content:[] IsError:false}`. The case
fails with no defect in `oxd`. This explains the two unsaved `--- FAIL` lines
the implementer saw.

### Evidence

`TestLifecycle` built with `-race` and run with `-count=25 -v` failed in 4 of
25 runs, 6 subtests in all. Every failure was
`create_top_level_session: returned {Content:[] IsError:false}`, raised
while `family` built its sessions. The subtests that failed differ from run to
run (`started with worktree/delete clean/idle`,
`recipient/archive/turn running`, `recipient/unarchive/report held`, and
others). The message is the `len(result.Result.Content) != 1` error from
`callTool`. The test's `attach` helper in `client_test.go` does not hit this,
because `client.call` matches its reply by ID and skips notifications.

### Action

Have `callTool` read each reply by its ID and skip lines that carry no ID,
as `client.call` does. Keep `TestLifecycle` as the test: under `-race`,
`-count=25` passes with no failure.

## Checks run

- A temporary sweep over `walk` with seeds 1–200, unmutated: all pass.
- The mutation sweeps in F1 and F2. Every mutation was reverted, and the
  sources match the reviewed tree.
- `TestLifecycle`, `TestChildReports`, and `TestRefusedDeleteKeepsReports`
  on the restored tree: pass.
- Spot check of the page types: a view that reads `Session.archived` or
  `Workspace.sessions` fails `tsc`.
- `make test` and `make e2e`: taken from the implementer's report, which says
  both passed. `make live` cannot observe this change.

## Verification 1

Checked the working tree after the fixes. `TestLifecycle` is now a
deterministic table: creation paths × outcomes, and roles × destructive
actions × states, with predicted outcomes. The plan's Goal, Decisions, and
Test plan describe the same table. As the request directed, the table was
judged as written, with no mutation runs.

| ID | Status   | Evidence                                                                                                                                                                                                                                                                          |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `predicted` checks every archive, unarchive, and delete against `refusal`. That covers children, then a dirty worktree, for a delete; a busy session or descendant for an archive; an archived parent for an unarchive. A wrong refusal and a wrong success both fail. `check` also requires that a child of an archived session is archived and that every archived session is parked. |
| F2 | Resolved | No seeds or randomness remain. Every role meets every action in every state. #192's path is the cell `started with worktree/delete dirty/report held`, and #191's is each tool path × `first message refused`. A new path joins as a row, as the doc comment says.                                       |
| F3 | Resolved | The comment now says the session's readiness for its reports is decided in the same critical section.                                                                                                                                                                             |

New finding: F5, above, which reproduces the unsaved failure the implementer
saw.

Checks run:

- `TestLifecycle` built with `-race`, run with `-count=25 -v`: 4 of 25 runs
  failed (F5). With F5 reproduced, the planned runs under load were not
  needed and were not done.
- `make test` and `make e2e` were not rerun. The `TestLifecycle` failures
  above already block the verdict.

## Verification 2

Checked the working tree after the F5 fix.

| ID | Status   | Evidence                                                                                                                                                                                                                                                       |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F5 | Resolved | `callTool` reads each reply by its ID and skips notifications, which carry none. `TestLifecycle` built with `-race` and run with `-count=25 -v` passed all 25 runs, 1,300 subtest passes in all, with no data race. Before the fix, the same run failed 4 of 25 times. |

No new findings. The change since Verification 1 touches only `callTool` and
its comment.

Checks run:

- `TestLifecycle` with `-race` and `-count=25`: pass.
- `go test -race ./internal/server`: pass.
- `make test`: taken from the implementer's report, which says it passed.

## Verdict

Ready to commit.
