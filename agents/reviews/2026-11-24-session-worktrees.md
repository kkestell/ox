# Session worktrees

## Scope and coverage

Reviewed the uncommitted changes in the `session-worktrees` worktree against `agents/plans/2026-11-22-session-worktrees.md`. Base revision `63277a04c06037578f612b5cf8671f6701acb085` (`git merge-base origin/main HEAD`, also `HEAD`). The working tree held 34 modified files and four untracked ones: the plan, `agents/scratch/session-worktrees-history.md`, `internal/server/worktree.go`, and `internal/server/worktree_test.go`. The requirements are the plan's Goal, Decisions, Requirement changes (approved word for word), and Check your assumptions entries 1–9; entry 10 takes the Alternative, so `AGENTS.md` changes only in its source map.

Checked against the plan:

- `docs/functional-requirements.md` carries the plan's Requirement changes word for word, and nothing else.
- `AGENTS.md` changes only the two source-map lines, as entry 10's decision says.
- Codex findings the plan marks fixed: 2 (`Reaped`, `session.exiting`, every delete waits), 3 (the `worktrees` row, `worktreeOwnerLocked` at `workspace/create` and `create_session`, symlinks resolved), 4 (a fresh name per attempt, the existing-ID decision at publication), 5 (`core.hooksPath=/dev/null`, branch deleted on a failed add), 7 (registration and folder read live, missing folder's registration cleared, start refuses a missing directory, a repeated delete finishes), 8 (absolute `-worktrees`, containment against every listed worktree, dev and e2e paths outside their checkouts, mapped-directory check), and 12 (`cmd/ox-mcp/main_test.go`, drafts start on Local) are in the code with tests. Finding 6's shutdown half is fixed by `Server.creations`. Its cleanup-context half is answered by "the workspace cannot be removed while it has sessions", which holds for the workspace record and not for its folder on disk; F2 covers what that leaves.
- Open `bug` issues #5, #6, #33, #34, #84, #85, #86: none is about this change's code, and the change resolves none.

Gaps:

- `make test`, `make e2e`, `make live`, and the headless Chrome check are taken from the implementer's report; this review re-ran only focused Go tests.
- The `/ox-work` Tests proved results were not passed on; four guards were spot-checked instead (Checks run).

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                             |
| -- | -------- | ------------ | -------- | ------ | ----------------------------------------------------------------- |
| F1 | Medium   | Requirements | Fix      | Open   | A delete holds up other sessions' `list_sessions`                 |
| F2 | Medium   | Correctness  | Fix      | Open   | A worktree session whose workspace folder is gone cannot be deleted |
| F3 | Low      | Conventions  | Fix      | Open   | Doc comments left above the wrong declarations                    |
| F4 | Low      | Correctness  | Rejected | —      | A discard failure after an `existing` answer is only logged       |

## F1. A delete holds up other sessions' `list_sessions`

Medium · Requirements · Fix · `internal/server/session.go:508`

`destroy` now waits on every `exiting` channel and then runs `removeWorktree`'s git commands, all on the deleted session's goroutine. `list_sessions` posts to every listed session's goroutine and waits for each answer, so another session's `list_sessions` waits until the delete finishes: about a second for a Claude that exits on SIGTERM, up to `killAfter` (10 s) plus the kill for one that does not, plus `git status` over the whole worktree. A `send_message` to the session being deleted waits the same way. This breaks **Independent sessions** ("One session starting, changing settings, or running never holds up another session"), and the plan's Concerns claim it holds.

`TestStartAndStopHoldUpNoOtherSession` used to assert that A's `list_sessions` answered while B's destroy left Claude running. The rewrite replaced that check with `workspace/list` (`methods_test.go:1108`), which reads under `Server.mu` and never reaches B's goroutine, so the test no longer observes the property its name states.

### Evidence

Put the old check back into the rewritten test: a call to `stateOfB()` (A's `list_sessions`) right after `c.update("session-b", "parked")`, while the stand-in Claude ignores SIGTERM. The test failed at that call with `no matching message within 5s`: A's `list_sessions` got no answer while B's destroy waited for Claude. The edit was reverted and the file compared byte for byte with its backup.

### Action

Keep the session goroutine answering while a delete waits. On the goroutine, `destroy` refuses a session with children, stops the agent, and puts the session into a deleting state, one value holding the pending reply, which `start` refuses (`session is being deleted`) and a second `session/destroy` is refused by. A goroutine waits on the `exiting` channels, runs `removeWorktree`, and posts the result back. The posted op deletes the row and unpublishes, or leaves the deleting state and replies with the error. If shutdown has ended the goroutine by then, the post fails, and the waiting goroutine replies `daemon is shutting down`. Nothing else can start the agent while it waits, because `start` runs only on the goroutine and refuses there.

Test: restore the assertion in `TestStartAndStopHoldUpNoOtherSession` that A's `list_sessions` answers, listing B, while B's destroy waits for the SIGTERM-ignoring stand-in, then kill the stand-in and check the destroy's reply. Add a check that `session/send` to B during the wait is refused, in the same test.

## F2. A worktree session whose workspace folder is gone cannot be deleted

Medium · Correctness · Fix · `internal/server/worktree.go:145`

`removeWorktree` runs every git command with `-C <workspace>`. When the workspace's folder no longer exists, its first command, `worktree list`, fails with `fatal: cannot change to '<workspace>'`, so `session/destroy` fails every time. `workspace/remove` refuses a workspace that has sessions, so neither the session nor its workspace can be removed. This happens when the user deletes the checkout they added as a workspace, as `make prune` does to a merged branch's worktree, or switches the main checkout to a branch without the workspace's subfolder. In the first case, recreating the folder does not help, since it is no longer in a repository.

The plan answers Codex finding 6 with "the workspace cannot be removed while it has sessions". That holds for the workspace's record, not its folder.

### Evidence

Traced `destroy` → `removeWorktree` → `worktreePaths(step, workspace)` → `git -C <workspace> worktree list --porcelain -z`. Git 2.50.1 probe in a temp repository: with a linked checkout `lead` as the source of a worktree `wt` on `ox/abc`, `rm -rf lead` makes `git -C lead worktree list` fail with `fatal: cannot change to 'lead'`, while `git -C wt worktree list --porcelain` lists all three worktrees and `git -C wt worktree remove <wt>` removes `wt` and exits 0. `removeWorkspace` refuses with `workspace has sessions` (`methods.go:221`).

### Action

Run the removal's git from a repository context that outlives the workspace folder. When the worktree root is present, run `worktree list` and `worktree remove` from the root (`git -C <root>`), which lists the repository's worktrees and can remove itself. When the root is missing, clear the registration from the workspace as now. If the workspace folder is also gone, nothing of the worktree remains on disk: log `session <id>: worktree <root> and workspace <path> are gone; leaving the registration` and go on, since git marks the registration prunable itself.

Test: a case in `internal/server/worktree_test.go` that creates a worktree session in `<repo>/sub`, removes `<repo>/sub` from the main checkout, and checks that `session/destroy` succeeds, the worktree folder and its registration are gone, and the branch stays.

## F3. Doc comments left above the wrong declarations

Low · Conventions · Fix · `internal/server/methods_test.go:1767`

Two insertions went between a doc comment and the declaration it documents:

- `internal/server/methods_test.go:1767`: `// configuringAgent reports each configuration it starts on started.` now sits above `gatedReap`'s own comment, and `configuringAgent` (line 1817) has none.
- `cmd/ox/web/e2e/ui.ts:121`: `newSession`'s comment now sits above `location` and `gitRepo`, and `newSession` (line 150) has none.

`cmd/ox/web/e2e/workspace.spec.ts:290` also spells out the Location combobox locator instead of using the new `location` helper.

### Evidence

Read in the diff.

### Action

Move each comment back above its declaration, and use `location(page)` in `workspace.spec.ts`. No test needed.

## F4. A discard failure after an `existing` answer is only logged

Low · Correctness · Rejected · `internal/server/methods.go:392`

When a creation loses the race to another attempt with the same ID in the same workspace, it discards its own worktree and answers `existing`; if the discard fails, the error is logged under the session's ID and the reply still succeeds.

### Action

Rejected. The session the page asked for exists, so `existing` is the correct answer, and failing the reply would make the page treat a created session as uncreated. **Worktrees**' "leaves no worktree or branch behind" covers a creation that fails, which this one does not. A forced `worktree remove` and `branch -D` failing on a checkout made milliseconds earlier is a hypothetical failure, and the log line names the session and git's reason.

## Checks run

- Probe for F1: A's `list_sessions` during B's destroy, added to `TestStartAndStopHoldUpNoOtherSession`, failed with `no matching message within 5s`. Reverted.
- Git probe for F2 in `/tmp`, removed afterwards.
- Guards removed one at a time, each failing its test, then restored and compared byte for byte:
  - the owner check in `checkChildLocked`: `TestCreateSession` fails;
  - the `s.closed` check at publication: `TestWorktreeCloseDuringCheckout` fails;
  - the directory check in `session.start`: `TestWorktreeDeleteAfterRemovedByHand` fails;
  - the wait on `exiting` in `destroy`: `TestDestroyWaitsForParkedProcesses` fails.
- `go build ./...` and `go vet ./internal/...` on the restored tree: pass.
- From the implementer's report: `make test` passed; `make e2e` 174 passed, 4 skipped; `make live` passed on a second full run (the first hit a `TestLiveUr` titling timeout through OpenRouter); a headless Chrome check through `make dev`.

## Verification 1

Checked the uncommitted tree on base `63277a04c06037578f612b5cf8671f6701acb085` after `/ox-work fix`: 34 modified files and the same four untracked ones, plus this review.

| ID | Status   | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| F1 | Resolved | `destroy` sets `session.deleting`, a single `*deleting` value holding only the reply. It is set only while the delete waits and cleared in `finishDelete`. No lock is added. The wait and `removeWorktree` run on the delete's own goroutine, which posts the outcome back. If the post fails because shutdown ended the session, that goroutine replies `daemon is shutting down`. The two reply paths exclude each other, so the destroy is answered once. `start`, `checkChildLocked` (on the parent's goroutine), and a second destroy refuse with `session is being deleted`. `TestStartAndStopHoldUpNoOtherSession` again checks that A's `list_sessions` answers while B's destroy waits on a SIGTERM-ignoring Claude, and that a send and a second destroy are refused. With `start`'s refusal removed, it fails. `docs/architecture.md` and `docs/protocol.md` state the new behavior. |
| F2 | Resolved | `removeWorktree` runs git in the worktree while it is present, and in the workspace when the worktree is missing. When both are gone, it logs and goes on. `TestWorktreeDeleteAfterWorkspaceRemoved` removes `<repo>/sub` and checks that the worktree and its registration are gone and the branch stays. It fails when the removal runs only from the workspace. `docs/protocol.md` states the case.                                                       |
| F3 | Resolved | The `configuringAgent` and `newSession` comments are back above their declarations, and `workspace.spec.ts:291` uses `location(page)`.                                                                                                                                                                                                                                                                                                                         |

No new findings. Shutdown no longer waits for a delete's git, as it did when the delete blocked the session's goroutine. Rejected: every outcome is valid. Git, once started, finishes on its own; a delete cut short leaves the session listed with a stored row; and a repeated delete finishes it, which `TestWorktreeDeleteAfterRemovedByHand` and `TestWorktreeDeleteRepeated` cover.

Checks run:

- `go test -race ./internal/server`: pass.
- `start`'s `deleting` refusal removed: `TestStartAndStopHoldUpNoOtherSession` fails. Restored.
- `removeWorktree` limited to the workspace: `TestWorktreeDeleteAfterWorkspaceRemoved` fails. Restored.
- From the implementer's report: `make test` and `make e2e` (174 passed) passed, and so did the headless Chrome check through `make dev`. In `make live`, every Go live test passed. The approval spec "an ExitPlanMode plan renders as Markdown" failed once, when Haiku's plan held no list, and passed when the approval specs were rerun alone. The change does not touch plan rendering, so the failure comes from the model's wording, not from this change.

## Verdict

Ready to commit. F1, F2, and F3 are resolved; F4 is rejected.
