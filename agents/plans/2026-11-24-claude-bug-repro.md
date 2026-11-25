# Park resumed sessions without schedules, and stop startup turns

## Goal

Two defects in how Ox drives a resumed Claude process:

- [#5](https://github.com/kkestell/ox/issues/5): every process started with
  `--resume` counts as possibly holding restored schedules until a Stop hook
  reports its schedule list. A first turn that ends by interrupt or error sends
  no Stop callback, so the idle session never parks until a later turn
  completes. After the change, a resumed process counts as possibly holding
  schedules only when the session's history has a `CronCreate` tool call. Every
  other resumed process parks like a new one.
- [#34](https://github.com/kkestell/ox/issues/34): while the agent is
  `starting`, `Interrupt` writes nothing, yet the page offers Stop once a
  startup turn reports `running` or `requires_action`. After the change,
  `Interrupt` writes Claude's interrupt in `starting` whenever the published
  state is neither `idle` nor `background`, as it does in `available`.

Both rest on the live reproductions in
`agents/scratch/2026-11-24-claude-bug-repros.md` and the matching
`docs/harnesses/claude-code.md` edits already in this worktree; both files
land with the change. The same notes show
[#6](https://github.com/kkestell/ox/issues/6) no longer happens on 2.1.331;
the pull request closes it with the harness doc update and needs no code for
it.

## Related code

- `internal/claude/agent.go` — `newAgent` sets `mayHaveSchedules` to
  `options.Resume != ""`; `handleOutput` sets it on a `CronCreate` or
  `ScheduleWakeup` tool call.
- `internal/claude/park.go` — `Parkable` and `stopHook`, the only place that
  clears `mayHaveSchedules`.
- `internal/claude/prompt.go` — `Interrupt` returns without writing in
  `*starting`, `*configuring`, `timedOut`, and `exited`.
- `internal/agent/agent.go` — `Options`, what the session passes a harness at
  start.
- `internal/server/session.go` — `start` builds `agent.Options`;
  `history.record` stores every `tool_use` update with its `tool_name`.
- `internal/store/store.go` — `Events`, `UserPrompts`: queries over a session's
  recorded updates with `json_extract`.
- `internal/claude/park_test.go`, `internal/claude/prompt_test.go`
  (`TestInputWaitsForStartup`), `internal/server/live_test.go` ("restored
  schedules") — the tests that get the new cases.

## Decisions

- **Source of "the history has a `CronCreate` call":** Ox's own session
  history. The session goroutine owns it, and the Claude agent records each
  top-level tool call into it as a `tool_use` update, which matches Claude's
  restore rule: only main-thread `CronCreate` calls come back. The server stays
  harness-neutral: it passes the names of every recorded tool call, and the
  Claude agent alone knows that `CronCreate` is the one that matters. The
  history can hold more than the conversation being resumed (a `/clear`
  starts a new conversation in the same history), which only keeps a session
  waiting for a Stop report it could have skipped. No `CronDelete`, expiry,
  or one-shot subtraction: that would copy Claude's restore rule into Ox.
- **`agent.Options.ToolNames []string`:** the distinct `tool_name`s of the
  session's recorded `tool_use` updates. The session reads it from the store at
  every start. A failed read goes to `oxd`'s log with the session's ID, and the
  session starts with no tool names, as "Database failures" requires: a session
  keeps working when its database fails. Such a session parks like a new one,
  so a restored job can wait for the next send. Ur and the fake harness ignore
  it.
- **Interrupt in `starting`:** `starting` joins `available` and `prompting` in
  the branch that writes the interrupt when the tracker is not idle. In
  `starting` no input has been written, so `written` is 0 and the request has
  no `cancel_queued`. Held inputs are dropped first, as today, so an interrupt
  with only held inputs and no startup turn still writes nothing. `configuring`
  and `timedOut` are unchanged.
- **Requirements:**
  - *Parking:* a resumed session whose history has no scheduling call parks
    after its period whatever its first turn's outcome. One whose history has a
    `CronCreate` call keeps today's behavior: it parks only after a completed
    turn reports no schedules. A resume after `oxd` restart, after a park, or
    after the process exited all take the same path, since each is a start with
    a resume token.
  - *Stop:* Stop during a startup turn of a resumed session ends that turn.
    Claude answers with the cancellations, an `aborted` result, and `idle`. A
    Stop sent with no startup turn running drops held prompts and writes
    nothing, as today. Page behavior is unchanged: it already offers Stop on
    `running` and `requires_action`.
  - *Startup turn results:* a startup turn's result is published like any
    turn's, so a stopped startup turn shows `Interrupted` and a failed one
    shows its error. Today `startupOutput` keeps every failed `result` in
    `starting` as the startup failure reason and publishes nothing. A failed
    `result` is a startup failure only while Claude's last reported state is
    `idle`: a failed resume prints its `result` with no `session_state_changed`
    before it (probed live on 2.1.331), and a startup turn's `result` follows
    its `running` report. The agent reads Claude's last reported state from the
    tracker through a new `Tracker.Reported`, the one place it is kept.
  - *Claude's own turns:* a startup turn's updates are unchanged.
- **Settling:** no new wait. Claude answers the interrupt control request, and
  Ox ignores its response as it does after `initialize`.
- **Construction:** no new state. `mayHaveSchedules` keeps its one setter per
  event: start (resume with a `CronCreate` in history), a scheduling tool call,
  and a Stop report. The fact is derived from the history at each start, not
  stored.
- **Claude Code:**
  - Restore rule: section 8, "Background work and schedules" (`claude
    --resume` restores unexpired main-thread recurring jobs and future one-shot
    jobs; the transcript's only record of a job is its `CronCreate` call; a
    `/loop` with an interval is a main-thread `CronCreate`; restored jobs are
    reported only at a Stop). Wakeups and subagent jobs are not restored, so
    `ScheduleWakeup` in the history does not count. The fact clears on the next
    Stop report, as today.
  - Startup turns: section 4, "Turns", "When a turn ends" ("A resumed process starts its
    restored schedules once its SessionStart hooks finish ... `interrupt` before
    `initialize` stops such a turn exactly as after it") and the `initialize`
    notes (a response during a turn carries `session_state: running`). Through
    Ox, `initialize` is on stdin at spawn and was answered before any scheduled
    turn in every run; the change does not depend on that ordering.

## Check your assumptions

None.

## Test plan

- Claude harness, `internal/claude/park_test.go`, new
  `TestResumeHoldsSchedulesOnlyAfterCronCreate`: an agent built with
  `Resume` set and `ToolNames` without `CronCreate` (for example `Bash`,
  `ScheduleWakeup`) is parkable once it has answered `initialize`, and stays
  parkable after a failed turn with no Stop hook. One built with `CronCreate`
  in `ToolNames` is not parkable after `initialize` or after a failed turn, and
  becomes parkable after a Stop hook with `session_crons: []`. No existing test
  builds a resumed agent, so none carries this. Fails before the change, where
  every resumed agent waits for a Stop report.
- Claude harness, `internal/claude/prompt_test.go`, `TestInputWaitsForStartup`,
  in its interrupt part: a starting agent that sees
  `session_state_changed` `running` (a startup turn) writes
  `{"subtype":"interrupt"}` without `cancel_queued` on `Interrupt`, also with a
  held input, which is dropped. Then Claude's `aborted_tools` result and `idle`
  publish the `Interrupted` result and `idle`. A starting agent that sees
  `running`, then a failed `model_error` result and `idle`, publishes the
  result with its error text and becomes available once Claude answers
  `initialize`. The existing assertion that an interrupt with only held inputs
  writes nothing stays, and `TestStartupFailureKeepsClaudesReason` keeps
  covering failed results outside a turn. Fails before the change.
- Server, `internal/server/methods_test.go`, new
  `TestStartPassesRecordedToolNames`: a stored session with recorded `tool_use`
  updates for `CronCreate` twice and `Bash`, served by a harness that wraps
  `fake.Start` and keeps its `Options`; `session/send` starts it with
  `ToolNames` holding `CronCreate` and `Bash` once each. No existing test
  inspects the options a start receives.
- Live, `internal/server/live_test.go`, "restored schedules": add a session with
  no scheduling call that completes one turn before the restart; after the
  restart its first turn is denied the same way as the scheduled session's
  (`denyBash`), before the control's turn. Once the control parks, assert this
  session has parked too, while the scheduled one still has not. This is #5's
  live reproduction; it fails before the change.
- The #34 startup turn is not reproducible through Ox's own ordering
  (`initialize` at spawn is answered before any scheduled turn), so it has no
  live case; the unit test drives the agent with Claude's recorded lines.

## Implementation plan

1. `internal/agent/agent.go`: add `ToolNames []string` to `Options`, documented
   as the names of the tool calls the session's history records, each once.
2. `internal/store/store.go`: add `ToolNames(sessionID string) ([]string,
   error)`, `SELECT DISTINCT json_extract(data, '$.tool_name')` over the
   session's events whose `$.type` is `tool_use`.
3. `internal/server/session.go`: in `start`, read `ToolNames` from the store and
   pass it in `agent.Options`; log a failed read with the session's ID and
   start with no tool names.
4. `internal/claude/agent.go`: in `newAgent`, set `mayHaveSchedules` to
   `options.Resume != "" && slices.Contains(options.ToolNames, "CronCreate")`,
   with a comment that `--resume` restores only main-thread `CronCreate` jobs.
   Update the field's comment.
5. `internal/claude/prompt.go`: move `*starting` into the `available,
   *prompting` case of `Interrupt`'s second switch; update the doc comment to
   say Interrupt writes nothing only while a configuration runs.
   `internal/agent/tracker.go`: add `Reported() string`, Claude's last reported
   state. `internal/claude/agent.go`: `startupOutput` keeps a failed `result`
   as the startup failure reason only while `a.tracker.Reported()` is `idle`,
   and otherwise lets `handleOutput` publish it.
6. Tests as in the test plan.
7. Docs as below; run `make docs`.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md`, "Workspaces and sessions" parking paragraph: "A
  scheduling tool call or a resumed conversation" becomes "A scheduling tool
  call, or a resumed conversation whose history has a `CronCreate` call,".
- `docs/architecture.md`, Claude agent: the **Interrupts** bullet names
  `starting` with `available` and `prompting`, and only `configuring` and
  `timedOut` send nothing; the **Parking** bullet's "On resume" becomes "On a
  resume whose history has a `CronCreate` call".
- `docs/harnesses/claude-code.md`: the research session's edits, and, under
  "Startup failures", that no `session_state_changed` precedes a bad
  `--resume`'s `result` (2.1.331).
