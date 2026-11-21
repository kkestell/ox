# Store reply text at once, own the database, and stop the dev stack cleanly

## Goal

Four findings from the
[deslop audit](../audits/2026-11-20-002-deslop.md), listed in `docs/todo.md`:

- [F1](../audits/2026-11-20-002-deslop.md#f1-reply-text-shown-live-is-lost-on-an-abrupt-oxd-exit):
  reply text shown live sits in a buffer until the next non-text update, so a
  killed `oxd` loses it. After the change every `text_delta` is in the database
  before the session goroutine handles its next event, and **Surviving
  restarts** holds for a killed `oxd` as written.
- [F2](../audits/2026-11-20-002-deslop.md#f2-one-ctrl-c-under-make-dev-forces-oxd-past-graceful-shutdown):
  a terminal Ctrl-C under `make dev` reaches `oxd` as `SIGINT` from the terminal
  and `SIGTERM` from the stack, and the second signal forces it out. After the
  change one Ctrl-C delivers one signal and `oxd` shuts down as **Stopping oxd**
  describes; a second Ctrl-C ends it at once.
- [F6](../audits/2026-11-20-002-deslop.md#f6-only-the-socket-not-the-database-has-one-owner):
  a second `oxd` on another socket opens the same database and repairs and
  archives the first one's sessions. After the change the store holds an
  exclusive lock from `store.Open` to `Close`, and the second daemon is refused
  before it reads anything.
- [F7](../audits/2026-11-20-002-deslop.md#f7-a-race-report-from-a-stopping-oxd-is-lost-in-e2e-and-dev-restarts):
  e2e and dev restarts ignore the exit status of the `oxd` they stop. After the
  change a stopped process that exits other than with status 0 or on the signal
  it was sent fails the e2e test or stops the dev stack.

## Related code

- `internal/server/history.go` — `history.pending` merges consecutive
  `text_delta` fragments in memory; `flush` writes them on the next other
  update; `entries` appends the unwritten tail to what the store returns.
- `internal/server/session.go:401` — `sess.history.record(update)` on the
  session goroutine for every update; `methods.go:411` and `mcp.go:458` read
  `history.entries()` for `session/get` and `read_session`.
- `internal/store/store.go` — `Open` builds the DSN with `journal_mode(WAL)` and
  one connection; `AddEvent`, `Events`, `LastEvent`.
- `cmd/oxd/main.go` `run` — takes the socket, opens the store, loads the server
  (whose `newSession` runs `recordEnd`, the history repair); its comment says
  the socket protects that repair. A second signal calls `claude.KillAll()` and
  `os.Exit(1)`.
- `cmd/oxd/main_test.go` `TestDaemonOwnsSocketUntilShutdown` — runs two real
  daemons on one socket and database; opens the database with `store.Open`
  while the first daemon runs to check its history.
- `cmd/ox/web/stack.mjs` — `runStack` spawns services in the terminal's process
  group; `stop(code)` sends `SIGTERM` to each; `restart` awaits `exited(old)`
  and ignores the status.
- `cmd/ox/web/dev.mjs` — installs `SIGINT` → `stack.stop(130)` and `SIGTERM` →
  `stack.stop(143)`.
- `cmd/ox/web/stack.test.mjs` — stack tests with shell-script services.
- `cmd/ox/web/e2e/services.ts` — `stopProcess` resolves on `exit` without the
  status; `restartDaemon` and `stop` use it.
- `cmd/ox/web/e2e/session.spec.ts:599` — opens the database with
  `DatabaseSync` while `oxd` runs; unaffected by a lock file.

## Decisions

- **F1: write every fragment as it arrives.** `history.record` appends each
  `text_delta` to the store like any other recorded update, and `history` keeps
  no in-memory text. `entries` merges consecutive stored `text_delta` updates
  with the same `message_id` into one, with the first fragment's time, so
  `session/get` and `read_session` return one entry per reply as now. The store
  becomes the one source of the history; `pending`, `flush`, and the tail in
  `entries` go. Merging at read also joins fragments separated by an unrecorded
  update such as `state`, which matches how the page joins them live
  (`ox.tsx` appends a `text_delta` to the last reply entry).
- **F1: `synchronous(NORMAL)`.** `store.Open` adds `_pragma=synchronous(NORMAL)`
  to the DSN. In WAL mode a committed write then survives the process being
  killed, which is the guarantee **Surviving restarts** needs, and a commit no
  longer waits on `fsync`, which keeps a write per fragment off the session
  goroutine's critical path. Only a power loss can drop the latest commits, and
  the database stays consistent.
- **F2: the stack knows which signal the terminal already delivered.**
  `runStack` takes `signals`, an event emitter; `dev.mjs` passes `process` and
  installs no handlers of its own. On `SIGINT` the terminal has already sent
  `SIGINT` to every service in the foreground process group, so the stack stops
  without signalling them and resolves 130. On `SIGTERM`, which comes from
  `kill` rather than the terminal, it signals each service as `stop` does and
  resolves 143. Services stay in the terminal's process group, so closing the
  terminal still ends them, and a second Ctrl-C reaches `oxd` directly and ends
  it at once, as **Stopping oxd** says. A service that dies from the same
  `SIGINT` (the relay has no handler) cannot trigger `stop`'s `SIGTERM` first:
  the kernel sends `SIGINT` to node with the group, the service's exit reaches
  node as `SIGCHLD` afterwards, and libuv dispatches both through one signal
  pipe in delivery order.
- **F6: the store owns the database exclusively.** `store.Open` opens
  `<path>.lock` (created with mode `0o600`, never removed) and takes
  `flock(LOCK_EX|LOCK_NB)` before opening SQLite; `EWOULDBLOCK` returns
  `another oxd is using <path>`. `Store` keeps the lock file and `Close` closes
  it after the database. A `Store` therefore exists only while its process owns
  the database, and no caller checks. A lock file is used because SQLite's own
  locks on the database file are POSIX locks that a `flock` on the same file
  conflicts with on macOS, and the directory holds the socket on macOS, where
  `listen` also takes a directory `flock`. The kernel drops the lock when the
  process dies, so a killed `oxd` never blocks the next start. **Ox's own
  files** names "its database"; the lock file sits beside it as SQLite's `-wal`
  and `-shm` files do, and the requirement covers it unchanged.
- **F6: `run`'s order.** `run` still takes the socket first, then opens the
  store, then loads the server. The comment moves the repair's protection to
  the database lock; the socket is held until shutdown for **Socket safety**.
  A daemon refused by the lock closes its own socket on return.
- **F7: one rule for a stopped process, in `stack.mjs`.** `stack.mjs` exports
  `stopProcess(proc, signal = "SIGTERM")`: it sends `signal` unless the process
  has already exited, and resolves once it exits with status 0 or on `signal`,
  or rejects with `<spawnfile> exited with status <code>` or `<spawnfile> exited
  on <signal>`. A process that had already exited is judged by its recorded
  `exitCode` and `signalCode`. `restart` uses it: a rejection logs its message
  and stops the stack with status 1 instead of starting the replacement.
  `services.ts` imports it for `restartRelay`, `restartDaemon` (with the given
  signal, so `SIGKILL` is expected), and `stop`; its own `stopProcess` goes.
  `e2e/tsconfig.json` gains `"allowJs": true` so `tsc -p e2e` resolves the
  import. `stop` keeps its check that `oxd` did not exit during the tests.
- **Requirements:** **Surviving restarts** and **One transcript** hold for a
  killed `oxd`: the page and a reload after the restart show the same reply
  text, since `session/get` reads only the store. **Stopping oxd** holds under
  `make dev`. **One transcript**, **Every page in sync**, and **Archived
  sessions** hold against a second daemon, which never loads the database.
  **Socket safety** is unchanged.
- **Settling:** no new waits in `oxd`. `stopProcess` waits on the process's
  exit, as before.
- **Construction:** the store is the single source of a session's history; a
  `Store` value is proof of exclusive ownership of its database.
- **Ownership:** the lock file's descriptor is owned by `Store`, opened in
  `Open` and closed in `Close`. User-visible need: one daemon per database
  (**One transcript**, **Archived sessions**). Races: two starters race only on
  `flock`, which the kernel serializes. Cleanup: `Close`, or the kernel on
  exit; the file stays and is reused. The commit body repeats this.
- **Trust boundaries:** the lock file is created in the database's directory,
  which `store.Open` already creates with mode `0o700`.

## Check your assumptions

None.

## Test plan

- **Reply text survives a kill**, server layer, in
  `TestHistoryMergesTextAndKeepsConversation` (`history_test.go`): the
  mid-reply check reads a fresh `history` on the same store, as after a kill,
  and expects the merged `hello` timed by its first fragment. Fails before the
  change, since the fresh history has no pending tail. Add a `state` update
  between two fragments of one message and expect them merged; the later
  checks (`prompt:hi,text_delta:hello,result:,text_delta:tick,parked:`) stay.
- **A second daemon on another socket is refused**, daemon layer, in
  `TestDaemonOwnsSocketUntilShutdown`, renamed
  `TestDaemonOwnsSocketAndDatabaseUntilShutdown`: beside `refusesSecond`, a
  daemon on a second socket in the same directory with the same database fails
  with `another oxd is using` while the first serves and while it stops. Fails
  before the change: the second daemon starts and is killed at the deadline.
  The history checks move to after `oxd` exits, opening the store once then
  and expecting `prompt,exited,parked`; the check before the first signal goes.
- **One terminal Ctrl-C signals each service once**, in `stack.test.mjs`, new
  test, since no existing test passes signals: `start` accepts `signals` and
  service bodies; each service traps `INT` and `TERM`, appending the name to a
  log and exiting. The test sends `SIGINT` to each service's PID, emits
  `SIGINT` on the emitter, awaits the stack (130), and expects every log to be
  `INT` alone. Fails before the change: the stack ignores the emitter and never
  resolves.
- **`SIGTERM` to the stack signals every service**, the same new test's second
  case: emitting `SIGTERM` resolves 143 and every log reads `TERM`.
- **A retiring service that fails stops the stack**, in `stack.test.mjs`, new
  test beside `an exit after a restart stops the stack`, which kills the
  replacement instead: `oxd` changed, its body traps `TERM` with `exit 66`; after
  Enter the stack resolves nonzero, `restarted oxd` is not logged, and no new
  `oxd` PID appears. Fails before the change.
- **`stopProcess` rejects a bad exit and accepts the expected one**, in the
  same file: a service that exits 66 on `TERM` rejects with `status 66`; one
  sent `SIGKILL` resolves. This guards the e2e use, which has no cheaper test.

## Implementation plan

1. `internal/store/store.go`: add the lock file to `Store` and `Open`, closed
   in `Close` after the database; add `synchronous(NORMAL)` to the DSN.
2. `internal/server/history.go`: drop `pending` and `flush`; record
   `text_delta` with the other recorded types; merge consecutive same-message
   `text_delta` entries in `entries`.
3. `internal/server/history_test.go`: the F1 assertions above.
4. `cmd/oxd/main.go`: rewrite `run`'s comment for the database lock and the
   socket.
5. `cmd/oxd/main_test.go`: the F6 assertions and the moved history check.
6. `cmd/ox/web/stack.mjs`: export `stopProcess`; use it in `restart`; take
   `signals` and handle `SIGINT` and `SIGTERM`; drop `exited`.
7. `cmd/ox/web/dev.mjs`: pass `signals: process`; remove its handlers.
8. `cmd/ox/web/stack.test.mjs`: the F2 and F7 tests above.
9. `cmd/ox/web/e2e/services.ts` and `cmd/ox/web/e2e/tsconfig.json`: import
   `stopProcess` from `../stack.mjs`; remove the local one; add `allowJs`.
10. Documentation updates below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`: the `cmd/oxd` row holds the socket until shutdown
  without claiming it protects database startup; the `store` row says the store
  holds the database exclusively, through a lock file beside it, from opening to
  closing; line 95 says the session writes each conversation event to the store
  as it arrives, instead of owning a history buffer.
- `docs/protocol.md` (`session/get`): "Consecutive `text_delta` fragments are
  recorded as one" becomes "The history returns consecutive `text_delta`
  fragments of one message as one."
- `docs/todo.md`: remove the entries for F1, F2, F6, and F7.
