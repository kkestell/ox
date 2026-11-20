# The harness alone owns the written input

## Goal

The fact "an input is written to the harness and the harness has not taken it
yet" is kept three times: `session.written` in the server, `submission.queued`
in the Claude harness, and `queued` in the fake harness. The server's copy
exists only to remember the input's text and attribution, so it can rewrite
the harness's `prompt_queued`, `prompt`, and `prompt_cancelled` updates and
list the input in `session/get`. `agentUpdate` dereferences `sess.written` on
every such update, trusting that the harness and the server agree; a harness
update the server's copy does not expect panics `oxd`, and a harness that
ends without reporting the drop would leave the server's copy set forever,
holding the session busy with no agent to clear it.

After the change the harness is the only owner. It receives the input's text
and attribution with the input, publishes its lifecycle updates complete, and
reports the untaken input in its snapshot. The server keeps only its inbox,
passes the harness's updates through, and has no copy to disagree with.

## Related code

- `internal/server/session.go:35` — `written *pendingInput`, set by
  `writeInput` (`:138-150`), read by `inputPending` (`:121-123`), `pending`
  (`:165-171`), and `agentUpdate` (`:173-185`), which rewrites and clears it.
  `pendingInput` (`:40-50`) is the inbox element type.
- `internal/server/methods.go:308-322` — `session/get` embeds the agent's
  `Snapshot` and adds `queued` from `sess.pending()`.
- `internal/server/methods.go:426` — `session/configure` refuses with
  `ErrBusy` on `inputPending`.
- `internal/server/mcp.go:271-279` — `messageInput` builds a message's
  `pendingInput`.
- `internal/agent/agent.go:24-27, 161-170` — the `Send` contract, `Snapshot`,
  and `Input{MessageID, Content}`.
- `internal/claude/prompt.go:26-104` — `submission.queued` (the ID only),
  `Send`, `Interrupt`'s `cancel_queued`, `queued()`, `commandStarted`,
  `commandCancelled`, `dropQueued`; `internal/claude/agent.go:214-216, 228-231`
  — `Snapshot` and the exit path's drop.
- `internal/fake/fake.go:22, 113-168` — `queued *agent.Input`, `Snapshot`,
  `Send`, `take`, `drop`.

## Decisions

