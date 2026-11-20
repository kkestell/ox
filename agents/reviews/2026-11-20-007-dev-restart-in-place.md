# Review: restart changed binaries in place under `make dev`

## Scope and coverage

Uncommitted changes on base `1319d9f` to `Makefile`, `cmd/ox/web/dev.mjs`, `cmd/ox/web/stack.mjs` (new), `cmd/ox/web/stack.test.mjs` (new), and `docs/architecture.md`, checked against `agents/plans/2026-11-20-007-dev-restart-in-place.md`. The edits to `.claude/skills/` and the plan file are out of scope.

Gaps:

- No `make dev` stack was started: `dev.mjs` hardcodes ports 8080 and 8081 and the shared socket, which the running stack holds. `runStack` was instead driven with real `oxd`, `ox`, and `ox-mcp` binaries built into a temp directory, on a temp socket, database, and port.
- The `oxd` restart was probed without a live Claude process, so `oxd`'s SIGTERM shutdown with running sessions was not exercised. That path is unchanged by this change.

## Summary

| ID | Severity | Category | Action | Status | Title                                                    |
| -- | -------- | -------- | ------ | ------ | -------------------------------------------------------- |
| F1 | Low      | Testing  | Fix    | Resolved | Busy guard and stop-during-restart check have no test    |

## F1. Busy guard and stop-during-restart check have no test

Low · Testing · Fix · Resolved · `cmd/ox/web/stack.mjs:67`, `cmd/ox/web/stack.mjs:90`

The plan's **Busy** decision adds two conditions that no test protects:

- `if (busy)` in the line handler (`stack.mjs:90`). Without it, a second Enter during a build starts a second rebuild, and a changed service restarts twice.
- `if (stopping) return` after the old process exits in `restart` (`stack.mjs:67`). Without it, stopping the stack while a restart waits on the old process spawns a new process after the stack has stopped. That process is never killed, and `dev.mjs` never exits because the child keeps Node running. This is reachable with Ctrl-C or SIGTERM during an `oxd` restart, which waits for `oxd`'s Claude processes to exit.

The implementer's Tests proved list does not claim either.

### Evidence

In a scratch copy of `stack.mjs` and `stack.test.mjs`, removing either condition leaves all six tests passing. Two probe tests in the same copy then:

- Enter twice during a build that sleeps 0.3 s and changes `ox`: with the guard, the log is `rebuilding`, `a rebuild is in progress`, `restarted ox`; without it, `rebuilding`, `rebuilding`, `restarted ox`, `restarted ox`.
- `stop(0)` while restarting a stand-in whose TERM trap sleeps 0.5 s before exiting: with the check, the pid file keeps the first pid; without it, a new pid appears after the stop and the test process hangs on the orphan.

### Action

Add both cases to `cmd/ox/web/stack.test.mjs`:

- **Enter during a rebuild:** a build that sleeps briefly before changing `ox`, two newlines written together; assert `a rebuild is in progress` is logged and `restarted ox` appears once.
- **Stop during a restart:** an `ox` stand-in that traps TERM, sleeps briefly, then exits (for example `trap 'sleep 0.5; exit 0' TERM` and a `while :; do sleep 0.05; done` loop that writes its pid first); press Enter after changing it, call `stop` while the old process is still exiting, and assert after the old process exits that the pid file still holds the first pid. The `script` helper needs a body parameter for this stand-in.

## Checks run

- `make test`: passes, including `node --test cmd/ox/web/stack.test.mjs` (6 tests).
- `make e2e`: passes (35 passed, 3 skipped).
- `make live`: not needed; the change does not alter how Ox drives Claude Code.
- `runStack` with real binaries on a temp socket and port 8197, through a build script that changes `ox` or `oxd` via `-ldflags`: an `ox`-only change restarted `ox` alone and the relay served again; a rebuild that changed nothing logged `nothing changed` and kept both pids; an `oxd` change restarted `oxd` alone after the old one exited and the relay served again; a failing build logged `build failed; nothing restarted` and kept both pids.
- `runStack` under a pty with TTY stdin: a service exiting with code 3 stopped the stack and Node exited with 3 in 1.4 s, so closing the readline reader releases a TTY stdin.
- Vite 8.3.8 spawned with stdin `ignore` stays up; its stdin `end` handler does not fire on `/dev/null`.
- Condition removals in a scratch copy: identity comparison fails tests 1 and 6, as claimed; the busy guard, the stopping check in `restart`, and the stopping check in the rebuild loop fail no test (F1 covers the first two; the third only skips hashing during a stop and needs no test).
- No new test reproduces a defect, so none applies to the base revision.

## Verification 1

Checked the working tree on base `1319d9f` after the fix. `cmd/ox/web/stack.mjs` is unchanged since the review; `cmd/ox/web/stack.test.mjs` gains the two tests and a `body` parameter on `script`, plus `buildDelay` and `oxBody` options on `start`. Checks: `make test` passes (8 stack tests); `make e2e` passes (35 passed, 3 skipped); the stack test file passed three runs in a row; the implementer ran the manual `make dev` check (FIFO stdin; an `cmd/ox` edit restarted only `ox`, an `internal/server` edit only `oxd`).

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | In a scratch copy, removing `if (busy)` fails "a second Enter during a rebuild does not start another", and removing `if (stopping) return` after `await gone` fails "stopping during a restart starts no new process"; both pass with the code as written. |

No new findings.

## Verdict

Ready to commit.
