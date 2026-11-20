# Each Claude agent phase carries its own fields

## Goal

`claude.Agent` keeps a `phase` enum beside fields that are valid only in some
phases: `prompt *submission` and `turn` (prompting), `config *configuration`
(configuring). Readers check the enum before trusting a field, `queued()`
being the clearest case, and the zero values (`prompt == nil`, `turn == ""`,
`config.requestID == ""`) double as state markers. The types allow states the
design forbids: a `turn` naming a client prompt outside `prompting`, a
`submission` with neither an untaken input nor a `last`, both `prompt` and
`config` set, and a timed-out configuration whose empty `requestID` matches a
`control_response` with an empty `request_id`, which would apply the setting
and release the agent while its process is being stopped.

After the change the phase is one value that holds the fields valid in it, so
those states cannot be built, and the guards and clears that maintained them
are gone. Behavior visible through `internal/agent` and the protocol does not
change.

## Related code

- `internal/claude/agent.go:10-45` — the `phase` enum and the `Agent` fields.
  `:185` clears `turn` on a `result`; `:210-212` `idle()`; `:214-216`
  `Snapshot`; `:218-244` `exit`, which switches on the phase and reads
  `stopping`; `:246-265` `Stop`, `terminate`, `Exited`.
- `internal/claude/prompt.go:24-29` — `submission{queued *agent.Input; last
  string}`. `Send` (`:33-52`) checks the phase and enters `prompting`;
  `Interrupt` (`:58-70`) and `Snapshot` read `queued()` (`:73-78`), which
  checks the phase; `commandStarted` (`:80-89`) sets `queued`, `last`, and
  `turn`; `dropQueued` (`:100-105`) sets `last`; `sessionState` (`:107-122`)
  ends prompting on idle; `endPrompt` (`:134-141`) names `last` and clears
  `prompt`, `turn`, and `phase`; `publish` (`:145-151`) tags updates with
  `turn`.
- `internal/claude/configure.go:10-17` — `configuration{requests, requestID,
  done}`, whose `requestID` is emptied on timeout. `Configure` (`:28-60`)
  enters `configuring`; `controlResponse` (`:72-90`) and `controlTimedOut`
  (`:96-104`) check the phase and the request ID; `endConfiguration`
  (`:106-111`) clears `config` and `phase`.
- `internal/claude/park.go:8-10` — `Parkable` reads `idle()`.
- `internal/claude/prompt_test.go`, `configure_test.go` — the tests that build
  each phase through `Send`, `Configure`, and `Handle`.

## Decisions

