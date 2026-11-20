# Untaken inputs owned by the agent

## Goal

Audit finding F2 (`.agents/audits/2026-11-20-001-whole-repository.md`): an
input Claude has not taken lives in one of two places. The agent holds at most
one, and the session's inbox holds the messages behind it. Each owner
announces its own, so a message is announced twice and the session filters the
second; `session/get` and `read_session` join the two lists; and the rule that
keeps them consistent is held by `deliver` running after every operation and
event and by three callers clearing the inbox when the agent goes away. The
last four defects in this area came from that split.

When the change is done, the agent owns every untaken input, in order. The
session hands it each prompt and message as it arrives, starting a parked
session first. The agent announces each input once, writes the next to its
process when the previous is taken or dropped, reports them all in
`Snapshot`, and drops them all on interrupt or when it ends. The inbox,
`deliver`, `messagesWaiting`, `cancelInbox`, `inboxUpdate`, the
`prompt_queued` filter, and the join in `queued` are gone.

## Related code

- `internal/server/session.go` — `inbox`, `run` (calls `deliver` after every
  operation and event), `deliver`, `queued`, `messagesWaiting`, `sendPrompt`,
  `acceptMessage`, `cancelInbox`, `inboxUpdate`, `agentUpdate`,
  `startForInput` (messages branch), `parked` (clears the inbox), `snapshot`.
- `internal/server/methods.go` — `getSession` joins `Snapshot` and
  `sess.queued()`; `interruptSession` calls `cancelInbox`;
  `configureSession` checks `messagesWaiting`.
- `internal/server/mcp.go` — `sendMessage` posts `acceptMessage` and replies
  `Message queued`; `readSession` calls `stateLine(snapshot, len(queued()))`.
- `internal/agent/agent.go` — the `Send`, `Interrupt`, and `Snapshot.Queued`
  contracts (one untaken input, `ErrBusy` for a second).
- `internal/claude/agent.go` — `starting.input`, `prompting.queued`,
  `started`, `end`, `Snapshot`.
- `internal/claude/prompt.go` — `Send`, `write`, `Interrupt`, `queued`,
  `waiting`, `commandStarted`, `commandCancelled`, `dropQueued`,
  `sessionState`, `endPrompt`.
- `internal/claude/configure.go` — `endConfiguration`; `Configure` requires
  `idle()`.
- `internal/fake/fake.go` — `queued`, `Send`, `take`, `drop`, `Handle`
  (`configured`, `tick`), `Interrupt`, `Stop`, `Snapshot`.
- Tests: `internal/claude/prompt_test.go`, `internal/claude/configure_test.go`,
  `internal/server/methods_test.go` (`TestSendMessage`,
  `TestPromptQueuedWhileClaudeWorks`, `TestExitDropsWaitingMessages`,
  `TestMessageToUnresumableConversationLeavesOneNotice`).

## Decisions

- **One owner.** The agent observes every event that changes an untaken
  input: its acceptance (`Send`), Claude taking or dropping it, an interrupt,
  and the agent's end. The session observed none of them except acceptance,
  which is why it needed guards. The session keeps no input.

- **`Send` accepts every input.** `Send` queues the input behind every input
  not yet taken, in every live phase, and publishes `prompt_queued` once. It
  returns an error only when it writes the input at once and the write fails;
  it then publishes nothing and keeps nothing. `ErrBusy` leaves `Send`.

- **The user-prompt refusal.** `sendPrompt` refuses a user prompt with
  `ErrBusy` while `Snapshot().Queued` is non-empty, before starting a parked
  session. It is the one place the rule lives, and it reads the agent's queue,
  the one source. A message is never refused for being behind other inputs.

- **Inputs during a configuration.** A configuration no longer refuses
  inputs: the agent holds any input sent while one runs and writes the first
  once it ends, so the change still reaches Claude before the input. The page
  that changed a setting holds Send meanwhile, as now, so only a send from
  another page or an MCP message reaches this. This removes the only reason
  for a harness to tell user prompts from messages. `Configure` still
  requires the agent idle with nothing untaken, which `idle()` already
  implies.

