# Review: keep session order across a restart, and let a second Ctrl-C reach a restarting oxd

## Scope and coverage

Reviewed the uncommitted changes in the worktree on branch `fix-104-edge-cases`, against base `4c32af4` (`git merge-base origin/main HEAD`), with `agents/plans/2026-11-27-fix-104-edge-cases.md` as the plan. Files changed: `internal/server/methods.go`, `internal/server/mcp.go`, `internal/server/worktree_test.go`, `cmd/ox/web/stack.mjs`, `cmd/ox/web/stack.test.mjs`.

The change touches one state owner, the creation time a top-level session gets under `s.mu`. It is small enough to review in this session, so no concern reviewers were started.

Traced:

- Every path that sets `store.Session.CreatedAt`. After the change only `addSession` (both top-level paths: `session/create` and MCP `create_top_level_session`) and `addChild` set it. `newSession` and `publishLocked` read it after it is set. The store write, the append to `w.sessions`, and the time all happen in one `s.mu` section, and `Sessions` loads `ORDER BY created_at, rowid`, so the live list and the reloaded list agree.
- `stack.mjs`: `retire`, `stop`, `restart`, and the second-SIGINT handler. After the first Ctrl-C, `stops` holds every current process and any process a restart is retiring. Entries whose exit was judged a failure stay, but their process has exited, so the guard skips them.

Issue #104 lists F14, F18, and F20. F18 (a configured host spelled with capitals) was fixed by #159 (`cmd/ox/main.go:121,145` lowercase both sides), so this change resolves the rest of #104, and its pull request can close it.

Gaps: none.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                      |
| -- | -------- | ------------ | -------- | ------ | ---------------------------------------------------------- |
| F1 | Low      | Conventions  | Fix      | Resolved | `retire`'s entry is named `stop`, hiding the `stop` function |
| F2 | Low      | Testing      | Rejected | —      | The new stack test leaves a background `kill` that prints an error |

## F1. `retire`'s entry is named `stop`, hiding the `stop` function

Low · Conventions · Fix · Resolved · `cmd/ox/web/stack.mjs:68`

`retire` declares `const stop = { proc, stopped: ... }`. That name hides `runStack`'s `stop(code, signal)` function inside `retire`. Nothing calls `stop()` in `retire` now. A later edit that does, such as stopping the stack when a retire fails, would call an object and throw a `TypeError`, on the path that is supposed to end the stack.

### Evidence

Read from the source: `async function stop(code, signal)` is declared at `stack.mjs:79` in the same closure as `retire`, whose local `stop` hides it at lines 68–75.

### Action

Rename the local to `entry` (`const entry = { proc, stopped: stopProcess(proc, signal) }`, `stops.push(entry)`, `stops.indexOf(entry)`, `return entry.stopped`). The rename changes no behavior. The existing stack tests cover it, and no new test is needed.

## F2. The new stack test leaves a background `kill` that prints an error

Low · Testing · Rejected · `cmd/ox/web/stack.test.mjs:221`

The `oxd` trap starts `(sleep 2; kill -USR1 $$) &`. When the forced `INT` ends `oxd` first, that subshell outlives it and prints `kill: No such process` two seconds later.

### Action

Rejected: the test copies the pattern of the existing "a second Ctrl-C forwards SIGINT" test (`stack.test.mjs:185`). The leftover subshell does not affect the assertions or later tests, and the stray line is cheaper to keep than a different shutdown scheme for the stand-in.

## Checks run

- `go test -race -run TestWorktreeAfterRestart -count=3 ./internal/server/`: passes with the change.
- The same test with `methods.go` and `mcp.go` reverted to base: fails, because after the restart the worktree session is listed first.
- `node --test` on the new test "a second Ctrl-C reaches an oxd a restart is retiring": passes with the change. With `stack.mjs` reverted to base it fails: the stack resolves 130, not 1.
- Spot check: with `addChild`'s `record.CreatedAt = time.Now()` removed, `TestCreateSession` ("children outlive a restart") and `TestChildReports` fail. The child's time is covered.
- `make test` on the final tree: all Go packages pass, and all 52 Node tests pass.
- `make e2e`, `make live`, and the `make dev` headless Chrome check: skipped. They cannot observe the change, which alters neither what the page does, nor how Ox drives Claude Code, nor how the page reaches `oxd`.

## Verification 1

Checked the uncommitted changes on `fix-104-edge-cases` over base `4c32af4`. Since the review, the only change is the rename in `retire`. The other files' diffs are the same as when the review was written.

| ID | Status   | Evidence                                                                                                                              |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `retire` names its entry `entry` (`stack.mjs:68-75`), and `stop` now refers only to the function. All 18 stack tests pass. |

No new findings.

Checks run: `node --test stack.test.mjs` (18 pass) and `make test` (every Go package and all 52 Node tests pass).

## Verdict

`Ready to commit`: F1 is resolved, and F2 is rejected.