- **Construction.** Field by phase, from the code:

  | Field | Valid in | Why |
  | --- | --- | --- |
  | `prompt.queued` | `prompting` | Set only by `Send`, which enters `prompting`; cleared on take, drop, or exit before `prompting` ends. |
  | `prompt.last` | `prompting` | Read only by `endPrompt`. |
  | `turn` | `prompting` | Set only by `commandStarted` on the queued input, so only in `prompting`; cleared by a `result` and by `endPrompt`. A turn Claude starts in another phase has no client prompt. |
  | `config` | `configuring` | Created by `Configure`, ended by `endConfiguration`. |
  | `config.requestID` | `configuring`, before the timeout | Emptied on timeout to mark "stopping, waiting for the exit". |
  | `streamedText` | every phase | Tracks Claude's model turn, set by a `text_delta` and cleared by its `result`. Claude starts its own turns in `available` and, from a schedule, while a configuration is in flight. |
  | `stopping` | every phase | Set by `terminate` from `Stop` in any phase and from a configuration's timeout; read only by `exit`, where it suppresses the `exited` update outside `prompting`. |
  | `state`, `approvals`, `settings`, `conversationID`, `resumable`, `background`, `mayHaveSchedules`, `done` | every phase | Claude's reports and process facts, independent of what Ox is doing. |

  `prompt.last` is always the latest input written in the current
  `prompting` phase whenever it is read: `Send` refuses while an input is
  untaken, every written input is taken or dropped before release, each take
  or drop sets `last` to it, and `exit` drops an untaken input before ending
  the prompt. So the prompting value stores the latest written input, which is
  always valid, and whether Claude has yet to take or drop it, instead of a
  nullable `queued` beside a `last` that is empty until the first one
  settles.

  The new representation, in `agent.go`:

  ```go
  // phase is what Ox is doing with the process. Each phase's fields exist
  // only while the agent is in it.
  type phase interface{ isPhase() }

  type available struct{}
  type exited struct{}

  // prompting holds the agent from the first client input written until
  // Claude reports idle with none queued.
  type prompting struct {
      input  agent.Input // the latest input written, which the releasing update names
      queued bool        // Claude has not taken or dropped input yet
      turn   string      // the client input whose turn is running, until its result
  }

  // configuring holds the agent while control requests are in flight, one at
  // a time: the first of requests, under requestID.
  type configuring struct {
      requests  []control
      requestID string
      done      func(error)
  }

  // timedOut holds the agent after a control request went unanswered and Ox
  // asked the process to exit. The configuration fails with err once it has.
  type timedOut struct {
      done func(error)
      err  error
  }
  ```

  `prompting` and `configuring` are held by pointer; `available`, `exited`,
  and `timedOut` by value. `Agent.phase` is the only phase field; `prompt`,
  `turn`, `config`, `submission`, `configuration`, and the enum constants are
  deleted. `newAgent` sets `phase: available{}`.

  Readers take a phase's fields from the phase itself:
  - `queued()` returns a pointer to a copy of `p.input` when the phase is `*prompting` with
    `queued` set, else nil. It returns a copy so a `Snapshot` a caller keeps
    does not change with the next `Send`.
  - `Send` switches on the phase: `available` enters a new `*prompting`;
    `*prompting` with `queued` returns `ErrBusy`; any other phase returns
    `ErrBusy`. After the write it sets `input` and `queued`.
  - `commandStarted` and `commandCancelled` act only on a `*prompting` whose
    `queued` input has the reported ID. `commandStarted` clears `queued`, sets
    `turn`, and publishes `prompt`. `dropQueued(p)` clears `queued` and
    publishes `prompt_cancelled`; neither touches the released ID.
  - `sessionState` on idle: a `*prompting` with `queued` keeps waiting; a
    `*prompting` without it ends through `endPrompt(p, update)`; any other
    phase sets the state.
  - `endPrompt(p, final)` names `p.input.MessageID` and sets `available{}`.
  - The `result` case clears `turn` on a `*prompting` phase. `publish` tags an
    update without a `MessageID` with `turn` of a `*prompting` phase.
  - `controlResponse` and `controlTimedOut` act only on a `*configuring` whose
    `requestID` matches. `controlTimedOut` calls `terminate`; on failure it
    ends the configuration with that error, as now; on success it sets the
    phase to `timedOut{done, c.fail(agent.ErrExited)}`. A late response, a
    response with an empty `request_id`, or a stale timer finds no
    `*configuring` and does nothing.
  - `endConfiguration(done, err)` sets `available{}` and calls `done`.
  - `exit` switches on the phase: `*prompting` drops an untaken input and ends
    the prompt with the `exited` update; `*configuring` ends with
    `fail(agent.ErrExited)`; `timedOut` ends with its `err`. It publishes the
    unprompted `exited` update unless the phase was `*prompting` or
    `stopping` is set, then sets `exited{}` and closes `done`.
  - `idle()` is `available` and state `idle`. `Exited()` and `Stop`'s loop
    test for `exited{}`.

  Guards and conventions this removes: `queued()`'s phase check (the fields
  are reachable only through the `*prompting` value); `endPrompt` clearing
  `prompt`, `turn`, and `phase` separately; `endConfiguration` clearing
  `config` and `phase` separately; `commandStarted` and `dropQueued` writing
  `last`; `exit`'s `reported := a.phase == prompting` comparison and its
  `a.config` read; `Send`'s three-way phase test; `controlTimedOut` emptying
  `requestID` and the reliance on no response carrying an empty ID; and the
  representable states listed under Goal.

  No other place in `internal/claude` keeps phase-scoped fields beside the
  phase: `streamedText` and `stopping` span phases, as the table shows, and
  stay on `Agent`.
