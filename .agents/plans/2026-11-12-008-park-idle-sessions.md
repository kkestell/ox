# oxd parks a session whose agent has been idle and unviewed for 15 minutes

- Task: `2026-11-12-008-park-idle-sessions`
- Kind: plan
- Status: ready
- Source HEAD: `ddb1b273b492bd880310fd7d3880727afee215df`; executes on the
  commit of `2026-11-12-007-bridge-background-work`
- Author: planner child of Hypercube session 945c2ca2, Claude Code, Opus 5.5
  (`claude-opus-5-5`); coordinator ID `planner`; native conversation ID
  `7eb96350-35c2-461c-9497-348302bd4237`
- Dependencies: `2026-11-12-007-bridge-background-work`.
  `2026-11-12-010-archive-sessions` relies on **Contract for later units**.

## Outcome and boundaries

Second of five units (see `2026-11-12-007-bridge-background-work` for the
request). Parking, as settled in plans 003–006: the agent is stopped; the
conversation, worktree and branch are kept; `session.load` resumes it.

Done means:

- `oxd` stops a session's agent once the session has been parkable for the
  park delay, continuously: agent live and idle, no permission pending,
  `background` reported as `[]`, and no subscription to the session;
- the delay is `oxd --park-after-ms MS`, default 900 000 (15 min);
- the history records `agent_parked`; the session is `exited`; no stream ends;
- a parked session loads like any exited one.

Not in this unit: archive, any app change beyond handling the new event, waking
on prompt.

## Contract relied on from 007

`claude-code-acp` reports `background` on every state update, starting with the
snapshot after `session/new` or `session/resume`. `idle` with `[]` means no
foreground work, no live background task and no known scheduled prompt.

## Current system

- `crates/oxd/src/state.rs`: `Phase::{Starting, Live, Exited, Closed}`;
  `Live { agent, turn, pending }`; `Live::follow` applies `state_update`s and
  ignores their `background`. `Phase::Starting` tracks `busy` from the resume
  snapshot. `Change::Exited(None)` from `Live` gives `agent_exited`.
- `crates/oxd/src/session.rs`: `Session::update` recomputes `SessionInfo`
  under the state lock and signals the daemon's `changed`. `stop()` aborts the
  agent task (dropping `AcpAgent` kills its process group) and records
  `Exited(None)`. `lifecycle` (`tokio::sync::Mutex<()>`) is held while a
  session starts in its worktree, loads, or is deleted.
- `crates/oxd/src/conn.rs`: each `session.subscribe` runs a `forward` task,
  ended by `unsubscribe` or the connection's end; `Client` sends `unsubscribe`
  when a `Subscription` drops (`ox-protocol/src/client.rs`), so the app's
  view, which subscribes to the selected session only, ends when the selection
  changes.
- `crates/oxd/src/main.rs`: `--idle-exit-ms`; `Daemon::idle` exits once no
  client is connected and every session is `exited`.
- `crates/ox/src/conversation.rs` turns events into conversation items; it
  matches `EventKind` exhaustively.
- `crates/oxd/src/bin/oxd-fake-agent.rs` reports no `background`.

## Design

### State

- `Live` gains `background: Option<Vec<BackgroundWork>>`, set from
  `StateUpdate::background()` on every state update, including repeated ones.
  `Phase::Starting` keeps the resume snapshot's list beside `busy` and hands it
  to `Live`. A list change that changes nothing else records the update event
  as now.
- `Live::quiet()`: no turn, no pending permission, `background == Some([])`.
  An agent that does not report the list (`None`) is never quiet.
- New `Change::Parked`: from `Live`, it gives `Exited(Some(agent))` and the
  event `EventKind::AgentParked`. From any other phase it is ignored. It is
  only ever applied to a quiet session, so it ends no turn and resolves no
  permission.

### Viewers and parkability

- `Session` counts viewers. `Session::view()` returns a guard that increments
  the count and decrements it on drop, each through `update` so parkability is
  recomputed. `conn.rs` `forward` takes the guard for its lifetime; `watch`
  does not. Any subscription counts: the app's open conversation, and any
  `oxctl` command that follows events.
- `Session::update` also publishes `parkable = live && quiet && viewers == 0`
  on a `watch::Sender<bool>` owned by the session.

### The park task

`Session` starts one park task when it is created (spawn or restore), with the
daemon's `park_after`, and aborts it in `close()`:

```rust
loop {
    parkable.wait_for(|p| *p).await;
    tokio::select! {
        () = tokio::time::sleep(park_after) => {}
        _ = parkable.wait_for(|p| !*p) => continue,
    }
    let _lifecycle = session.lifecycle().await;
    if session.parkable() { session.park().await; }
}
```

The sleep is the idle period itself, the use AGENTS.md allows; every other
wait is on the watch. Any change that makes the session not parkable restarts
the full delay. Taking the lifecycle lock keeps parking out of a load, startup
or delete in progress; the re-check under it catches a change while waiting.
`park()` is `stop()` recording `Change::Parked` instead of `Exited(None)`:
`stop` takes the change to record.

Known limit: work Claude starts on its own with nothing listed can begin in
the instant between the re-check and the kill. With 007's list, the only such
work is a schedule the bridge could not see (durable jobs from another
session), so this is named, not handled.

### Daemon wiring

