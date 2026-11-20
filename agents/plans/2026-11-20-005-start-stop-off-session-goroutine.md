# Starting and stopping Claude without holding the session

## Goal

Audit finding F1 (`agents/audits/2026-11-20-001-whole-repository.md`):
`agent.Harness.Start` returns only once Claude has answered `initialize`, and
`Agent.Stop` returns only once the process has exited. Both run on the
session's goroutine, so every read of that session waits for them: a page's
`session/get`, and another session's `list_sessions`, `send_message`, and
`read_session`. One session starting or stopping holds up a tool call in any
other session, which breaks **Independent sessions** in normal use.

When the change is done, `Start` spawns Claude and returns at once with the
agent starting; the `initialize` response, a startup failure, the startup
limit, and the exit reach the agent as events through the one stdout reader
that runs from spawn to exit. `Stop` ends the agent at once and leaves its
process to exit or be killed in the background. No request waits on another
session's start or stop, and the synchronous `initialize` reader, the inner
event loop of `Agent.Stop`, and the session's lost-conversation path are gone.

## Related code

- `internal/server/session.go` — `run` (park timer calls `stop`),
  `startForInput` (start failure, `ErrNoConversation`, messages not
  delivered), `start`, `stop`, `parked`, `end`, `forgetConversation`,
  `lostConversation`, `markResumable`, `deliver`.
- `internal/server/methods.go` — `destroySession` calls `stop` and fails the
  destroy on its error; `sendSession` replies from `sendPrompt`.
- `internal/server/mcp.go` — `listSessions`, `sendMessage`, `readSession`
  post to other sessions and wait for the operation.
- `internal/agent/agent.go` — the `Agent` contract (`Send`, `Stop`,
  `Exited`), `Harness.Start`, `Options.Resume`, `Options.OnResumable`,
  `ErrNoConversation`.
- `internal/claude/agent.go` — phases, `Start`, `newAgent`, `Handle`,
  `handleOutput`, `exit`, `Stop`, `terminate`, the `stopping` flag.
- `internal/claude/claude.go` — `startProcess` (one-minute timer, the
  synchronous `initialize` reader and its `diagnostic`), `initialize`,
  `process.read`, `deliverOutput`, `Terminate`, `killAfter`, `children`,
  `KillAll`.
- `internal/claude/prompt.go` — `Send`, `Interrupt`, `queued`,
  `sessionState`, `dropQueued`, `endPrompt`.
- `internal/claude/configure.go` — `controlTimedOut` uses `terminate`.
- `internal/fake/fake.go` — `Stop` already publishes its last updates
  synchronously; `Start` already returns at once.
- `cmd/oxd/main.go` `run` — `s.Close()` on the first signal, `claude.KillAll`
  on the second.
- `cmd/ox/web/src/ox.tsx` `createSessionControl` — `finishPrompt` returns a
  dropped prompt's text to the composer of the page that sent it, and
  `reconcilePrompt` does so from a snapshot. A failed start reuses both
  unchanged.
- Tests: `internal/claude/claude_test.go` (startup tests driving
  `startProcess` and `initialize`), `internal/claude/prompt_test.go`
  (`agentWriting`, `stoppableAgent`, `agentRunning`,
  `TestStopDropsQueuedPrompt`, `TestUnexpectedExitIsReported`,
  `TestStopKillsClaudeIgnoringSIGTERM`, `TestTurnsClaudeStartsArePublished`),
  `internal/server/methods_test.go` (`unresumableSession` and the three
  unresumable tests, `TestSendMessage`'s failed start,
  `TestExitDropsWaitingMessages`), `internal/server/live_test.go` (the
  "a failed start reports Claude's reason" step).

## Decisions

- **Start.** `claude.Start` checks the effort, spawns Claude, starts the
  stdout reader and delivery, writes the `initialize` request (with the Stop
  hook, as today), arms the startup limit, and returns the agent in a new
  `starting` phase. It returns an error only when Claude cannot be launched
  (unknown effort, spawn failure, `oxd` shutting down), which fails the send
  at once as today. A failed write of `initialize` goes to `OnError`; the
  exit that follows reports the failure.