- **Ownership.** The `timedOut` phase replaces the empty-`requestID` marker;
  it adds no state the agent did not already hold. The user-visible behavior
  that needs it: a setting change whose control request Claude never answers
  stays pending, with prompts and other setting changes refused, until the
  process exits, and then succeeds with the settings applied at the next start
  (When settings can change). It brings no new race: its only exit is the
  process's exit, and timers and responses for the configuration it replaced
  match nothing. The commit body repeats this.
- **Requirements.** One prompt at a time, When settings can change, Parking,
  Approvals wait, Message delivery, and Unexpected exits hold unchanged: the
  same updates are published in the same order with the same `message_id`s,
  `Send` and `Configure` refuse in the same phases, and `Parkable` reads the
  same facts. The releasing update names the same input, since the latest
  written input is the latest taken or dropped one whenever the agent
  releases.
- **Settling.** No new waits. The `timedOut` phase ends on the process's exit,
  which `terminate`'s kill timer bounds.
- **Claude Code.** No new assumption. Uptake, drop, and release follow
  [`command_lifecycle`](../../docs/claude-code.md#command_lifecycle),
  [When a prompt is taken](../../docs/claude-code.md#when-a-prompt-is-taken),
  [When a turn ends](../../docs/claude-code.md#when-a-turn-ends), and
  [Interrupt](../../docs/claude-code.md#interrupt) as now. Bytes written to
  Claude are unchanged.

## Check your assumptions

None.

## Test plan

- Claude harness, `TestConfigureTimeoutStopsProcess`
  (`internal/claude/configure_test.go`): after the timeout, also deliver a
  successful `control_response` with an empty `request_id`, and assert the
  configuration has not ended and no settings were reported. This fails on the
  current code, where the timed-out configuration's empty `requestID` matches
  it. Keep the existing assertions that the late acknowledgement does nothing,
  `Send` returns `ErrBusy`, and the exit ends the configuration with
  `ErrExited`.
- Claude harness, `TestStopDropsQueuedPrompt` (`internal/claude/prompt_test.go`):
  assert that the idle release after the interrupt names the dropped input
  `id`, not the taken `first`, which guards the release naming the latest
  written input when a turn's prompt and the dropped one differ.
- The rest of the behavior is already covered and keeps its assertions:
  release naming and steering in `TestPromptSentWhileClaudeWorks`, a queued
  prompt across Claude's own turn in `TestPromptReleasedAfterClaudeTakesIt`,
  `turn` cleared by a `result` in `TestTurnAfterPromptResultIsClaudes`, exit in
  each phase in `TestStopDropsQueuedPrompt`, `TestConfigureFailure`, and
  `TestUnexpectedExitIsReported`, and the busy rules in
  `TestConfigureRequiresIdle` and `TestConfigureWaitsForAcknowledgements`.
- Live: run `make live`, since the harness's prompt and configuration
  handling is rewritten.

## Implementation plan

1. `internal/claude/agent.go`: replace the enum with the `phase` interface and
   its five types, delete `prompt`, `turn`, and `config` from `Agent`, set
   `available{}` in `newAgent`, and rewrite the `result` case, `idle`, `exit`,
   `Stop`, and `Exited` as the Construction decision describes.
2. `internal/claude/prompt.go`: delete `submission`; rewrite `Send`,
   `queued`, `commandStarted`, `commandCancelled`, `dropQueued`,
   `sessionState`, `endPrompt`, and `publish` over `*prompting`.
3. `internal/claude/configure.go`: rename `configuration` to `configuring`;
   rewrite `Configure`, `sendControl`, `controlResponse`, `controlTimedOut`,
   and `endConfiguration` over `*configuring` and `timedOut`.
4. Tests as the test plan describes, in `internal/claude/configure_test.go`
   and `internal/claude/prompt_test.go`.
5. `docs/architecture.md`, as below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, The Claude agent: the phase table gains a
  `timedOut` row ("A control request went unanswered and Ox asked the process
  to exit." / "The process exits.") and the `configuring` row's "Leaves on"
  adds "or a timeout"; replace "The phase identifies the current operation:"
  with "The phase identifies the current operation and holds the fields valid
  only during it:". "A prompt is written in any phase but `configuring` and
  `exited`" becomes "A prompt is written in `available`, or in `prompting`".

