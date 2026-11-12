# oxd keeps every session's events in its database

- Task: `2026-11-12-009-persist-session-events`
- Kind: plan
- Status: ready
- Source HEAD: `ddb1b273b492bd880310fd7d3880727afee215df`; executes on the
  commit of `2026-11-12-008-park-idle-sessions`
- Author: planner child of Hypercube session 945c2ca2, Claude Code, Opus 5.5
  (`claude-opus-5-5`); coordinator ID `planner`; native conversation ID
  `7eb96350-35c2-461c-9497-348302bd4237`
- Dependencies: `2026-11-12-008-park-idle-sessions` (its `agent_parked` event
  and `State` changes are the code this unit extends; nothing here needs
  parking). `2026-11-12-010-archive-sessions` relies on **Contract for later
  units**.

## Outcome and boundaries

Third of five units (request in `2026-11-12-007-bridge-background-work`). The
user's decision for archive: `oxd` persists every session's events as they
arrive, so every session's history, archived or not, survives `oxd` restarts
and does not depend on Claude's transcript.

Done means:

- each event a session records is written to `oxd`'s database without the
  event path waiting on SQLite;
- after an `oxd` restart, subscribing to a session delivers its whole history
  from the database, with the same `seq`s it had;
- `session.load` of an exited session resumes the agent; the bridge's replay
  is dropped because the history is already there;
- `session.delete` removes the session's events with it;
- known limit, recorded in `docs/architecture.md`: the database keeps every
  session's full history, including tool output and images, until the session
  is deleted;