- **Construction:** The structural cause is that the server keeps its own copy
  of a harness fact and interprets the harness's updates through it. The only
  reason for the copy is that `agent.Input` lacks the text and attribution the
  updates carry. Giving `agent.Input` those fields lets the harness, which
  alone observes the input being written, taken, and dropped, publish complete
  updates and report the input in `Snapshot`. With `session.written` gone,
  there is no second copy, so the disagreement that `agentUpdate`'s
  dereference assumed away cannot be represented, and a parked session cannot
  hold a written input, since the fact goes away with the agent.
  - `agent.Input` becomes the one input type: `MessageID`, `Text`, `From`,
    `FromLabel` (JSON `message_id`, `text`, `from,omitempty`,
    `from_label,omitempty`) and `Content` (`json:"-"`), what the model
    receives. `Input.Update(kind string) Update` builds the lifecycle update
    with `MessageID`, `Type`, `Text`, `From`, and `FromLabel`. Both harnesses
    and the server's inbox build their updates through it. `pendingInput` is
    deleted, and the inbox becomes `[]agent.Input`.
  - `agent.Snapshot` gains `Queued *Input` with `json:"-"`: the input written
    to the harness and not yet taken. It is a pointer because the `Send`
    contract allows at most one, and `json:"-"` because `session/get` lists it
    in the protocol's `queued` array, ahead of the inbox, rather than as a
    snapshot field. `session/get` builds `queued` as that input, if any,
    followed by `sess.inbox`, always a JSON array.
  - The Claude harness keeps `submission.queued` as `*agent.Input` (nil when
    none) and `queued()` returns it; `prompt.last` and `turn` stay IDs.
    `commandStarted`, `commandCancelled`, `dropQueued`, and `Send` publish
    `input.Update(...)`. The fake harness already holds `*agent.Input` and
    publishes through `Input.Update` in `Send`, `take`, and `drop`. Both
    report the untaken input in `Snapshot`.
  - The busy rule for a written input is decided once, by the harness's `Send`
    and `Configure`, which already refuse while an input is untaken (Claude's
    `prompting` phase, the fake's running state). The server keeps the rule it
    owns: waiting inbox messages go ahead of a user prompt and hold off a
    configuration. `inputPending` becomes `messagesWaiting()`, which reports
    `len(sess.inbox) != 0`. `sendPrompt` checks it, starts the agent, and
    returns `Send`'s result; `deliver` calls `sess.agent.Send` directly.
    `writeInput` and `pending` are deleted.
  - `agentUpdate` passes every update through except a `prompt_queued` with
    `From` set, which the inbox already announced at acceptance. With nothing
    left to rewrite, it is the only filter on harness updates.
  - Other places with the same structure: the server keeps no other per-input
    copy of harness state. `sess.settings` and `sess.resumeToken` are the
    session's own stored facts, which the harness reports changes to and which
    must outlive the process; they are not copies of a harness-owned fact and
    stay as they are.
- **Requirements:** One prompt at a time, Stop, Messages in the transcript, and
  Message delivery hold unchanged. The updates and `session/get` carry the same
  fields in the same order: `prompt_queued` is still published by the harness
  right after the write and before `state` `running`, so a reconnecting page's
  `session/get` and the live updates agree as before. A second page sending
  while an input is untaken still receives `-32002` from the harness's `Send`.
- **Settling:** No new waits.
- **Ownership:** No state, timer, lock, or stored field is added;
  `session.written` is removed.
- **Claude Code:** No new assumption. The Claude harness still clears its
  untaken input on `command_lifecycle` `started` or `cancelled` and drops it
  with `cancel_queued` or on exit, as `docs/claude-code.md` records under
  `command_lifecycle`, When a prompt is taken, and Interrupt. The bytes written to Claude are unchanged: `Content` under
  `MessageID`.
- **Overlap with the later `claude.Agent` restructuring:** `submission` is
  valid only in the `prompting` phase, and `queued()` checks the phase before
  reading it. This plan keeps that shape and changes only `queued`'s type. The
  later plan that makes the phase carry its own fields should fold
  `submission`, with its `*agent.Input`, into the `prompting` value, and
  reconsider `turn` beside `prompt.last`.

## Check your assumptions

None.

## Test plan

- Claude harness, `internal/claude/prompt_test.go`: give `prompt` inputs a
  `Text`, and add a message-like input with `From` and `FromLabel` to
  `TestPromptSentWhileClaudeWorks`. Assert that `prompt_queued` and, after
  `command_lifecycle` `started`, `prompt` carry its `Text`, `From`, and
  `FromLabel`; that `Snapshot().Queued` is that input while it is untaken; and
  that it is nil once taken.
- Claude harness, `TestStopDropsQueuedPrompt`: assert that `prompt_cancelled`
  carries the dropped input's `Text`, both for the interrupt's
  `command_lifecycle` `cancelled` and for the exit path, and that
  `Snapshot().Queued` is nil after each.
- Server with the fake harness: `TestPromptQueuedWhileClaudeWorks` and
  `TestSendMessage` already assert `text`, `from`, and `from_label` on
  `prompt_queued`, `prompt`, and `prompt_cancelled`, the `queued` list from
  `session/get` in order, `[]` when empty, `-32002` for a second input, and
  that a message is not announced twice. With the server no longer rewriting
  updates, these now observe what the fake harness publishes and reports;
  they keep their assertions and decode `queued` into `agent.Input` in place
  of `pendingInput`. Add to `TestPromptQueuedWhileClaudeWorks` that
  `session/configure` returns `-32002` while the prompt is untaken, since the
  harness now decides that alone.
- Live: run `make live`, since the Claude harness now builds the prompt
  lifecycle updates that `TestLive`'s steering and message cases observe.

## Implementation plan

1. `internal/agent/agent.go`: add `Text`, `From`, `FromLabel`, and JSON tags
   to `Input`, and `Input.Update`. Add `Snapshot.Queued`. Update the `Send`
   doc: its lifecycle updates carry the input's text and attribution, and
   `Snapshot` reports the untaken input until then.
2. `internal/claude/prompt.go`, `internal/claude/agent.go`: hold and publish
   the input as the Construction decision describes, and report it in
   `Snapshot`.
3. `internal/fake/fake.go`: publish through `Input.Update` and report `queued`
   in `Snapshot`.
4. `internal/server/session.go`: delete `written`, `pendingInput`,
   `writeInput`, and `pending`; make the inbox `[]agent.Input`; replace
   `inputPending` with `messagesWaiting`; simplify `sendPrompt`, `deliver`,
   and `agentUpdate`; stamp inbox updates (`acceptMessage`, `cancelInbox`,
   `deliver`'s failure) with `time.Now()` through one helper.
5. `internal/server/methods.go`: build `session/get`'s `queued` from
   `Snapshot.Queued` and the inbox; use `messagesWaiting` in
   `session/configure`. `internal/server/mcp.go`: `messageInput` returns
   `agent.Input`.
6. Tests as the test plan describes, in `internal/claude/prompt_test.go` and
   `internal/server/methods_test.go`.
7. `docs/architecture.md`, as below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, Sessions: the session owns the inbox and its
  messages until written; the agent owns a written input, with its text and
  attribution, until it is taken or dropped. Agents: `Send` receives the
  input's text and attribution with it, and the agent publishes the lifecycle
  updates with them and reports the untaken input in `Snapshot`, replacing
  "The session publishes those lifecycle updates with the input's text and
  attribution."