`oxd --park-after-ms MS` (default 900 000), parsed beside `--idle-exit-ms`,
stored on `Daemon` and passed to `Session::spawn`/`restore`. After the last
session parks, `Daemon::idle` exits the daemon as it does today. `close_all`
(shutdown) is unchanged.

### Protocol and app

- `EventKind::AgentParked` (`agent_parked`): "The daemon stopped the agent
  because the session was idle, with no background work, and unviewed. The
  session is `exited`; `session.load` resumes it." 010 reuses it when archive
  stops the agent and extends the wording then.
- `crates/ox/src/conversation.rs`: `AgentParked` produces no item.
  `ui/src/protocol.ts` needs no change (it receives items, not events); check
  `Item` handling compiles.
- `SessionState` is unchanged: a parked session is `exited`, and the sidebar
  shows "Exited" as for any exited session.

### Fake agent

`oxd-fake-agent --background` reports `background` on every state update,
sends `idle` with `[]` after answering `session/new`, and accepts two
directives: `background-start <name>` (a tool call `bg-<name>` left
`in_progress` and listed) and `background-end <name>` (its completion, then
the state with the list without it). Without the flag the agent behaves as
now, so existing tests keep their event sequences and never park.

### Docs

`docs/architecture.md` (parking: when, what it keeps, what wakes),
`docs/acp-v2.md` (`oxd` column for `background`: supported, used for parking;
`agent_parked`), `crates/oxd/src/main.rs` usage line, and `README.md` where it
describes session states or `oxd` flags.

## Contract for later units

- `EventKind::AgentParked` exists and `Session::stop` takes the change to
  record; 010 records `Parked` when archive stops an agent.
- A session with a subscriber never parks, so no stream ends at park.
- Parking holds the lifecycle lock, so a delete, load or archive in progress
  never races it.

## Check your assumptions

1. **Confirmed as proposed.** A session parks 15 minutes after it becomes
   parkable with nothing subscribed to it; the delay is `oxd --park-after-ms`,
   with no app setting. Only `session.load` wakes it (the app does so when the
   user clicks it); `oxctl prompt` to a parked session is refused, as for any
   exited session.
2. **Confirmed as proposed.** No stream ends at park; a parked session shows as
   Exited; `agent_parked` is recorded and not displayed.
3. Confirmed in 007 and relied on here: any background work or known scheduled
   prompt keeps a session awake; an agent that does not report `background`
   never parks.

## Implementation sequence

1. `ox-protocol` `EventKind::AgentParked`, and its arm in
   `crates/ox/src/conversation.rs`.
2. `state.rs`: `background` on `Live` and `Starting`, `quiet()`,
   `Change::Parked`; unit tests beside the existing ones.
3. `session.rs`: viewer count and guard, `parkable` watch in `update`,
   `stop(change)`, `park()`, the park task started in `new` and aborted in
   `close`.
4. `conn.rs`: `forward` holds the viewer guard.
5. `main.rs` / `daemon.rs`: `--park-after-ms`, passed to sessions.
6. `oxd-fake-agent`: `--background` and the two directives.
7. Docs.

## Verification

`crates/oxd/tests/daemon.rs`, daemon started with a short `--park-after-ms`
and fake agents with `--background`. Each negative case waits for a control
session in the same daemon to park first, so "did not park" is checked after
parking demonstrably happened:

- an unsubscribed idle session parks: `agent_parked` arrives (seen through a
  later subscription from `after_seq` 0 or the watch listing `exited`), and
  its agent process has exited;
- a subscribed session does not park while a control session parks; after
  unsubscribing it parks;
- a session with `background-start` does not park while the control parks;
  after `background-end` it parks;
- a session whose agent reports no `background` (fake without the flag) does
  not park while the control parks;
- becoming busy restarts the delay: prompt a session shortly before its delay
  ends and check it is still live when a control started at the same time has
  parked;
- a parked session loads, replays nothing new (history kept), and takes a
  prompt;
- `lifetime`-style: after every session parks and the client leaves, the
  daemon exits.

`state.rs` unit tests: `background` from the resume snapshot; `quiet()` for
`None`, `[]` and a non-empty list; `Parked` from `Live` and its event.

`scripts/smoke-acp.py` gains `check_parking`, run with real Claude against
its own `oxd` started with a short `--park-after-ms` (such as 10 s) so the
other checks keep the default: Claude starts a background `sleep` longer than
the delay and ends its turn; the client unsubscribes and follows the session
through the watch listing only, which is not a viewer; the listing shows the
session `running` again for Claude's own turn after the command (so it was not
parked), then `exited`, and a later subscription shows `agent_parked` after
that turn. This is the end-to-end check of
the request's hard requirement.

Undo check (AGENTS.md): making `forward` drop its guard, or `quiet()` ignore
`background`, fails the corresponding test.

Commands: `make check-rust`, `make e2e` (the `crates/ox` change),
`make smoke-claude` (`oxd` and `ox-protocol` changed).

## Handoff

Ready. Work size: state additions, a per-session timer task driven by a watch,
viewer counting, one flag, fake-agent support, daemon tests and one smoke
check; a normal assignment comparable to 005's delete path. Detail: the park
loop is given because its restart and re-check rules are the tricky part.
`plan_metrics.py`: 1530 words, just above the provisional band and below this
repository's median (1669).
