# One tracker for every harness's untaken inputs and execution state

## Goal

Each harness keeps its own queue of untaken inputs, publishes its own input lifecycle updates, and sets the execution state clients see by individual transitions. Two of those transitions write states the rules forbid: the fake reports `idle` with an input queued during a configuration, so archive stops the session and cancels the prompt; and a Claude configuration acknowledgement resets a reported `running` to `idle`, so the agent counts as parkable and `Interrupt` writes nothing. See [audit 2026-11-20-003, F1](../audits/2026-11-20-003-since-first-audit.md#f1-each-harness-keeps-its-own-input-queue-and-sets-its-published-state).

When done, one type in `internal/agent`, `agent.Tracker`, owns every harness's untaken inputs, the state the harness last reported, and its background task count. It publishes `prompt_queued`, `prompt`, and `prompt_cancelled`, and derives the published state: `running` while any input is untaken, `background` while the reported state is `idle` with tasks running, and otherwise the reported state. No harness can set the published state directly, so neither wrong state can be built.

## Related code

- `internal/agent/agent.go:23-33` — the `Send` contract every harness implements by hand.
- `internal/claude/prompt.go` — Claude's `Send`, `write`, `writeNext`, `ready` (sets `idle`), `dropUntaken`, `Interrupt`, `waiting`, `commandStarted`, `commandCancelled`, `sessionState` (ignores `idle` while inputs are untaken), `setState`, `published`, `publishState`, `endPrompt`, `publish`.
- `internal/claude/agent.go` — `Agent.untaken`, `Agent.state`, `Agent.background`; `background_tasks_changed` handling; `idle`, `Snapshot`, `end`.
- `internal/claude/configure.go` — `Configure` requires `idle()`; `endConfiguration` calls `ready`.
- `internal/claude/park.go` — `Parkable` reads `background`.
- `internal/fake/fake.go` — `state`, `queue`, `tasks`, `published`, `publishState`, `Send`, `begin` (sets `running` only when a turn starts), `take`, `drop`, `finish`, `ask`, `Approve`, `Interrupt`, `Configure`, `Parkable`, `Stop`, `Snapshot`.
- `internal/ur/ur.go` — `queue`, `Snapshot` (derives `running` from the turn), `Send`, `begin`, `finish`, `drop`, `Interrupt`, `Configure`, `Stop`.
- `internal/server/session.go:116-125`, `:197-210` — `sendPrompt` reads `Snapshot().Queued`; `hold` reads `Snapshot().State` to decide whether archive may stop the session. Unchanged; they now read one derivation.

## Decisions

- **The type.** `agent.Tracker` in a new `internal/agent/tracker.go`:

  ```go
  // Tracker is a harness's untaken inputs and the execution state it
  // publishes. The harness decides when an input is written, taken, or
  // dropped; Tracker publishes the input's lifecycle update and any change to
  // the published state.
  type Tracker struct {
      publish  func(Update)
      queued   []Input // untaken, oldest first
      reported string  // the harness's last reported state; idle at start
      tasks    int     // background tasks running
  }

  func NewTracker(publish func(Update)) *Tracker
  func (t *Tracker) State() string        // running while queued; background while reported idle with tasks; else reported
  func (t *Tracker) Idle() bool           // State() is idle or background
  func (t *Tracker) Queued() []Input      // a copy, never nil
  func (t *Tracker) First() (Input, bool) // the oldest untaken input
  func (t *Tracker) Len() int
  func (t *Tracker) Tasks() int
  func (t *Tracker) Accept(in Input)      // queue, publish prompt_queued
  func (t *Tracker) Take() Input          // pop the first, publish prompt; the reported state becomes running
  func (t *Tracker) DropFirst()           // pop the first, publish prompt_cancelled
  func (t *Tracker) DropFrom(i int)       // drop queued[i:], oldest first, each with prompt_cancelled
  func (t *Tracker) Report(state, messageID string)
  func (t *Tracker) SetTasks(n int)
  ```

  Every mutator reads `State()` before and after, and publishes a `state` update when it changed. The update names `messageID` when given (`Accept` passes the input's ID), and is otherwise left for the harness's `publish` to tag. `publish` is the harness's own method, so `Time` and turn tagging stay with the harness.

- **Taking an input means running.** `Take` sets the reported state to `running`: a harness takes an input only to start or join a turn. Claude's next `session_state_changed` clears it. This removes the fake's and Ur's explicit `running` sets in `begin`, and covers Claude reporting `idle` before it takes a written input, which `sessionState` handled with its own case.

- **What each harness keeps.** The decisions `e347e89` gave the harness stay there: Claude writes only the first input, keeps the written one on interrupt, and writes the next on `initialize`, a configuration's end, or a take or drop; the fake takes a queued input on its next tick; Ur begins the next input when a turn ends. Claude's `prompting.input` (the latest input written, which the releasing update names) and `prompting.turn` stay in the phase.

- **Claude's release.** `sessionState`:

  ```go
  if state == agent.StateIdle {
      a.withdrawApprovals()
      if p, ok := a.phase.(*prompting); ok && a.tracker.Len() == 0 {
          a.tracker.Report(state, p.input.MessageID)
          a.ready()
          return
      }
  }
  a.tracker.Report(state, "")
  ```

  The starting-phase early return and the untaken-input case go: a report during a start, or while inputs are untaken, is stored and the derivation keeps `running`. `ready` no longer sets any state; after a configuration the state is Claude's last report.

- **Requirements.**
  - **Session status:** the published state has one derivation, so every page, `session/get`, `list_sessions`, and `read_session` show the same value from acceptance on. A reconnect or second page reads it from the snapshot, which reads the same `Tracker`.
  - **One prompt at a time** and **Message delivery:** `prompt_queued`, `prompt`, and `prompt_cancelled` are published once per input by `Tracker`; `sendPrompt` still refuses while `Queued` is nonempty. An Ur session with a queued input stays `running` between the two turns instead of publishing `idle` and `running` in between.
  - **Archiving a session:** `hold` sees `running` for any session with an untaken input, including a fake or Claude session that accepted one during a configuration, and refuses with `-32002`.
  - **Parking**, **When settings can change**, **Pending setting changes:** Claude's `idle()` is `available && tracker.Idle()`. A turn Claude starts during a configuration leaves the agent `running` after the acknowledgement: not parkable, `Configure` refused with `ErrBusy`, and the page shows `running` until Claude reports `idle`.
  - **Stop:** Claude's `Interrupt` checks `tracker.Idle()`, so a session showing `running` after a configuration gets an interrupt written. Dropping inputs on interrupt or stop publishes a `state` update when nothing untaken is left and the reported state differs; otherwise the order of updates is unchanged.
  - **Ur sessions:** Ur's `Configure` refuses unless `tracker.Idle()`; its behavior is otherwise unchanged.

- **Claude Code.** No new assumption. The state after a configuration follows whatever Claude last reported ([§3 `system`](../../docs/claude-code.md#system), [§4 When a prompt is taken](../../docs/claude-code.md#when-a-prompt-is-taken)), so the plan holds whether or not Claude reports `running` before acknowledging a control request. `Take` setting `running` relies on Claude taking an input only into a running turn or as a turn of its own ([§2 User messages](../../docs/claude-code.md#user-messages)).

- **Construction.** The published state cannot be `idle` or `background` while an input is untaken, because `State()` computes it from the queue and no harness can write it. The queue and the input lifecycle updates cannot disagree, because only `Tracker` changes the queue and it publishes in the same call. The background count lives only in `Tracker`; Claude's and the fake's `Parkable` read `Tracker.Tasks()`.

- **Ownership.** `Tracker` adds no state: it takes over `untaken`/`queue`, `state`, and `background`/`tasks` from the three harnesses. Each agent owns its `Tracker`, and only the session goroutine calls it, through the agent's methods and `Handle`. No timers, locks, or races are added. It ends with the agent; `Stop` and `end` drop its inputs through `DropFrom(0)`. The commit body states this.

## Check your assumptions

Settled without the user, as instructed by the requesting session:

1. **An Ur session with a queued prompt shows running between turns.**
   - **Plan:** When a prompt or message waits behind a running Ur reply, the session's status stays running until the waiting one is answered, as Claude sessions already do.
   - **Alternative:** Keep the brief idle between the two replies; it would let the status disagree with the pending prompt it shows.
2. **A Claude session that starts its own turn during a settings change shows running afterwards.**
   - **Plan:** If Claude starts a turn of its own (a schedule or a finished background task) while a setting change is applied, the session shows running once the change lands, keeps Stop available, and does not park until Claude finishes.
   - **Alternative:** Show idle after every settings change, as now; Stop would do nothing and the session could park mid-turn.

## Test plan

- `Tracker`'s rules, unit tests in a new `internal/agent/tracker_test.go` (no test exists in `internal/agent`): `Accept` on idle publishes `prompt_queued` then `state running` naming the input; `Report(idle)` while an input is queued publishes nothing and `State()` stays `running`; `Take` publishes `prompt` and no state, leaving `running`; `DropFrom` publishes `prompt_cancelled` oldest first, then the reported state; `SetTasks` with the reported state `idle` publishes `background`, and `idle` when the count returns to 0; `SetTasks` while `running` publishes nothing; `Report` with a message ID tags the state update; `Idle()` is true for `idle` and `background` only. Each condition in `State()` has a case that fails without it.
- Claude configuration acknowledgement keeps a reported `running` (audit defect), Claude harness: new `TestConfigurationKeepsReportedState` in `internal/claude/configure_test.go`, since no existing test delivers a state report during a configuration. Initialize, `Configure`, deliver `session_state_changed running`, acknowledge; assert `Snapshot().State` is `running`, `Parkable()` is false, `Configure` returns `ErrBusy`, and `Interrupt` writes an `interrupt` control request. Then deliver `idle`; assert `idle` is published and the agent is parkable.
- Fake reports `idle` with an input queued during a configuration (audit defect), server: new step in `TestArchive` (`internal/server/mcp_test.go`) on the idle bystander session `b`: `c.send` a `session/configure`, `c.call` `session/send`, then `session/archive` is refused with `sessionBusy`; the configure response succeeds, `b` publishes `prompt`, then `idle`.
- `TestInterruptDuringConfiguration` (`internal/claude/configure_test.go`): the interrupt now publishes the held inputs' `prompt_cancelled` followed by `state idle`, and the acknowledgement publishes nothing.
- `TestInputDuringTurnRunsNext` (`internal/ur/ur_test.go`): the expected sequence becomes `prompt_queued p1`, `state p1 running`, `prompt p1`, `prompt_queued p2`, `result p1`, `prompt p2`, `result p2`, `state p2 idle`.
- Any other harness or server test asserting `prompt` before `state running` for an idle fake or Ur session gets the new order: `state running` now follows `prompt_queued`.

## Implementation plan

1. Add `internal/agent/tracker.go` with `Tracker` as above, and `internal/agent/tracker_test.go`. Shorten the `Send` contract comment in `agent.go` to the behavior, naming `Tracker` as the type that keeps the queue, the lifecycle updates, and the published state.
2. Claude (`internal/claude/agent.go`, `prompt.go`, `configure.go`, `park.go`): replace `untaken`, `state`, and `background` with `tracker *agent.Tracker`, created in `newAgent` with `a.publish`. `Send` calls `tracker.Accept` after the write decision and drops its `running` set. `writeNext` uses `First` and `DropFrom(0)`; `commandStarted` uses `Take`; `commandCancelled` uses `DropFirst`; `waiting` uses `First`. `Interrupt` uses `DropFrom(written)` and `tracker.Idle()`. Rewrite `sessionState` as above, fold `endPrompt` into it, and remove the state set from `ready`. `background_tasks_changed` calls `SetTasks`. `idle()` is `available && tracker.Idle()`; `Parkable` reads `Tasks()`; `Snapshot` reads `State()` and `Queued()`; `end` names the last of `Queued()` and calls `DropFrom(0)`. Delete `dropUntaken`, `setState`, `published`, `publishState`, `endPrompt`, and the `state` field's comment.
3. Fake (`internal/fake/fake.go`): replace `state`, `queue`, and `tasks` with a `Tracker`. `Send` calls `Accept`; `begin` and `take` call `Take`; `ask` reports `requires_action`; `Approve` reports `running`; `finish` reports `idle` before clearing `prompt`; background tasks use `SetTasks(Tasks()±1)`; `drop` becomes `DropFrom(0)`; `idle()` is `tracker.Idle() && config == nil`; `Interrupt` finishes when `!tracker.Idle()` after dropping; `Parkable` reads `Tasks()`. Delete `published` and `publishState`.
4. Ur (`internal/ur/ur.go`): replace `queue` with a `Tracker`. `Send` calls `Accept`; `begin` calls `Take` and drops its `running` publish; `finish` reports `idle` before clearing `turn`; `drop` becomes `DropFrom(0)`; `Configure` refuses unless `tracker.Idle()`; `Snapshot` reads `State()` and `Queued()`.
5. Add the Claude and server tests, and update the existing tests listed above.
6. Update the documents below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, Agents: `agent.Tracker` keeps each harness's untaken inputs, publishes their lifecycle updates, and derives the published state; the harness decides when inputs are written, taken, or dropped. The Claude agent section's paragraph on Claude's execution state and the untaken-input queue describes the state as Claude's last report, kept in the tracker, and drops "except that a prompt is `running` from the moment it is accepted".
- `AGENTS.md`, Source Map: add `internal/agent/tracker.go` (untaken inputs and published state) and `tracker_test.go`.