- **Claude agent queue.** `Agent.untaken []agent.Input` holds every input
  Claude has not taken, oldest first. It replaces `starting.input` and
  `prompting.queued`. Whether the first is written is derived from the
  phase: in `prompting` the first untaken input has always been written; in
  `starting`, `configuring`, and `timedOut` none has; in `available` there
  are none. `prompting.input` stays, naming the latest input written for the
  releasing update.
  - `Send` appends, and writes at once when the queue was empty and the phase
    is `available` or `prompting`.
  - `writeNext` writes the first untaken input, moving to `prompting`. A
    failed write calls `OnError` and drops every untaken input with
    `prompt_cancelled`; Claude is exiting, and its exit ends the agent.
  - `ready` is the one way into `available`: it sets the phase, calls
    `writeNext`, and sets the state `idle` if the phase is still `available`.
    `started`, `endPrompt`, and `endConfiguration` use it.
  - `commandStarted` for the first untaken input removes it, publishes
    `prompt`, and calls `writeNext`. `commandCancelled` for it removes it,
    publishes `prompt_cancelled`, and calls `writeNext`.
  - `sessionState(idle)` in `prompting` keeps waiting while `untaken` is
    non-empty, as it does now for `queued`.
  - `Interrupt` drops every untaken input not written, oldest first, with
    `prompt_cancelled`. In `available` and `prompting` with the state not
    `idle`, it then writes the interrupt, with `cancel_queued` when one input
    is written; Claude's `cancelled` drops that one. In `starting`,
    `configuring`, and `timedOut` it writes nothing.
  - `end` drops every untaken input, oldest first, before it ends a
    configuration, and its `exited` names the latest input written in
    `prompting` or the latest untaken input in `starting`. It calls a
    configuration's `done` directly rather than through `endConfiguration`,
    so ending never writes.

- **Fake agent queue.** `queue []agent.Input` replaces `queued`. `Send`
  appends and publishes `prompt_queued`, then starts a turn with the first if
  the agent is idle with no configuration. Each `tick` takes the next queued
  input, as `take` does now. The `configured` event starts a turn with the
  first queued input after `done`. `Interrupt` and `Stop` drop every queued
  input, oldest first, whatever the state.

- **Snapshot.** `Snapshot.Queued` becomes `[]Input` with the JSON name
  `queued`: every untaken input, oldest first, `[]` when empty. `getSession`
  returns the snapshot as is; the parked snapshot carries an empty queue.
  `stateLine` takes only the snapshot.

- **A message that cannot be accepted.** `acceptMessage` assigns the message
  ID, starts a parked session, and calls `Send`, returning any error.
  `sendMessage` replies `Message queued for <to>.` only on success, and the
  error otherwise. A harness that cannot be launched, or a write that fails at
  once, therefore reaches the sending Claude as the tool's error, and the
  recipient shows nothing. A start that fails after launch drops the message
  through the agent's end, with `exited` in the recipient's transcript, as
  now. See Check your assumptions.

- **Order with F1.** F1 has landed (`ac6f188`): a message to a parked session
  goes straight to the starting agent, which holds it with any others until
  Claude answers `initialize`.

- **Requirements.**
  - **One prompt at a time:** Send is refused while any input is untaken,
    from `Snapshot().Queued`; the page's `canSend` is unchanged. A second page
    sending meanwhile gets `-32002`; a reconnect or reload reads `queued` from
    `session/get`, which now comes from the agent alone.
  - **Stop:** an interrupt drops the pending prompt and every untaken
    message, each with one `prompt_cancelled`, so the prompt's text returns
    to the page that sent it.
  - **Message delivery:** messages wait in order behind the written input,
    including through a configuration; a message to a parked session starts
    it; a failed start, Claude's exit, and `oxd` stopping drop them through
    the agent's end. The launch-failure case changes as Check your
    assumptions describes.
  - **Messages in the transcript:** each message is announced once, by the
    agent, and stays pending on every page until taken or dropped.
  - **Pending setting changes:** an input sent during a configuration is
    written after the last acknowledgement, so the change reaches Claude
    first.
  - **When settings can change:** a configuration still requires the agent
    idle with nothing untaken.
  - **Parking:** untaken inputs keep the agent out of `available`/`idle`, so
    it is not parkable.
  - **Failed startup:** unchanged; the start's end drops the user's prompt,
    and any messages, with `prompt_cancelled`.
  - **Every request settles:** `send_message` replies on the recipient's
    goroutine with the outcome of `Send`.
- **Settling.** No new wait. `send_message` replies from the same posted
  operation it does now; a launch is non-blocking since F1.
- **Construction.**
  - Every untaken input has one owner, the agent; the session has no field
    for one.
  - Each input is announced once, by the agent that accepts it.
  - The Claude agent writes at most one untaken input, the first, and only in
    `prompting`; `available` has none. `Send`, `writeNext`, and `ready` are
    the only writers, and `ready` the only way into `available`.
  - The user-prompt refusal lives in `sendPrompt` and reads
    `Snapshot().Queued`.
  - Untaken inputs end with the agent, in `end`; no caller clears them.
