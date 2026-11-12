# oxd keeps every session's events in its database

- Task: `2026-11-12-009-persist-session-events`
- Status: complete
- Plan: `.agents/plans/2026-11-12-009-persist-session-events.md`

## Changes

Implemented as the plan describes.

- `crates/oxd/src/store.rs`: WAL, `synchronous = NORMAL`, `auto_vacuum = FULL`; `events` table; writer thread with `log`, `flush`, `events(id)`; `SavedSession.last_seq` from `MAX(seq)`; store unit tests.
- `crates/oxd/src/state.rs`: `events: Option<Vec<Event>>` plus `last_seq`; `fill` closes open turns and permission requests and sets `next_request`; `take_unlogged` (excludes `SessionClosed`); `loaded`; unit tests.
- `crates/oxd/src/session.rs`: `history()` (`OnceCell`, `spawn_blocking`, `STORAGE` on error); `update` queues new events with `Store::log`; restored sessions start with their stored last seq.
- `crates/oxd/src/conn.rs`, `daemon.rs`: `session.subscribe` and `session.load` await `history()`; `Daemon::flush`.
- `crates/oxd/src/main.rs`: `flush()` after `close_all`.
- `crates/oxd/tests/daemon.rs`: restart history with same seqs, mid-seq subscribe, load adds no replay and seqs continue; SIGTERM mid-turn; SIGKILL mid-turn; delete then restart. Replaced `loading_a_restored_session_replays_its_conversation`; adjusted the busy-on-resume test (history now has the first `agent_started`).
- `scripts/smoke-acp.py`: `check_load_after_restart` reads the stored history with its original seqs, then checks load records no replay.
- `docs/architecture.md`, `crates/ox-protocol/src/messages.rs` (doc comments): event log, limits, load/subscribe semantics.

Changed paths: the files above.

## Deviations

- The plan's `append returns what to write` is a `State::take_unlogged` drain that `Session::update` calls, rather than a return value, since `record` and `request` both append.
- `session.load` also loads history first for sessions that never started (a no-op: they are loaded by construction).

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-rust` | passed (second run; first run failed once on `claude_exiting_fails_the_tool_calls_of_its_background_work` in `claude-code-acp`, an untouched crate, which passed 3 times alone and in the rerun) | `/tmp/check-rust.log` |
| `make check-scripts` | passed | terminal |
| `make smoke-claude` | passed (Haiku) | `/tmp/smoke-claude.log` |

`make check-ui` and `make e2e` were not run: no TypeScript or `crates/ox` change.

Undo checks: skipping `fill`'s closing of the turn fails `a_killed_daemon_leaves_a_history_that_ends_its_turn`. Dropping `flush()` does not fail `a_stopped_daemon_stores_the_end_of_the_turn_it_interrupted`: the writer commits before the process exits in practice.

## Limitations

- No test fails without `Daemon::flush`; the shutdown flush is unverified by a failing test.
- The SIGKILL test polls the database read-only (`wait_for_stored`) until the request event is stored, since `oxd` announces no commit.
- Events queued when `oxd` is killed are lost (documented).

## Repair round 1

- Findings addressed: R2. `a_killed_daemon_leaves_a_history_that_ends_its_turn` and its polling `wait_for_stored` and `kill` helpers are gone. `a_history_that_a_crash_left_open_ends_its_turn` creates a session through the ordinary interfaces, stops the daemon, inserts a `turn_started` and an unanswered `permission_requested` into the `events` table, restarts the daemon, and checks through `session.subscribe` and `session.load` that the history ends with the cancelled resolution and `turn_ended` ("oxd stopped during the turn") and that the next event continues the seqs. R1 and R3 were dropped by the user; the missing shutdown-flush test stays in Limitations.
- Deviation: the plan asked for the production `Store` API, but `oxd` is a binary crate, so the test inserts the rows with `rusqlite` directly into the table the daemon wrote.
- Changed paths: `crates/oxd/tests/daemon.rs`.
- Verification: `make check-rust` passed (`/tmp/check-rust-2.log`). Undo check: disabling `fill`'s closing of the turn fails the new test.
- The SIGKILL limitation above no longer applies; no test kills the daemon.
