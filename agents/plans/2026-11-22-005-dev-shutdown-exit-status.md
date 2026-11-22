# Judge every service exit before the dev stack returns its status

## Goal

A terminal Ctrl-C or `SIGTERM` to `make dev` resolves the stack's result at
once, before any service has exited, so an `oxd` that exits with the race
detector's status 66 during shutdown leaves the stack reporting 130 like an
ordinary Ctrl-C. Rebuilds already judge the exit of the process they retire.
After the change, the stack judges every process it stops, on a rebuild or a
shutdown, by one rule. It resolves only once all of them have exited, logs each
failed exit, and resolves 1 when any failed. A clean shutdown keeps 130 or 143.

From [Audit F4](../audits/2026-11-22-001-conversation-launch-and-layout.md#f4-the-dev-stack-decides-its-exit-status-before-services-stop),
listed in `docs/todo.md` as **Dev shutdown masks daemon exit failures**.

## Related code

- `cmd/ox/web/stack.mjs` — `stopProcess` signals a process and judges its exit;
  `runStack`'s `stop` resolves the result immediately and its `restart` judges
  a retiring process on its own path.
- `cmd/ox/web/dev.mjs` — runs `oxd`, `ox`, and Vite through `runStack` and uses
  the result as its exit status. Unchanged.
- `cmd/ox/web/e2e/services.ts` — also calls `stopProcess` for e2e teardown.
- `cmd/ox/web/stack.test.mjs` — stack tests with shell-script services.
- `cmd/oxd/main.go:88–108` — `oxd` exits 0 after a graceful stop on its first
  `SIGINT` or `SIGTERM`, and 1 on a second signal.

## Decisions

- **One judge.** `exited(proc, signal)` waits for `proc` to exit without
  signalling it. It resolves on status 0, on `signal` received, or on status
  128 plus `signal`'s number, and otherwise rejects with today's messages.
  `stopProcess(proc, signal)` sends `signal` unless `proc` has exited and
  returns `exited(proc, signal)`. Vite handles `SIGTERM` and exits with status
  143, observed with a probe; `oxd` exits 0 and `ox` dies on the signal. The
  race detector's 66 stays a failure.
- **One record of stops.** `runStack` keeps `stops`, the judged exits of every
  process it stops. `retire(proc, signal, send)` adds `send ? stopProcess(proc,
  signal) : exited(proc, signal)` to it and returns it. `restart` retires the
  old process through `retire`; on rejection it calls `stop(1)` without logging.
  `stop(code, signal = "SIGTERM", send = true)` sets `stopping`, closes input,
  retires each record's current process, awaits `Promise.allSettled(stops)`,
  logs each rejection's message, and resolves `1` if any rejected, else `code`.
  Each failure is logged once, by `stop`.
- **Signals.** The `SIGINT` handler calls `stop(130, "SIGINT", false)`: the
  terminal has delivered `SIGINT` to every service, so the stack sends nothing
  and judges against `SIGINT`. `SIGTERM` calls `stop(143, "SIGTERM")`.
- **The exit that stops the stack.** A service's own exit or spawn error clears
  `record.proc` before calling `stop`, so the shutdown does not judge the exit
  that caused it; that exit's status stays the stack's result, as today.
- **Ctrl-C during a restart.** The retiring process receives the restart's
  `SIGTERM` and the terminal's `SIGINT`. `oxd` takes the second signal as a
  forced stop and exits 1, which the stack reports as a failure. That is a real
  forced exit, and the rule stays as stated.
- **Construction.** The stack's result has one source: `stop`, after every
  process the stack stopped has exited. No path resolves it earlier.
- **Settling.** `stop` waits on each process's exit. That wait is not new: Node
  already stays alive until its spawned children exit. A process that ignores
  its signal holds the stack as it holds Node today.
- **Process I/O.** Services keep inheriting stdout and stderr, so a race report
  reaches the terminal; the stack adds `<command> exited with status N` for each
  failed stop.
- **Ownership.** `stops` is owned by `runStack`, needed so a failed shutdown
  exit is reported. It grows by one per restart for the life of the dev
  session, and no process is added after `stopping` is set, because `rebuild`
  checks `stopping` before each restart and `retire` runs synchronously.

## Check your assumptions

None.

## Test plan

All in `cmd/ox/web/stack.test.mjs`, run by `make test`.

- New test, **a service that fails on Ctrl-C fails the stack**: start with
  `oxd` trapping `INT` to `exit 66`, send `SIGINT` to every service, emit
  `SIGINT`; the stack resolves 1 and `lines` holds `${paths.oxd} exited with
  status 66`. It resolves 130 before the change. No existing test sets a
  failing trap on shutdown.
- `signalStack`: replace the `until` after `await stack` with an immediate
  assertion that every service has exited, so both signal tests check that the
  result comes after the services stop. Each still expects 130 or 143.
- **stopping during a restart starts no new process**: assert the retiring
  process has exited immediately after `await stack`, instead of `until`.
- **a retiring service that fails stops the stack**: assert the failure line
  appears exactly once.
- **stopProcess accepts only status 0 or the signal it sent**: add a service
  trapping `TERM` to `exit 143`, which `stopProcess` resolves, and rename the
  test to say it also accepts 128 plus the signal's number.

## Implementation plan

1. `cmd/ox/web/stack.mjs`: add `exited` and rebuild `stopProcess` on it; add
   `stops`, `retire`, and the new `stop`; route `restart` through `retire`;
   clear `record.proc` in the exit and error handlers before `stop`; change the
   `SIGINT` handler to `stop(130, "SIGINT", false)`. Update the comments on
   `stopProcess` and `runStack` to describe the result as judged after every
   stopped service exits.
2. `cmd/ox/web/stack.test.mjs`: the test changes above.
3. Check the real stack: after `make web build`, start
   `node cmd/ox/web/dev.mjs` with `OX_VITE_PORT=9080 OX_RELAY_PORT=9081` and
   `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_RUNTIME_DIR` set to an empty
   temporary directory, in its own process group. Send `SIGINT` to the group,
   and confirm status 130 with no service left; repeat with `SIGTERM` to the
   node process alone and confirm 143.
4. `docs/todo.md`: remove the entry.

## Requirement changes

None.

## Documentation updates

- `docs/todo.md`: remove **Dev shutdown masks daemon exit failures**.
