# Dev shutdown exit status review

## Scope and coverage

Uncommitted changes on `fix-f4` at base `312f3da`: `cmd/ox/web/stack.mjs`,
`cmd/ox/web/stack.test.mjs`, and `docs/todo.md`, checked against
[the plan](../plans/2026-11-22-005-dev-shutdown-exit-status.md). Callers read:
`cmd/ox/web/dev.mjs` and `cmd/ox/web/e2e/services.ts`.

The change delivers the plan's Goal and keeps its Decisions. `exited` is the one
judge, `stopProcess` sends and then judges, `stops` records every retired
process, `stop` is the only place that resolves the result and logs each failure
once, the `SIGINT` handler judges without sending, and the exit and error
handlers clear `record.proc` before `stop`. `stops` gains no entry after
`stopping` is set. Rejected promises in `stops` always have a handler by the
time they settle, from `restart`'s `await` or from `stop`'s `allSettled`. The
plan changes no requirements, and none changed. `docs/todo.md` drops the entry,
and no other document states the stack's exit status.

Gaps:

- The real dev stack check and `make test`/`make e2e` were taken from the
  implementer's report and not rerun. `make live` and a headless Chrome check
  do not apply: the change touches neither how Ox drives Claude Code nor how
  the page reaches `oxd`.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                        |
| -- | -------- | -------- | ------ | -------- | ------------------------------------------------------------ |
| F1 | Low      | Testing  | Fix    | Resolved | No test fails when Ctrl-C signals the services again         |
| F2 | Low      | Testing  | Fix    | Resolved | No test fails when Ctrl-C exits are judged against `SIGTERM` |
| F3 | Low      | Testing  | Fix    | Resolved | No test fails when the stack judges the exit that stopped it |

## F1. No test fails when Ctrl-C signals the services again

Low · Testing · Fix · Resolved · `cmd/ox/web/stack.test.mjs:168`

The plan's Signals decision says the stack sends nothing on `SIGINT`, because
the terminal has already signalled every service, and a second `SIGINT` makes
`oxd` force-stop and exit 1. "one terminal Ctrl-C signals each service once"
is meant to guard this, but it signals each service and then emits the stack's
`SIGINT` in the same tick. The shell has not run its trap yet, so the two
signals merge into one pending `SIGINT`, and the log shows `INT` once either
way.

### Evidence

Changing the handler to `stop(130, "SIGINT", true)` leaves every test passing,
over three runs. Adding a sleep to the trap, and waiting for the trap's log
before emitting, still passed, because the shell drops a pending trap when the
first trap exits.

A version that models `oxd` fails under the mutation in three of three runs and
passes without it, along with the `SIGTERM` and Ctrl-C failure tests. In that
version the first `INT` marks the service and schedules a clean exit, a second
`INT` exits 1, and the test waits for every marker before emitting:

```js
const record = "trap 'echo INT >> $0.log; [ -e $0.int ] && exit 1; touch $0.int; (sleep 0.3; kill -USR1 $$) &' INT; trap 'exit 0' USR1; trap 'echo TERM >> $0.log; exit 0' TERM";
...
if (terminal) for (const id of pids) process.kill(id, signal);
if (terminal) await until(() => names.every((name) => existsSync(`${paths[name]}.int`)));
```

### Action

In `signalStack`, make each service exit cleanly a short time after its first
`SIGINT` and exit 1 on a second one, and in the terminal case wait until every
service has handled the first signal before emitting the stack's. Keep it in
"one terminal Ctrl-C signals each service once", which must fail when the
handler sends `SIGINT`.

## F2. No test fails when Ctrl-C exits are judged against `SIGTERM`

Low · Testing · Fix · Resolved · `cmd/ox/web/stack.test.mjs:156`

`stop(130, "SIGINT", false)` judges each exit against `SIGINT`, so a service
that dies on the terminal's `SIGINT`, as `ox` does, counts as a clean stop.
None of the tests notices when the handler judges against `SIGTERM` instead.
`signalStack`'s services exit 0, which passes against any signal. In "a service
that fails on Ctrl-C fails the stack", the extra `exited on SIGINT` lines from
`ox` and `vite` still leave the result at 1 and the `oxd` line present.

