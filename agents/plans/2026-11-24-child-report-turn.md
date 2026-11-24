# Bind a child's message to its parent to the turn that made it

## Goal

A child that calls `send_message` to its parent sends no report for that step.
Today the child's goroutine keeps one `told` value for whatever turn is in
progress, and `sendMessage` sets it once the parent accepts. When the parent is
slow to accept, the child's turn can end, by an interrupt or an exit, and a new
turn start before the post runs. The post then marks the new turn as told, and
that turn ends with no report, breaking **Child reports**.

After this change the told state belongs to one turn's value. `sendMessage`
takes the caller's current turn before it waits on the parent and marks only
that turn, so a message accepted after its turn ended changes nothing.

Resolves [#71](https://github.com/kkestell/ox/issues/71), from
[audit finding F1](../audits/2026-11-24-audit-2026-11-24.md#f1-a-late-child-message-suppresses-the-next-turn-report).

## Related code

- `internal/server/session.go` — `toParent` holds `reports` and `told`;
  `newSession` builds it for a child; `report` moves `told` on `tool_result`
  and `tool_use`, reads it at `result`, and clears it at `result` and `exited`;
  `free` is the busy check.
- `internal/server/mcp.go` — `sendMessage` waits for the parent's
  `acceptMessage`, then posts to the caller, sets `toldSent` unless the caller
  is `free()`, and answers the call from that post.
- `internal/server/mcp_test.go` — `TestChildReports` covers the idle-child case
  (review F2 of the child reports change) and the told step;
  `sendGateAgent` holds an agent's `Send`, used by
  `TestChildStartHoldsUpNoOtherSession`.

## Decisions

- **Turn value.** A new type `turn` in `session.go` holds `told told` for one
  turn of a child. `toParent` replaces its `told` field with `turn *turn`,
  never nil: the turn in progress, or the next one while the child is free.
  `newSession` builds `&toParent{turn: &turn{}}`. `report` reads and moves
  `t.turn.told` as it moves `t.told` today, and at `result` and `exited`
  replaces `t.turn` with a new `&turn{}` instead of resetting `told`. The
  `told` enum and its step rules are unchanged.
- **`sendMessage`.** When the caller is a child of the recipient, before posting
  to the recipient it asks the caller's goroutine for its turn:
  `ask(caller, func() *turn { … })` returns nil when `caller.free()`, else
  `caller.toParent.turn`. After the parent accepts, a nil turn answers the call
  at once. A non-nil turn posts to the caller, sets `current.told = toldSent`,
  and answers from that post, so the call's `tool_result` still follows it. The
  post no longer checks `free()`. A message to anyone other than the parent
  takes no turn and answers as today. A caller whose goroutine has ended
  returns nil from `ask`, and the call is answered without a post.
- **Why the turn is taken before the wait.** The call is made by the turn in
  progress when it arrives, and the step that made it cannot end until Ox
  answers it, except by the turn ending. Taking the turn before the wait ties
  the call to that turn; a turn that ends during the wait is replaced, and the
  late post writes to the replaced value, which nothing reads. This removes the
  busy check at post time as a stand-in for turn identity. Within a turn the
  step rules are unchanged: the call blocks its step, and parallel calls emit
  every `tool_use` before any `tool_result`
  ([`user`](../../docs/harnesses/claude-code.md#user)).
- **Rare case.** If Ox reads a call before it has handled the previous turn's
  `result` on stdout, or a background task's automatic turn calls
  `send_message` before the child's state becomes `running`, the call takes the
  previous turn or none, and the parent receives both the message and the
  turn's report. The extra report is harmless and the ordering needs a model
  call to lose a race with a local pipe, so the plan does not guard it.
- **Requirements.** **Child reports** holds as written: the parent now receives
  the report of a turn that did not message it, and still receives none for a
  turn whose last step did. Message delivery and the MCP result are unchanged.
- **Settling.** The new `ask` ends when the caller's goroutine runs it, or at
  once when the caller has ended. It runs on the MCP call goroutine, which is
  not a session goroutine, so the ancestor-to-descendant wait rule holds.
- **Construction.** The told fact lives in the value of the turn that made the
  call; a turn's end replaces the value, so no told state can outlive its turn
  and no reader compares turn identities.
- **Ownership.** `toParent.turn`: owned by the child's goroutine, written by
  `report` and by `sendMessage`'s post on that goroutine; needed so a child that
  told its parent sends no second copy, and a later turn still reports; races:
  a turn ending while the parent accepts, which now writes only to the ended
  turn; cleanup: replaced at each `result` and `exited`, dropped with the
  session. The commit body repeats this.
- **Claude Code.** Relies only on the `tool_result` and `tool_use` order around
  an MCP call already recorded under
  [`user`](../../docs/harnesses/claude-code.md#user). No new assumption.

## Check your assumptions

None.

## Test plan

- Server, `TestChildReports`, new case "a message accepted after its turn ended
  leaves the next turn's report": the test's harness start wraps each fake
  agent in `sendGateAgent`, whose gate holds a `Send` whose input is a message
  from child `one` with text `late`, and passes others through.
  1. Send `one` `wait 60000` and wait for its `prompt`.
  2. From `one`'s attachment, send `send_message` to `p` with text `late`
     without waiting for the reply, and wait until the gate holds the parent.
  3. Interrupt `one`, then send it `approve Bash {}` and wait for its
     `approval`.
  4. Release the gate and wait for the `send_message` reply.
  5. Approve, and require `reportsTaken(t, c, p, map[string]int{one: 2})`, with
     the reports `Turn failed: Interrupted` and the approval turn's reply, and
     the parent's prompt from `one` with text `late`.

  It fails today: the approval turn's report is missing.
- The existing cases stay: the idle child's message leaves its next report
  (nil turn), and the child that told its parent while waiting on approval
  sends no report for that step.
- `sendGateAgent.gate` becomes `func(agent.Input)`;
  `TestChildStartHoldsUpNoOtherSession` ignores the input.

## Implementation plan

1. `internal/server/session.go`: add `turn`; replace `toParent.told` with
   `turn *turn`; build it in `newSession`; in `report`, use `t.turn.told` and
   replace `t.turn` at `result` and `exited`; update the comments on
   `toParent` and `told`.
2. `internal/server/mcp.go`: in `sendMessage`, take the caller's turn before
   posting to the recipient, and after acceptance set `toldSent` on it from a
   post to the caller; replace the comment.
3. `internal/server/mcp_test.go`: change `sendGateAgent.gate` and its caller;
   wrap `TestChildReports`'s harness; add the case above.
4. Documents below; `make docs`.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`: the `told` passage under Sessions: the state belongs
  to the child's current turn, which `result` and `exited` replace;
  `send_message` takes the caller's turn before it waits on the parent, and
  marks only that turn.
- `docs/protocol.md`, Child reports: the `send_message` that suppresses a report
  is one made during that turn.