- **Ownership.** `claude.Agent.untaken` and `fake.Agent.queue` replace the
  session's `inbox`, `starting.input`, `prompting.queued`, and the fake's
  `queued`. Owner: the agent, on the session goroutine. Needed for
  **Message delivery** and **One prompt at a time**. Races: none new; every
  change happens in `Send`, `Handle`, `Interrupt`, or `Stop`. Cleanup: `end`
  (Claude) and `Stop` (fake) drop them; the session parks after the agent
  ends and holds nothing to clear.
- **Claude Code.** The agent writes the next input as soon as Claude takes
  or drops the previous one, which is while a turn runs or is winding down.
  [User messages](../../docs/claude-code.md#user-messages) records each
  variant for a prompt written during a turn: taken after the running tool's
  `tool_result`, held while `can_use_tool` waits, or run as its own turn
  after the `result` with no `idle` between. Claude can report `idle` before
  taking a written prompt ([When a prompt is taken](../../docs/claude-code.md#when-a-prompt-is-taken));
  `sessionState` keeps waiting. `cancel_queued` drops only the written input,
  as [Interrupt](../../docs/claude-code.md#interrupt) records; the rest were
  never written. Writing after Claude's `cancelled` is what the session's
  `deliver` does today.

## Check your assumptions

1. **A message to a session whose Claude cannot be launched at all.** Kept.
   - **Plan:** the sending Claude's `send_message` call fails with the reason
     (for example, the Claude program is missing), and nothing appears in the
     recipient's transcript, as a user's send to that session fails with the
     reason in the composer. A start that fails after Claude launches still
     drops the message with the reason in the recipient's transcript, and
     the sender is not told.
   - **Alternative:** keep today's behavior: the sender is told the message
     was queued, and the recipient's transcript shows the message dropped
     with the reason. The session keeps a path of its own for messages that
     the agent never received.
   - Changes **Message delivery**. Old: "A message from another session
     reaches Claude as a prompt sent while it works does: Claude takes it into
     the running turn after its current step, or runs it next, and an idle
     session starts a turn with it. Messages that arrive while a prompt or
     message is pending wait their turn in order, and a message to a parked
     session starts it. Messages Claude has not taken are dropped if the
     session cannot start, when Claude exits, or when `oxd` stops; a failed
     start shows why in the transcript." New: see Requirement changes.

## Test plan

- Claude harness, `TestPromptSentWhileClaudeWorks`: with one input written
  and untaken, two more inputs (a prompt and a message) are accepted, each
  publishes one `prompt_queued`, neither is written, and `Snapshot().Queued`
  lists all three in order. Claude taking the first writes the second only;
  taking the second writes the third. Replaces the `ErrBusy` probes with
  `Snapshot().Queued` checks. Fails before: the second `Send` returns
  `ErrBusy`.
- Claude harness, `TestPromptReleasedAfterClaudeTakesIt`: replace the
  `ErrBusy` probes with `Snapshot().Queued` checks; the behavior asserted is
  unchanged.
- Claude harness, `TestStopDropsQueuedPrompt`: with one input written and two
  held, `Interrupt` publishes `prompt_cancelled` for the two held at once,
  oldest first, and writes `cancel_queued`; Claude's `cancelled` drops the
  written one. An input sent after the interrupt and before Claude's
  `cancelled` is written on that `cancelled`. `Stop` with one written and one
  held drops both, oldest first, then publishes `exited` naming the written
  one.
- Claude harness, `TestInputWaitsForStartup`: two inputs held while starting
  are both accepted and reported; `initialize` writes only the first; taking
  it writes the second. `Interrupt` drops both; a failing writer drops both
  with one `OnError`; `Stop` drops both. Fails before: the second `Send`
  returns `ErrBusy`.
- Claude harness, `TestConfigureWaitsForAcknowledgements`: a `Send` during
  the configuration is accepted and published but not written while control
  requests are in flight; the last acknowledgement writes it. Fails before:
  `ErrBusy`.
- Claude harness, `TestConfigureTimeoutStopsProcess`: a `Send` after the
  timeout is held, and the exit drops it with `prompt_cancelled`.
- Server, `TestSendMessage`:
  - "messages wait their turn in order" and the announced-once check stay,
    now reached through the fake's queue.
  - "Stop drops every message" expects `prompt_cancelled` oldest first.
  - New step on idle B: a configuration is sent without waiting, then a
    message and a user prompt; both publish `prompt_queued`, the
    configuration succeeds, and both are taken in order after it. Fails
    before: the user prompt returns `-32002`.
  - "a failed start drops the messages" becomes: `send_message` to the
    `broken` session returns the launch error as a refusal, and D receives no
    update. The retry check goes.
- Server, `TestPromptQueuedWhileClaudeWorks`: unchanged; its refused second
  prompt now fails if `sendPrompt`'s queue check is removed.
- Server, `TestExitDropsWaitingMessages` and
  `TestMessageToUnresumableConversationLeavesOneNotice`: unchanged; they now
  cover the Claude agent's end dropping two untaken inputs, one written and
  one held, in order.
- Live: the existing steer and stop cases in `TestLive` drive the changed
  write path.

## Implementation plan

1. `internal/agent/agent.go`: rewrite the `Send` contract (queues behind
   every untaken input, publishes `prompt_queued` once, writes the next when
   the previous is taken or dropped, errors only on a failed immediate
   write), the `Interrupt` contract (drops every untaken input), `Stop`
   (drops every untaken input), and `Snapshot.Queued` (`[]Input`, JSON
   `queued`, every untaken input oldest first).
2. `internal/claude/agent.go`, `prompt.go`, `configure.go`: add `untaken`;
   remove `starting.input` and `prompting.queued`; implement `Send`,
   `writeNext`, `ready`, `commandStarted`, `commandCancelled`,
   `sessionState`, `Interrupt`, and `end` as Decisions describe; `queued`
   returns a copy of `untaken`; `waiting` matches the first untaken input in
   `prompting`; remove `dropQueued` if nothing else needs it.
3. `internal/fake/fake.go`: replace `queued` with `queue` as Decisions
   describe.
4. `internal/server/session.go`: remove `inbox`, `deliver` and its call in
   `run`, `queued`, `messagesWaiting`, `cancelInbox`, `inboxUpdate`, and
   `agentUpdate` (pass `sess.update` as `OnUpdate`). `sendPrompt` refuses
   while `snapshot().Queued` is non-empty. `acceptMessage` returns an error
   and calls `startForInput` and `Send`. `startForInput` loses its messages
   branch. `parked` stops clearing. The parked `snapshot` has an empty
   `Queued`.
5. `internal/server/methods.go`: `getSession` drops its `Queued` field;
   `interruptSession` drops `cancelInbox`; `configureSession` drops the
   `messagesWaiting` check.
6. `internal/server/mcp.go`: `sendMessage` replies with `acceptMessage`'s
   error or `Message queued`; `stateLine` takes only the snapshot.
7. Tests as the Test plan describes.
8. Documentation updates below, and the requirement change once approved.

## Requirement changes

- **Changed:** **Message delivery.**
  - Old: "A message from another session reaches Claude as a prompt sent
    while it works does: Claude takes it into the running turn after its
    current step, or runs it next, and an idle session starts a turn with it.
    Messages that arrive while a prompt or message is pending wait their turn
    in order, and a message to a parked session starts it. Messages Claude
    has not taken are dropped if the session cannot start, when Claude exits,
    or when `oxd` stops; a failed start shows why in the transcript."
  - New: "A message from another session reaches Claude as a prompt sent
    while it works does: Claude takes it into the running turn after its
    current step, or runs it next, and an idle session starts a turn with it.
    Messages that arrive while a prompt or message is pending wait their turn
    in order, and a message to a parked session starts it. A message to a
    session whose Claude cannot be launched is refused, and the sending
    Claude gets the reason. Messages Claude has not taken are dropped if
    Claude fails to start, when Claude exits, or when `oxd` stops; a failed
    start shows why in the transcript."

## Documentation updates

- `docs/architecture.md`: the session's owned state (no inbox); replace the
  inbox paragraph with the agent owning every untaken input and the session
  handing it each input, starting a parked session first; the Agents
  paragraph (`Send` queues behind untaken inputs, `Snapshot` reports them
  all); the Claude phase table and the paragraph after it (`starting` and
  `configuring` hold inputs, `prompting` has the first written, inputs
  written as each is taken or dropped, interrupt behavior per phase).
- `docs/protocol.md`: `session/send` (`-32002` only while any input is
  untaken; a send during a configuration waits for it); `session/get`
  `queued` (every untaken input, oldest first, with no inbox);
  `session/configure` (sends no longer refused during a configuration);
  `session/interrupt` (no inbox); `prompt_queued` (published once, at
  acceptance, for every input); `send_message` (accepted into the session's
  queue, refused with the launch error when Claude cannot be launched).