### Evidence

Changing the handler to `stop(130, "SIGTERM", false)` leaves every test
passing. Replacing the last assertion of "a service that fails on Ctrl-C fails
the stack" with
`assert.deepEqual(lines, [`${paths.oxd} exited with status 66`])` passes on the
change and fails under the mutation.

### Action

Assert in "a service that fails on Ctrl-C fails the stack" that the `oxd`
failure is the only line logged. The `ox` and `vite` services die on `SIGINT`,
so the test also guards that the stack accepts a service killed by the signal
it is judged against.

## F3. No test fails when the stack judges the exit that stopped it

Low · Testing · Fix · Resolved · `cmd/ox/web/stack.mjs:93`

The plan's decision "The exit that stops the stack" clears `record.proc` before
`stop`, so the service's own exit status stays the stack's result and is not
judged again as a stop. When the clearing is removed, a service that exits with
status 3 makes the stack log `… exited with status 3` and resolve 1 instead of
3. "an exit the stack did not cause stops the stack" kills `vite` with
`SIGKILL`, whose exit resolves 1 by either path, and checks only that the code
is nonzero.

### Evidence

Removing `record.proc = null` from the exit handler leaves every test passing.
This test passes on the change and fails under that mutation:

```js
const paths = await start({ traps: { vite: "trap 'exit 3' USR1" } });
process.kill(Number(pid(paths.vite)), "SIGUSR1");
assert.equal(await stack, 3);
assert.deepEqual(lines, []);
```

### Action

Rewrite "an exit the stack did not cause stops the stack" so the service exits
with a specific status. Assert that the stack resolves that status and logs
nothing, and keep its check that the other services exit.

## Checks run

- `node --test cmd/ox/web/stack.test.mjs` on the change: 13 of 13 pass.
- Mutations on a temporary copy, reverted:
  - Removing the 128-plus-signal clause fails the `stopProcess` test.
  - Resolving `stop` before `allSettled` fails five tests, including the new
    Ctrl-C failure test, both signal tests, and the restart-stop test.
  - Sending `SIGINT` from the handler passes every test (F1).
  - Judging Ctrl-C against `SIGTERM` passes every test (F2).
  - Removing `record.proc = null` from the exit handler passes every test (F3).
- The new Ctrl-C failure test resolves 130 on the base, per the plan, because
  the old `stop` resolved before any service exited.
- Each proposed test above passes on the change and fails under its mutation.
- `make test`, `make e2e`, and the real dev stack check on ports 9080/9081 (130
  on `SIGINT`, 143 on `SIGTERM`, no process left) are as the implementer
  reported. They were not rerun.

## Verification 1

Checked the uncommitted tree on `fix-f4` at base `312f3da`. The fixes change
only `cmd/ox/web/stack.test.mjs`, and `cmd/ox/web/stack.mjs` matches the
reviewed version. `node --test cmd/ox/web/stack.test.mjs` passed 13 of 13 on
three runs. Each mutation below ran on a temporary copy of `stack.mjs` against
the fixed tests. `make test` and `make e2e` (108 passed, 4 skipped) are as the
implementer reported, not rerun.

| ID | Status   | Evidence                                                                                                                                                                  |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `signalStack` models `oxd`'s graceful first and forced second `SIGINT` and waits for each service's first; `stop(130, "SIGINT", true)` fails one test, passes without it |
| F2 | Resolved | The Ctrl-C failure test asserts the `oxd` line is the only one; `stop(130, "SIGTERM", false)` fails one test                                                              |
| F3 | Resolved | "an exit the stack did not cause stops the stack" expects status 3 and no lines; removing `record.proc = null` from the exit handler fails one test                       |

Resolving `stop` before `allSettled` still fails six tests. No new findings.

## Verdict

Ready to commit. F1, F2, and F3 are resolved.
