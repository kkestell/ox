# Review: publish an agent-started top-level session only once it holds its first message

## Scope and coverage

Uncommitted changes in the `top-level-session-rollback` worktree on base `dae1033` (`git merge-base origin/main HEAD`): `internal/server/mcp.go`, `methods.go`, `server.go`, `mcp_test.go`, `docs/architecture.md`, and `docs/protocol.md`. Checked against `agents/plans/2026-11-27-top-level-session-rollback.md`, issue #188, and audit finding F2 in `agents/audits/2026-11-27-main.md`.

The change is contained to `internal/server` and touches one state owner (creation and publication), so it was reviewed in this session without concern reviewers.

Gaps:

- No `/ox-work` final response reached this review, so its Tests proved and Checks were not available; the checks below were run here instead.
- `make e2e` and `make live` were not run. The page's code is unchanged and handles `session/created` with any state, and Ox makes the same `Start` and `Send` calls to the harness, only before publication instead of after, so neither suite observes the change.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                             |
| -- | -------- | ------------- | -------- | ------ | ----------------------------------------------------------------- |
| F1 | Low      | Documentation | Fix      | Resolved | `session.update` comment says only a child is held unpublished    |
| F2 | Low      | Settling      | Rejected | —      | `addChild` rollback now waits for the process on the parent       |
| F3 | Low      | Correctness   | Rejected | —      | A launched session's `created_at` precedes its publication        |
| F4 | Low      | Testing       | Rejected | —      | No test for a caller whose delete finished with no `workspace`    |

## F1. `session.update` comment says only a child is held unpublished

Low · Documentation · Fix · Resolved · `internal/server/session.go:634`

The comment in `session.update` reads "A child's creator holds it outside membership until its agent has accepted the first input". After this change, `createTopLevel` also holds an agent-started top-level session outside membership while it launches and takes its first message, so the comment states a narrower rule than the code follows.

### Evidence

`createTopLevel` (`internal/server/methods.go`) calls `s.launch` before `publishLocked`; `launch` runs `sess.start` and `acceptMessage`, whose updates reach `session.update` while `s.sessions[sess.id] != sess`.

### Action

Reword the comment to cover any session another session starts, child or top-level, for example: "A session another session starts is held outside membership until its agent has accepted the first input; publication then reports its state." No test.

## F2. `addChild` rollback now waits for the process on the parent

Low · Settling · Rejected · `internal/server/mcp.go:499`

`discardUnpublished` waits on `Reaped()` after `Stop`, and `addChild` runs on the parent's goroutine, so a child refused at publication by shutdown holds the parent for as long as the harness takes to exit, up to `killAfter` (10 s).

### Action

Rejected: it happens only during shutdown, which already waits for every session and creation, or after a first `Send` that failed because the process has already exited, when `Reaped` closes at once. The wait is bounded by the kill. The plan chose one rollback for both paths so the worktree is removed only after the harness has left it.

## F3. A launched session's `created_at` precedes its publication

Low · Correctness · Rejected · `internal/server/methods.go:484`

`storeSession` sets `CreatedAt` at launch, before publication, so a page session published while an agent-started session is launching could take a later `created_at` but an earlier place in the live list.

### Action

Rejected: the launch is a harness `Start` that returns at once and a `Send` that queues, so the window is microseconds, and `addChild` already behaves this way. Not reached in normal use.

## F4. No test for a caller whose delete finished with no `workspace`

Low · Testing · Rejected · `internal/server/mcp.go:601`

The new `w == nil && info == nil` branch answers `session not found` when the caller's delete finished between its tool call arriving and the workspace lookup. No test reaches it.

### Action

Rejected: reaching it needs a delete to finish inside a tool call's handling, a contrived interleaving. The branch only chooses which refusal is reported.

## Checks run

- `go vet ./internal/server/` and `go test -race -count=1 ./internal/server/`: pass.
- `make test` (lint, Go with the race detector, Node suites): pass.
- `dprint check docs/architecture.md docs/protocol.md`: clean.
- New and changed tests on the base revision with this change's `mcp_test.go`: `TestCreateTopLevelSession` fails (the broken harness answers "session … was created, but refused its first message"), `TestCreateTopLevelSessionByArchivedCaller` fails (the archived caller creates a session), both `create_top_level_session` cases of `TestStartHoldsUpNoOtherSession` fail (the session is published `parked` before its gated start or send), and `TestFailedLaunchKeepsItsErrorWhenDeleteFails` fails on the renamed log line.
- Removing `abandonWorktree` from `createTopLevel`'s launch-failure return: `TestCreateTopLevelSession` fails with the worktree still registered.
- Removing `discardUnpublished` from `createTopLevel`'s refusal path: both `create_top_level_session` cases of `TestStartHoldsUpNoOtherSession` fail with the refused session listed after a restart.
- Every edit made for these checks was reverted.

## Verification 1

Checked the working tree on base `dae1033` after the fix. The only change since the review is the comment in `internal/server/session.go`; the other files' diffs are unchanged. `gofmt -l internal/server` is clean, and `go test -race -count=1 ./internal/server/` passes.

| ID | Status   | Evidence                                                                                                                  |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The comment now says any session another session starts, child or top-level, is held outside membership until its first input |

No new findings.

## Verdict

`Ready to commit`.