- known limit, recorded in `docs/architecture.md`: continuing a conversation
  still needs Claude's transcript, so a session older than Claude's
  `cleanupPeriodDays` (default 30 days,
  https://code.claude.com/docs/en/data-usage#data-retention) is readable but
  cannot resume.

No migration and no compatibility path (AGENTS.md): a session saved before
this unit has an empty log, and its first load records the bridge's replay as
its history, by the rule that already applies to a session with no history.

Not in this unit: archive (010), the app (011), search.

## Current system

- `crates/oxd/src/state.rs`: `State { events: Vec<Event>, title,
  next_request, phase }`. `State::append` pushes an event with `seq =
  len + 1`. `State::restored` starts a saved session `Exited` with no events.
  `State::load` records the resume replay only when the history has no
  `agent_started`. `next_request` restarts at 1 for a restored session.
  `SessionClosed` is documented as the last event of a session.
- `crates/oxd/src/session.rs`: `Session::update` changes the state under its
  lock, publishes `last_seq` on a watch, writes title and agent session ID
  changes to the store inline, and signals the daemon.
- `crates/oxd/src/conn.rs`: `session.subscribe` checks `Session::subscribe`
  and starts `forward`, which reads `events_after` from memory.
- `crates/oxd/src/store.rs`: one `Mutex<Connection>`, small inline queries,
  `CREATE TABLE IF NOT EXISTS`, `PRAGMA foreign_keys = ON`, cascades from
  `sessions` to `worktrees`.
- `crates/oxd/src/main.rs`: at shutdown `close_all` records `Closed` on every
  session (a running turn ends with "the session was closed", subscriptions
  end with `session_closed`), then `run` returns while holding the startup
  lock, so a new daemon cannot open the database until this one exits.

## Design

### Table

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA auto_vacuum = FULL;   -- takes effect for a database created from now on
CREATE TABLE IF NOT EXISTS events (
    session_id TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    kind       TEXT    NOT NULL,  -- JSON of ox_protocol::EventKind
    PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;
```

One row per event, exactly as recorded, so `seq`s mean the same before and
after a restart. WAL with `synchronous = NORMAL`
makes each commit cheap and survives an `oxd` crash; a power loss can lose the
last commits but not corrupt the file. `auto_vacuum = FULL` shrinks the file
when a delete frees pages, for databases created after this unit.

### Writing without blocking the event path

- `Store` gains an event writer: an unbounded `std::sync::mpsc` channel and
  one dedicated thread, started by `Store::open`. `Store::log(events)` sends a
  batch and never blocks.
- `Session::update` sends the events its change appended, in order, while it
  still holds the state lock, so each session's events reach the channel in
  `seq` order.
- The thread blocks for the first message, drains whatever else is queued,
  and writes all of it in one transaction on the store's connection (taking
  the existing mutex for the length of the commit). Batches grow with load and
  no timer is involved. Rows use `INSERT INTO events SELECT ?, ?, ? WHERE
  EXISTS (SELECT 1 FROM sessions WHERE id = ?)`, so events still queued for a
  session that was just deleted are dropped rather than failing the foreign
  key. A failed write goes to `oxd`'s log and the session carries on, as title
  writes do.
- `SessionClosed` is never written: it ends streams, and a stored history
  continues after a restart. Every other event is.
- `Store::flush()` sends a marker with a `oneshot` and awaits its
  acknowledgement, which the thread sends after committing everything queued
  before it. `main.rs` calls it after `close_all`, while still holding the
  startup lock, so the next daemon reads a complete log. Events still queued
  when `oxd` is killed are lost; that is named in the docs.

### Reading the history

- `Store::sessions()` also returns each session's `MAX(seq)`, so a restored
  `State` knows `last_seq` without loading events. Events load lazily: a
  restored session's `State.events` is `None` until needed.
- `Session::history()` loads them once (`tokio::sync::OnceCell`), reading
  `Store::events(id)` in `spawn_blocking`, and fills `State`. A read error is
  `STORAGE`. Callers: `conn.rs` before `Session::subscribe`, and
  `Daemon`'s `session.load` before `State::load`. A new session's history is
  loaded by construction.
- Only a loaded history records events that are written: a restored session
  is `exited` and records nothing until it loads, and `Closed` on an unloaded
  session changes only its phase (no subscriber can exist without a load).
  So the lazy read never misses a queued write.
- While filling `State`, the log's open ends are closed, so `State`'s
  invariants hold after a crash: a `prompt_started` or `turn_started` without
  `turn_ended` gets `turn_ended { error: "oxd stopped during the turn" }`, and
  a `permission_requested` without `permission_resolved` gets a cancelled
  resolution. These events are appended and written like any other.
- `next_request` becomes one more than the largest numeric `request_id` in the
  log, so permission request IDs stay unique across restarts.
- Subscriptions and `last_seq` work from memory exactly as now once the
  history is loaded; `after_seq` checks use the restored `last_seq`.

### Load and delete

- `session.load`: unchanged apart from loading the history first. A session
  that started has `agent_started` in its log, so `State::load` drops the
  bridge's replay.
- `session.delete`: deleting the `sessions` row cascades to `events` (and to
  `worktrees` as now). Claude's transcript is untouched.

### Docs

`docs/architecture.md`: the event log, lazy loading, what survives a restart,
the growth and `cleanupPeriodDays` limits, and that events queued at a kill are lost.
`ox-protocol` docs for `session.subscribe` and `session.load` (history from the
log; the replay is used only for a session with no history).

## Contract for later units

- Every session's history is in the database and loads on subscribe; it does
  not need an agent or Claude's transcript.
- `Session::history().await` loads it; `session.load` still starts the agent.
- Deleting a session removes its events.

## Check your assumptions

Settled by the user and built in:

- `oxd` persists every session's events as they arrive; archive stores
  nothing of its own (010); no migration.
- Database growth is accepted for now and recorded as a known limit.

## Implementation sequence

1. `store.rs`: pragmas, `events` table, the writer thread and channel,
   `log`, `flush`, `events(id)`, `MAX(seq)` in `sessions()`; unit tests.
2. `state.rs`: `events: Option<Vec<Event>>` with restored `last_seq`;
   `fill(history)` closing open ends and setting `next_request`;
   `SessionClosed` marked as not logged; `append` returns what to write.
3. `session.rs`: `history()` with `OnceCell`; `update` sends new events to
   `Store::log`.
4. `conn.rs`: await `history()` before subscribing. `daemon.rs`: await it in
   `session.load`.
5. `main.rs`: `flush()` after `close_all`.
6. Docs.

## Verification

- `store.rs` unit tests: events round-trip in order; a batch for a deleted
  session is dropped; deleting a session removes its events; `flush` returns
  after earlier batches are committed.
- `state.rs` unit tests: `fill` closes an open turn and a pending permission;
  `next_request` continues after the log's largest ID.
- `crates/oxd/tests/daemon.rs`: a session's events, after a daemon restart
  (`Daemon::again`), arrive from `after_seq` 0 with the same `seq`s and kinds,
  ending without `session_closed`; a subscription from a middle `seq` gets the
  rest; `load` after restart adds no replay and the next prompt continues the
  `seq`s; a daemon killed mid-turn (SIGKILL) and restarted shows the turn
  ended with the `oxd stopped` error; delete then restart: the session and its
  events are gone.
- Undo checks: dropping the `flush` call makes the restart test lose the
  shutdown's `turn_ended`; dropping `fill`'s closing step fails the SIGKILL
  test.
- `scripts/smoke-acp.py` `check_load_after_restart` now also checks that the
  conversation after restart has the earlier turns' text with their original
  `seq`s.

Commands: `make check-rust`, `make smoke-claude`.

## Handoff

Ready. Work size: a store writer thread, lazy history
loading, crash-consistency of the log, and daemon tests; a normal assignment.
`plan_metrics.py`: 1460 words, inside the band.
