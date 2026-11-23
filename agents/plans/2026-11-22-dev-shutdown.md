# The dev stack sends every stop signal its services get

## Goal

One Ctrl-C under `make dev` stops `oxd` gracefully, every time. Today the
services share the terminal's process group, so a Ctrl-C reaches each service
directly, and the stack's SIGINT handler stops without signalling them. When a
service's exit reaches the stack before its own SIGINT, the exit handler runs
`stop(code ?? 1)`, which sends SIGTERM to `oxd`, and `oxd` takes it as a second
signal and exits at once. After the change, the stack is the only sender of its
services' signals, so a service cannot receive a signal the stack did not decide
to send. Resolves [#29](https://github.com/kkestell/ox/issues/29), finding F4 of
the 2026-11-22 deslop audit.

## Related code

- `cmd/ox/web/stack.mjs` — `runStack`: `start` spawns each service, its `exit`
  handler stops the stack on an exit it did not cause, `stop(code, signal, send)`
  signals each current process unless `send` is false, and the `signals`
  handlers map SIGINT to `stop(130, "SIGINT", false)` and SIGTERM to
  `stop(143)`.
- `cmd/ox/web/dev.mjs` — the only caller; passes `process` as `signals`.
- `cmd/oxd/main.go:86-105` — the first SIGINT or SIGTERM starts graceful
  shutdown; a second one kills Claude and exits 1.
- `cmd/ox/web/stack.test.mjs` — stand-in services that log each INT and TERM
  they receive; `signalStack` simulates the terminal by signalling every service
  before the stack.

## Decisions

- **Construction:** `start` spawns each service with `detached: true`, which on
  macOS and Linux puts it in a new session and process group. A terminal Ctrl-C
  then reaches only the stack, and the stack decides every signal in one place.
  No ordering between the terminal's signal and a service's exit exists to
  race, so the stack needs no inference about why a service exited. `stop`
  always signals: the `send` parameter goes, and `retire` always calls
  `stopProcess`.
- **Signals the stack handles:**
  - SIGINT, first: `stop(130, "SIGINT")`, one SIGINT to each service.
  - SIGINT while stopping: send SIGINT to each record's current process. This
    keeps **Stopping oxd**'s "a second stop request ends it at once" under
    `make dev`, which the shared process group used to provide. `oxd` exits 1,
    so `exited` rejects it, the stack logs its status, and resolves 1.
  - SIGTERM: `stop(143)`, unchanged.
  - SIGHUP: `stop(129)`, SIGTERM to each service. Services no longer receive the
    terminal's hangup, so without this a closed terminal would leave `oxd`
    holding the socket and the database.
- **Rare cases:** if node itself is killed with SIGKILL or crashes, the services
  keep running, as they do today. Ctrl-Z suspends only the stack.
- **Requirements:** **Stopping oxd** holds under `make dev`: one Ctrl-C stops
  `oxd` gracefully, and a second ends it at once. No page-visible behavior
  changes.
- **Settling:** no new waits. `stop` waits on the services it signalled, as
  before.

## Check your assumptions

None.

## Test plan

All in `cmd/ox/web/stack.test.mjs`, run by `make test`.

- `signalStack(signal, received)` drops the terminal broadcast and takes the
  signal each service should log, then waits with `until` for every service to
  exit before awaiting the stack, so a stack that sends nothing fails with
  "timed out" rather than hanging.
- "one terminal Ctrl-C signals each service once" becomes "Ctrl-C to the stack
  signals each service once": `signalStack("SIGINT", "INT")` resolves 130. It
  fails on the old code, which sends nothing on SIGINT.
- "SIGTERM to the stack signals every service" uses `signalStack("SIGTERM",
  "TERM")`, unchanged in effect.
- New "a closed terminal stops every service": `signalStack("SIGHUP", "TERM")`
  resolves 129. It fails on the old code, which has no SIGHUP handler.
- New "a terminal Ctrl-C reaches only the stack": each service's PID names its
  own process group (`process.kill(-pid, 0)` succeeds). This is the property
  that removes the race; it fails on the old code with ESRCH.
- New "a second Ctrl-C forwards SIGINT": with the `signalStack` stand-ins, emit
  SIGINT once, wait until every service has its `.int` file, emit SIGINT again;
  each service logs `INT\nINT\n`, all exit, and the stack resolves 1. It fails
  on the old code.
- "a service that fails on Ctrl-C fails the stack" stops signalling the
  services itself and only emits SIGINT on the stack.

## Implementation plan

1. `cmd/ox/web/stack.mjs`: spawn with `detached: true`; remove `send` from
   `stop` and `retire`; add the second-SIGINT forwarding and the SIGHUP handler;
   rewrite the `runStack` comment's last paragraph to say the services run in
   their own process groups and the stack sends every signal they receive.
2. `cmd/ox/web/stack.test.mjs`: the test changes above.

## Requirement changes

None.