- **The `starting` phase** holds `input *agent.Input` (the one input accepted
  while starting, or nil), `reason string` (Claude's last failure output: a
  failed `result`'s `errorText`, or a plain-text stdout line), and
  `noConversation bool` (the reason is the documented "No conversation found
  with session ID" result; a later plain-text line clears it, as
  `initialize` does today). It ends on:
  - the `initialize` `control_response` with `subtype` `success` and no
    `noConversation`: the phase becomes `available`, and a held input is
    written through the same path as `Send` in `available` (so the phase
    becomes `prompting` and the input stays queued until Claude takes it).
    With no held input, the state is set to `idle`. A failed write drops the
    input with `prompt_cancelled` and goes to `OnError`; the exit reports the
    process.
  - a refusal (`Claude refused initialization (<subtype>): <error>`), a
    success after `noConversation`, or the startup limit (`no startup response
    within one minute`): the agent gives up. It asks the process to exit and
    ends at once with that failure (see **Ending**).
  - the process's exit: the agent ends with the failure reason, choosing
    `reason`, else the exit's stderr tail, else `Claude exited: <exit error>`.
  - `Stop`: the agent ends without a failure.
- **Lost conversation.** When the start fails with `noConversation`, the
  agent calls `OnResumable("")`, and the failure's notice is the current
  `lostConversation` text, which moves from `internal/server` to
  `internal/claude`. Claude's own reason goes to `OnError` for the log.
  `OnResumable` becomes the one path by which the resume token changes,
  including its loss; `markResumable` already keeps an empty token in memory
  when storing fails, so `forgetConversation`, `lostConversation` in the
  server, the `ErrNoConversation` branch of `startForInput`, and
  `agent.ErrNoConversation` are removed.
- **Input while starting.** `Send` in `starting` holds the input when none is
  held, publishes `prompt_queued`, and publishes `state` `running`; it
  returns `ErrBusy` when an input is already held. `Snapshot().Queued`
  reports the held input. The state stays `running` until the start ends:
  Claude's `idle` report during `starting` withdraws approvals but does not
  change the state, and `Interrupt` during `starting` drops the held input
  with `prompt_cancelled` and writes nothing to Claude. `Configure` returns
  `ErrBusy` (`idle()` already requires `available`), and `Parkable` is false.
- **Output during start.** Every message the reader delivers during
  `starting` goes through `handleOutput` as at any other time, so a turn a
  resumed conversation starts during startup is published instead of
  discarded. The exceptions are the `initialize` response and failed
  `result` lines, which feed the phase and are not published, as today.
- **One reader.** `startProcess` no longer reads stdout itself: it spawns and
  starts `process.read` and `deliverOutput`, which run from spawn to exit.
  `process.read` also delivers each nonblank line that is not JSON as a
  `plainText` event, which the agent uses only in `starting`. `initialize`,
  the startup timer that killed Claude and set the read deadline, and the
  `diagnostic` stderr read are deleted; the exit's stderr tail and the read
  deadlines `process.read` already sets after the exit replace them.
- **Ending.** One agent method ends the agent for its three causes: the
  process's exit, `Stop`, and a start Ox gave up on. It drops the held or
  queued input with `prompt_cancelled`, withdraws approvals, ends a
  configuration (`configuring` with `ErrExited`, `timedOut` with its
  error), publishes `exited` naming the latest input when a prompt was active
  or the start failed, publishes `exited` for an unexpected exit in
  `available`, and moves to `exited` and closes `done`. The reasons are the
  exit's (unchanged), the start's failure, and `Claude was stopped` for an
  active prompt that `Stop` ends. Only an exit in `timedOut` is not reported
  as unexpected, so the `stopping` flag is removed.
- **Stop.** `Agent.Stop()` returns nothing. The Claude agent asks the process
  to exit (`Terminate`) and ends at once; `Exited()` is then true. The
  process is reaped in the background: `Terminate` kills at once when SIGTERM
  cannot be sent (other than `os.ErrProcessDone`) and otherwise after
  `killAfter`; `deliverOutput` discards events once the agent's `done` is
  closed, so the reader and delivery goroutines end with the process.
  The session's `stop` becomes `agent.Stop(); parked()`, with no error path
  in the park timer, `end`, or `destroySession`. The fake's `Stop` loses its
  error result.
- **Shutdown.** `claude.WaitAll` returns once every Claude process in
  `children` has exited (a condition on `children`' mutex, signalled by
  `wait`). `cmd/oxd` calls it after `s.Close()` in the goroutine that closes
  `stopped`, so the first signal still waits for every process, bounded by
  `killAfter`, and the second signal still calls `KillAll`.
- **Requirements.**
  - **Independent sessions:** a start or stop no longer occupies the session's
    goroutine, so `session/get`, `list_sessions`, `send_message`, and
    `read_session` answer at once while another session starts or stops.
  - **Failed startup** and **Lost conversation** change (see Requirement
    changes). A send to a parked session replies once the input is held,
    before Claude has started. A failed start drops the prompt with
    `prompt_cancelled`, which returns its text to the sending page's composer
    through `finishPrompt`, and the transcript shows the `exited` notice;
    `parked` follows. Reply lost: the composer keeps the text (the page sets
    `sent` only from the reply) and the transcript shows the outcome, as
    **Send cut off** requires. Reconnect or navigation: `session/get` reports
    `running` with the held prompt in `queued`, and `reconcilePrompt`
    resolves it. A second page sees `running` and a pending prompt, so its
    Send and settings are held. The start ending in any order with an
    interrupt, a destroy, or a message is decided by the phase.
  - **Stop:** during a start, Stop drops the pending prompt and returns its
    text; Send is available again at once, and a new prompt is held for the
    start.
  - **Message delivery:** a message to a parked session goes to the starting
    agent through `deliver` as today; a failed start drops it with
    `prompt_cancelled` and shows why in the transcript, and `parked` then
    drops the rest of the inbox.
  - **Session status:** a starting session shows `running` while it holds an
    input.
  - **When settings can change:** a starting session is neither idle nor
    parked, and `Configure` refuses it with `ErrBusy`.
  - **Deleting a session:** the destroy replies once the agent has ended,
    without waiting up to ten seconds for the process.
  - **Stopping oxd:** `WaitAll` keeps the wait for every Claude process.
- **Settings.** The process starts with the session's settings as today, and
  the held input is written only after Claude has answered `initialize` with
  those flags; no configuration can run during the start.
- **Settling.** The startup limit (one minute) ends every start: by the
  `initialize` response, the exit, or the limit giving up. A give-up and a
  `Stop` end the agent at once; the process's own end is bounded by
  `killAfter`. `WaitAll` is bounded by `killAfter` and ended early by the
  second signal. The send's reply no longer waits on anything.
- **Process I/O.** One reader reads stdout to its last line, including one
  that ends at EOF, from spawn to exit; the exit is seen while a child holds
  the pipes through the existing deadlines; Claude's failure reason reaches
  the transcript from a failed result, a plain-text line, or the stderr tail.
- **Construction.** Only the `starting` phase holds an input that has not been
  written, and its fields exist only in that phase. One reader owns stdout.
  An agent the session holds is never stopping: `Stop` ends it, so the
  session has no stopping state for requests to handle. The resume token
  changes only through `OnResumable`.
- **Ownership.**
  - `starting` (Claude agent): needed so a send to a parked session replies
    at once and no other session waits on the start. Races: `Send`,
    `Interrupt`, `Stop`, `Configure`, and Claude's output during the start
    are each decided by the phase. Cleanup: it ends on the response, the
    exit, a give-up, or `Stop`.
  - The startup timer (Claude agent, through `after`): keeps **Failed
    startup** within a minute. A late firing finds another phase and does
    nothing; after the end, `done` drops it.
  - `WaitAll`'s condition (claude package `children`): keeps **Stopping
    oxd** waiting for every process now that `Stop` does not. No spawn
    follows `Server.Close`, and `KillAll` still ends the wait.
  - Removed: the `stopping` flag, `agent.ErrNoConversation`,
    `forgetConversation`.
- **Claude Code.**
  - `initialize` first, answered by a `control_response` with `request_id`
    `initialize`, success or error ([Initialize first](../../docs/claude-code.md#initialize-first)).
  - Every variant in [Startup failures](../../docs/claude-code.md#startup-failures):
    a `result` before or instead of the response, then exit; exit before
    reading `initialize` (the write fails, the result is still read); plain
    text on stdout; a stderr-only refusal with empty stdout; SIGTERM exits.
  - [Pipes](../../docs/claude-code.md#pipes): a child holding stdout delays
    EOF; the last line can end at EOF.
  - [When a turn ends](../../docs/claude-code.md#when-a-turn-ends): a resumed
    conversation can start a turn during startup, which is now published.
  - Not recorded: after `Stop`, a parked session can resume its conversation
    while the previous process is still exiting. A `TestLive` step checks it,
    and its result goes into [Stopping](../../docs/claude-code.md#stopping).
- **Session label.** `workspace/list` keeps each session's label: it still
  labels the sidebar while each session's full history loads, so the audit's
  F3 rejection stands.
- **F2.** The inbox is unchanged. The `starting` phase holds at most one
  input, as `prompting` does, and a message to a parked session reaches the
  starting agent through `deliver`. This makes F2 easier: a starting agent
  already accepts input, so F2's harness queue can take every input straight
  into it, and a failed start already drops inputs through the agent's end
  like any exit, leaving `startForInput` to clear the inbox only for a launch
  failure.

## Check your assumptions

1. **A prompt to a parked session is accepted before Claude has started.**
   Kept.
   - **Plan:** Send returns at once and the prompt shows as pending while
     Claude starts, as a prompt sent while Claude works does. If Claude
     cannot start, or can no longer resume the conversation, the prompt is
     dropped, its text returns to the composer, and the transcript says why.
     Prompts and messages then start a session, and fail to, the same way.
     Changes **Failed startup** and **Lost conversation**:
     - **Failed startup**, from "A failed Claude startup fails the send within
       a minute and shows why, in Claude's own words when it gives a reason."
       to "A failed Claude startup ends within a minute and shows why, in
       Claude's own words when it gives a reason. The prompt that started it
       is dropped, and its text returns to the composer of the page that sent
       it."
     - **Lost conversation**, from "If Claude can no longer resume the
       conversation, the send fails with a notice in the transcript, and the
       next send starts a new conversation without the earlier context." to
       "If Claude can no longer resume the conversation, the prompt is
       dropped with a notice in the transcript and its text returns to the
       composer, and the next send starts a new conversation without the
       earlier context."
   - **Alternative:** Send keeps spinning until Claude has started (about a
     second, up to a minute), and a failed start shows as the send's error
     under the composer, as today. Other sessions still are not held up. It
     costs a second way for a send to finish, answered later from the start,
     beside the way messages already fail in the transcript.

## Test plan

- Claude harness, `internal/claude/prompt_test.go`: `newAgent` returns a
  starting agent, and `agentWriting` answers `initialize` through `Handle`,
  so existing tests start from `available`.
- Claude harness, new `TestInputWaitsForStartup` in `prompt_test.go` (no
  existing test starts from `starting`): `Send` publishes `prompt_queued`
  and `state` `running` and writes nothing; a second `Send` and `Configure`
  return `ErrBusy`; `Parkable` is false; the `initialize` response writes the
  held input, which Claude's `command_lifecycle started` then takes. A second
  case: `Interrupt` drops the held input with `prompt_cancelled` and writes
  nothing, the state stays `running`, and the response publishes `idle`. A
  third: `Stop` during the start drops the held input with no `exited`.
- Claude harness, `TestTurnsClaudeStartsArePublished`: a starting agent
  publishes a turn Claude starts before its `initialize` response.
- Claude harness, `TestStartupFailureKeepsClaudesReason` rewritten to drive a
  starting agent with a held input through `Handle`: an error result then the
  exit, plain text then the exit, a line at EOF (through a stand-in), and an
  error result followed by a successful response. Each publishes
  `prompt_cancelled` then `exited` carrying Claude's text, or the lost
  notice. Only the documented result calls `OnResumable("")`.
- Claude harness, new cases in the same test for a refused `initialize` and
  the startup limit (made a package variable as `killAfter` is, and
  shortened): each ends the agent at once with its notice.
- Claude harness, `TestStartupFailureOnStderr` through `Start` and `Handle`
  with the stand-in: the `exited` reason holds the refusal within three
  seconds, including when a child holds stderr.
- Claude harness, `TestExitReportedWhileAChildHoldsStdout`,
  `TestOutputDrainsBeforeSlowDelivery`, `TestClaudeStartsWithoutAnAPIKey`,
  `TestClaudeRunsAsAnOxSession`, `TestUnknownEffortFailsToStart` follow
  `startProcess`'s new signature; the stand-in's `initialize` response now
  arrives as an event.
- Claude harness, `TestStopKillsClaudeIgnoringSIGTERM`: the process is
  spawned through `spawn`; `Stop` returns before the process exits,
  `Exited()` is true, and `WaitAll` returns once `killAfter` kills it.
  `TestStopDropsQueuedPrompt` and `TestUnexpectedExitIsReported` keep their
  assertions with `Stop` returning at once.
- Server, new `TestStartAndStopHoldUpNoOtherSession` in `methods_test.go`
  (no existing test gates a start): a stand-in Claude answers `initialize`
  only once a file exists and ignores SIGTERM. A send to session B replies
  with its `message_id` while the gate is closed; B's `session/get` and A's
  `list_sessions` (attached MCP) answer while it is closed, showing B
  `running` with the prompt queued. After the gate opens, B's prompt is
  written. Then B is destroyed while its process ignores SIGTERM: the
  destroy replies and A's `list_sessions` answers while that process is
  still alive. The test kills the stand-in in cleanup. It fails before the
  change, where B's send waits for the start.
- Server, `TestUnresumableConversationStartsAnew`,
  `TestUnresumableConversationForgottenWhenNotStored`, and
  `TestMessageToUnresumableConversationLeavesOneNotice`: the send succeeds;
  `prompt_cancelled`, the `exited` lost notice (matched by its wording), and
  `parked` follow; the history holds the notice and `parked`; the next send
  starts a new conversation and the stored token is empty.
- Server, `TestSendMessage`'s failed start and `TestExitDropsWaitingMessages`:
  adjust history expectations for the `parked` update a failed start now
  records.
- Live, `TestLive`: the failed-start step goes through the server. A stored
  session whose resume token Claude does not have gets a successful send,
  then `prompt_cancelled` and the lost notice, and its token is cleared. The
  misspelled-mode session gets a successful send, then an `exited` reason
  containing `'acceptEdit' is invalid`.
- Live, `TestLive`, new step: start a Haiku conversation with `claude.Start`,
  have it remember a word, `Stop` it, and at once `Start` it again resuming
  the conversation; it answers with the word.

## Implementation plan

1. `internal/agent/agent.go`: `Harness.Start` returns at once with the agent
   starting, and an error only when the harness cannot be launched; `Send`
   accepts an input during the start; `Stop()` ends the agent at once and
   returns nothing; `OnResumable("")` means no conversation to resume; remove
   `ErrNoConversation` and its mention in `Options.Resume`.
2. `internal/claude/claude.go`: `startProcess` spawns and starts
   `process.read` and `deliverOutput`, and returns; delete `initialize`;
   `process.read` delivers `plainText`; `deliverOutput` takes `done`;
   `Terminate` returns nothing and kills when SIGTERM fails; add the startup
   limit variable and `WaitAll`.
3. `internal/claude/agent.go`: the `starting` phase; `Start` writes
   `initialize` and arms the startup limit; `Handle` and `handleOutput` route
   the `initialize` response, failed results, `plainText`, and the startup
   timer during `starting`; the shared ending; non-blocking `Stop`; remove
   `stopping`; the lost notice and `OnResumable("")`.
4. `internal/claude/prompt.go`: `Send`, `Interrupt`, `queued`, and
   `sessionState` in `starting`. `internal/claude/configure.go`:
   `controlTimedOut` calls `proc.Terminate`.
5. `internal/fake/fake.go`: `Stop()` without a result.
6. `internal/server/session.go`: `stop`, the park timer, `end`, and
   `startForInput` without start-failure branches other than a launch
   failure; remove `forgetConversation` and `lostConversation`.
   `internal/server/methods.go`: `destroySession` without the stop error.
7. `cmd/oxd/main.go`: call `claude.WaitAll()` after `s.Close()`.
8. Tests as in the Test plan, then `make test`, `make e2e`, and `make live`.
9. Documentation as below.

## Requirement changes

- **Changed:** **Failed startup.** Old: "A failed Claude startup fails the
  send within a minute and shows why, in Claude's own words when it gives a
  reason." New: "A failed Claude startup ends within a minute and shows why,
  in Claude's own words when it gives a reason. The prompt that started it is
  dropped, and its text returns to the composer of the page that sent it."
- **Changed:** **Lost conversation.** Old: "If Claude can no longer resume
  the conversation, the send fails with a notice in the transcript, and the
  next send starts a new conversation without the earlier context." New: "If
  Claude can no longer resume the conversation, the prompt is dropped with a
  notice in the transcript and its text returns to the composer, and the next
  send starts a new conversation without the earlier context."

## Documentation updates

- `docs/architecture.md`: the Sessions section (the token cleared through
  `OnResumable`, not `ErrNoConversation`; the park timer ends the agent at
  once); the Agents section (`Start` returns at once, `Stop` ends the agent
  at once); the Claude agent's reader paragraph (one reader from spawn to
  exit) and phase table (`starting`, and `exited` reached by exit, `Stop`, or
  a failed start); the `cmd/oxd` row (holds the socket until every Claude
  process has exited).
- `docs/protocol.md`: Workspaces and sessions (a lost conversation drops the
  prompt); `session/send` (a parked session replies once the prompt is held;
  a failed start drops it and reports `exited`); `session/destroy` (replies
  without waiting for the process, which is killed after ten seconds);
  `prompt_queued` (written, or held while Claude starts); `exited` (the
  process could not start or resume).
- `docs/claude-code.md` Stopping: the live check's result.
