# Flatten handleOutput

## Goal

`handleOutput` in `internal/claude/agent.go` is 106 lines of nested switches
across ten message types. Split it so it reads as the startup check, the
conversation bookkeeping, and a flat dispatch switch whose cases each make one
call. Behavior does not change. Resolves #209.

## Related code

- `internal/claude/agent.go:180-285` — `handleOutput`, the function to split.
- `internal/claude/agent.go:309` — `startupOutput`, which runs first and keeps
  its place.
- `internal/claude/prompt.go` — `commandStarted`, `commandCancelled`,
  `sessionState`, `publish`: the existing per-message handlers the new methods
  sit beside in style.
- `internal/claude/messages.go` — `message` and its accessors (`toolUses`,
  `toolResults`, `failed`, `errorText`, `usageWindows`), unchanged.

## Decisions

- **Shape.** `handleOutput` becomes:
  1. the `startupOutput` early return, unchanged;
  2. `a.noteConversation(m)`, which records `m.SessionID` into
     `conversationID` and, for `stream_event`, `assistant`, `user`, and
     `result`, reports a new resumable token. The rule stays in one place,
     ahead of dispatch, as now;
  3. one `switch m.Type` whose every case is a single call or statement.
- **New methods**, in `agent.go` directly after `handleOutput`, in dispatch
  order, each taking `m message` and carrying its existing comments verbatim:
  - `commandLifecycle` — the `started`/`cancelled` switch.
  - `systemMessage` — `session_state_changed` (running and idle only) and
    `background_tasks_changed`.
  - `assistantMessage` — notes `mayHaveSchedules` for `CronCreate` and
    `ScheduleWakeup`, publishes top-level `tool_use`.
  - `userMessage` — publishes top-level `tool_result`, skipping error results
    while `stops` is nonempty.
  - `streamEvent` — top-level `content_block_delta` text and thinking deltas,
    setting `streamedText`.
  - `turnResult` — the result's text, the `text_delta` fallback when nothing
    streamed, the reset of `streamedText`, the bookkeeping-result filter, and
    clearing `prompting.turn`.
- `control_response`, `control_request`, `control_cancel_request` already
  dispatch to one call. `rate_limit_event` keeps its single `if` inline; a
  method for it would only rename three lines.
- **Construction.** No state, owner, or ordering changes. Every check runs in
  the same order on the same message; the methods only move code. The
  resumable report still precedes any update the same message publishes.

## Check your assumptions

None.

## Test plan

`internal/server` tests run the fake harness and never reach
`handleOutput`. Coverage comes from `internal/claude` tests and `TestLive`.

| Case | Observed by |
| --- | --- |
| startup check precedence | `TestStartupFailureKeepsClaudesReason`, `TestInputWaitsForStartup` (failed startup turn publishes its error) |
| `noteConversation` | `TestLive` "resume after stop" only — **gap** |
| `commandLifecycle` | `TestPromptReleasedAfterClaudeTakesIt`, `TestPromptSentWhileClaudeWorks`, `TestStopDropsQueuedPrompt` |
| `systemMessage` | `TestApprovalRouting` (requires_action not published), `TestParkableFollowsActivity`, `TestBackgroundTasksAfterTurn`, every test using `state` |
| `assistantMessage` | `TestTopLevelToolCallsArePublished`, `TestTurnWithoutStopHookKeepsSchedules`, `TestResumeHoldsSchedulesOnlyAfterCronCreate` |
| `userMessage` | `TestTopLevelToolCallsArePublished` (subagent results hidden, interrupt skip, next turn's errors published) |
| `streamEvent` | `TestThinkingIsPublished`, `TestTurnsClaudeStartsArePublished`, `TestTurnAfterPromptResultIsClaudes` |
| `rate_limit_event` | `TestUsageIsPublished` |
| `turnResult` | `TestTurnsClaudeStartsArePublished` (streamed text not repeated, empty result dropped), `TestTopLevelToolCallsArePublished` (Interrupted), `TestInputWaitsForStartup` (error text), `TestTurnAfterPromptResultIsClaudes` (turn cleared); `text_delta` fallback without streaming — **gap** |

Two gaps sit on exactly the code this change moves, and each is cheap to
close in an existing test's setup. Both tests pass before and after the
change; they guard the move.

- **Result text without streaming, Claude harness.** Extend
  `TestTurnsClaudeStartsArePublished` (`internal/claude/prompt_test.go`) with a
  second turn after the first idle: `state running`, a result
  `"tock"` with no stream, `state idle`. Expected types become
  `state,text_delta,result,state,state,text_delta,result,state,exited`, and the
  second `text_delta` carries `"tock"`. This also shows `streamedText` resets
  after a result.
- **Resumable token from model output, Claude harness.** Add
  `TestModelOutputMakesConversationResumable` to
  `internal/claude/claude_test.go`, beside `startLog`, which already records
  `OnResumable` tokens. Build the agent with `newAgent(newWriter(io.Discard),
  make(chan agent.Event), make(chan struct{}), nil, log.options())` and
  `Handle(initialized)`. A `system`/`init` line with `session_id` `"c1"`
  reports no token; an `assistant` line with `"c1"` reports `["c1"]`; a second
  `assistant` line with `"c1"` reports nothing more. No existing unit test
  configures `OnResumable` on a running agent, so this one is new.

## Implementation plan

1. `internal/claude/prompt_test.go`, `internal/claude/claude_test.go`: add the
   two test cases above and run `go test ./internal/claude` on the unchanged
   code; both pass.
2. `internal/claude/agent.go`: extract `noteConversation`, `commandLifecycle`,
   `systemMessage`, `assistantMessage`, `userMessage`, `streamEvent`, and
   `turnResult` as described, moving code and comments without edits beyond
   `m.` receivers and early `return`s that replace `break`.
3. Run `make test`. `make live` cannot observe a pure move beyond what the unit
   tests already do and is skipped.

## Requirement changes

None.
