# oxd parks idle, unviewed sessions

- Task: `2026-11-12-008-park-idle-sessions`
- Status: complete
- Plan: `.agents/plans/2026-11-12-008-park-idle-sessions.md`

## Changes

As the plan's design.

- `crates/ox-protocol/src/messages.rs`: `EventKind::AgentParked`.
- `crates/oxd/src/state.rs`: `background` on `Live` and on `Phase::Starting`
  (from the resume snapshot), `quiet()`, `State::quiet`, `Change::Parked`
  with unit tests.
- `crates/oxd/src/session.rs`: viewer count and `Viewer` guard
  (`Session::view`), `Parkable` watch published in `update`, `stop(change)`,
  the park task started in `new` and aborted in `close`.
- `crates/oxd/src/conn.rs`: `forward` holds the viewer guard.
- `crates/oxd/src/main.rs`, `daemon.rs`: `--park-after-ms` (default 15 min),
  passed to `Session::spawn`/`restore`.
- `crates/ox/src/conversation.rs`, `crates/oxctl/src/main.rs`: handle the new
  event (no item; "agent parked").
- `crates/oxd/src/bin/oxd-fake-agent.rs`: `--background`, `background-start`,
  `background-end`.
- `crates/oxd/tests/daemon.rs`: parking tests (parks and agent exits, loads
  again, viewed, background work, no background reporting, busy restarts the
  delay, daemon exits after parking).
- `scripts/smoke-acp.py`: `check_parking` with a `Watch` reading the daemon
  socket directly (oxctl has no watch command), run on its own `oxd`.
- `docs/architecture.md`, `docs/acp-v2.md`.

Changed paths: the above, plus `.agents/work/2026-11-12-008-park-idle-sessions.md`.

## Deviations

- `Change::Parked` on a `Live` session ends what is open (`live.end`) before
  `agent_parked`, instead of assuming quiet. A prompt can land between the
  park task's re-check and the kill; this keeps each `PromptStarted` followed
  by a `TurnEnded`. For a quiet session the events are the same.
- The park task also tracks a `lapses` counter beside the parkable flag, so a
  not-parkable period that ends before the task observes it still restarts the
  delay (a watch coalesces values).
- The plan's `README.md` item: README has no text on session states or `oxd`
  flags, so unchanged. `oxctl` needed an arm for the new event (not named in
  the plan).

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-rust` | passed | `/tmp/008-check-rust.log` |
| `make check-ui` | passed | `/tmp/008-check-ui.log` |
| `make e2e` | passed | `/tmp/008-e2e.log` |
| `make check-scripts` | passed | `/tmp/008-check-scripts.log` |
| `make smoke-claude` | passed, including `idle_session_with_background_work_parks_after_it` | `/tmp/008-smoke.log` |
| undo checks: `forward` without its guard; `quiet()` accepting any reported list | `a_viewed_session_is_not_parked` and two state unit tests failed; restored | run by hand |

## Limitations

- Work Claude starts with nothing listed can begin between the park task's
  re-check and the kill (named in the plan).
- A parked session wakes only by `session.load`.
- After the park delay, the park task waits for the lifecycle lock and then
  checks only that the session is parkable, not that it stayed so. A turn
  that starts and ends while a refused `session.delete` holds the lock lets
  the session park right after it (review R1, accepted by the user).
