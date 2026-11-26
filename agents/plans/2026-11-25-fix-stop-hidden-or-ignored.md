# One Send/Stop button that stops reliably

## Goal

Stop can vanish for a whole turn and can be swallowed during a settings change
([#111](https://github.com/kkestell/ox/issues/111); audit
`agents/audits/2026-11-25-ox-sweep.md`, F4 and F8). The composer also shows
Stop beside Send, where ChatGPT and Claude Desktop show one button that is Send
or Stop.

- **F4.** The page hides Stop from the click until it sees the session's
  published state leave `running`. When it never sees that transition, because
  it reconnected while a later turn was running or because an input taken
  during the interrupt kept the state `running` into the next turn, Stop stays
  hidden for the rest of the turn.
- **F8.** While a configuration runs, the Claude agent's `Interrupt` writes
  nothing and returns success. A turn Claude starts itself before
  acknowledging the configuration cannot be stopped.

When done, the composer has one button: Send, or Stop while Claude works and
the composer is empty. Pressing Stop turns it back into Send at once, and Stop
does not return until the turn it stopped has ended, with no flicker. An
interrupt during a configuration stops a turn Claude reports running.

## Related code

- `cmd/ox/web/src/SessionView.tsx:134` — the Stop and Send buttons.
- `cmd/ox/web/src/style.css:79` — `.send` (Lucide `send`) and `.stop` (Lucide
  `square`); `.composer .box button.icon` at line 327.
- `cmd/ox/web/src/ox.tsx:230` — `stopping`, set by `interrupt`, cleared on
  refusal or by `stopped`, which `setState` calls at line 607.
- `internal/agent/agent.go:41` — `Interrupt() error` in the harness interface.
- `internal/claude/prompt.go:86` — Claude's `Interrupt`; `*configuring` writes
  nothing. `sessionState` and `commandStarted` in the same file.
- `internal/claude/agent.go:408` — `end`, which settles a configuration's
  `done` when the agent ends.
- `internal/ur/ur.go:531`, `internal/fake/fake.go:301` — Ur and the fake end
  the turn inside `Interrupt`.
- `internal/server/methods.go:656` — `interruptSession`; `configureSession`
  below it already replies from a harness's `done`.
- `internal/server/conn.go:25` — one queue per connection carries replies and
  session updates in the order the session goroutine queues them.

## Decisions

- **Construction.** The page's `stopping` is its own fact: its
  `session/interrupt` request in flight. It starts with the request and ends
  when the request settles, by reply, refusal, or a lost connection. Nothing
  else sets or clears it, so no missed state transition leaves it set.
  `stopped` and its call in `setState` go.
- **Ownership.** `oxd` answers `session/interrupt` once the turn it stopped has
  ended, so the request in flight covers exactly the stop. The harness owns
  that wait: `Interrupt(done func(error))`, like `Configure`. Ur and the fake
  end the turn inside `Interrupt` and call `done(nil)` before returning. The
  Claude agent keeps `stops []func(error)`, the replies for interrupts it has
  written and Claude has not finished. It is needed so Stop stays hidden until
  the turn ends; it is non-empty only between a written interrupt and the
  event that settles it. Settled, all at once with `nil`, when Claude reports
  `idle`, when Claude takes an input (the stopped turn has ended and a new one
  starts, so Stop correctly shows for it), or when the agent ends (`end`,
  for exit, `Stop`, or a failed start). Settling runs after the update that
  event publishes, so the connection's queue carries the `idle` state before
  the reply. A second interrupt while one is in flight writes again and joins
  `stops`. When nothing needs writing, `done(nil)` runs at once; a failed
  write calls `done` with the error.
- **Requirements.** **Stop** (changed below). Through the request's lifecycle:
  in flight, the button is Send, unavailable while the composer is empty;
  refused, Stop returns with the error; reply lost to a disconnect, the request
  fails and the button follows the state the reconnect reads; a second page
  never pressed Stop and shows Stop while Claude works; the process exiting or
  parking settles the reply with success, and the published state hides Stop.
  **One prompt at a time** holds: typing while Claude works turns the button
  into Send, unavailable while an input is pending. **Lost connection** holds:
  the pending interrupt fails with the other pending requests.
- **Settling.** The new wait ends at Claude's `idle`, a taken input, or the
  agent's end. Each is in every recorded interrupt sequence below; a process
  that never answers ends with its exit or `Stop`.
- **Claude Code.** `docs/harnesses/claude-code.md`, "Interrupt": every recorded
  variant ends with `session_state_changed idle` — a running Bash tool, a
  received untaken prompt with and without `cancel_queued`, and a prompt
  written during a turn. Ox always sends `cancel_queued` when an input is
  written and untaken, so a prompt Claude takes only to stop it does not
  arise. An input sent after the interrupt can be taken before Claude reports
  idle; taking it settles the stop. An interrupt written during a
  configuration carries a fresh `request_id`, so its `control_response` does
  not match the configuration's, which `controlResponse` ignores; the turn
  ends with `result` and `idle` as in any phase. The live check below confirms
  the reply follows `idle` against real Claude.
- **UI.** One button at the right of the field, in the ChatGPT and Claude
  Desktop form: a filled circle in the primary color, Lucide `arrow-up` for
  Send and a filled Lucide `square` for Stop, both from `lucide-static`. It is
  a submit button labelled Send, unless Claude works (`running` or
  `requires_action`), the composer has no text or images, and no Stop is in
  flight; then it is a button labelled Stop that sends the interrupt. Send
  disabled shows the circle muted.

## Check your assumptions

1. **The Send button becomes Stop only while the composer is empty.** Kept,
   with the Stop requirement change below.
   - **Plan:** While Claude works, the button is Stop until the user types or
     attaches something, then Send, which queues the prompt for Claude to take
     into the turn, as Claude Desktop does. Clearing the text brings Stop back.
   - **Alternative:** Stop whenever Claude works, as ChatGPT does. Sending
     during a turn would then need Enter, and a phone, where Return makes a new
     line, could not send during a turn at all.

## Test plan

- Claude harness, `TestPromptReleasedAfterClaudeTakesIt`
  (`internal/claude/prompt_test.go`): its interrupt's `done` is not called
  before Claude reports idle and is called after the `idle` update is
  recorded.
- Claude harness, `TestPromptSentWhileClaudeWorks` or the nearest test that
  interrupts a running turn: an input sent after the interrupt that Claude
  takes before reporting idle settles `done` once it is taken.
- Claude harness, `TestUnexpectedExitIsReported`: an interrupt pending when
  Claude exits is settled with `nil`.
- Claude harness, `TestConfigurationKeepsReportedState`
  (`internal/claude/configure_test.go`): after `state(t, a, "running")` and
  before the acknowledgement, `Interrupt` writes `"subtype":"interrupt"`; its
  `done` waits for idle; the configuration still succeeds. Fails on the
  current code, which writes nothing.
- Claude harness, the other `Interrupt` callers in `prompt_test.go` and
  `configure_test.go`: pass a `done` and assert it settles where the case
  settles (at once when nothing is written).
- Ur, `internal/ur/ur_test.go`: its `Interrupt` calls pass a `done` and assert
  it ran before `Interrupt` returned.
- Live, the `interrupt` section of `TestLive`
  (`internal/server/live_test.go`): the `session/interrupt` reply arrives
  after the session's `idle` or `background` state update.
- e2e, `cmd/ox/web/e2e/session.spec.ts`, replacing the two Stop specs at line
  647 and 668:
  - "Send becomes Stop once the page's send is accepted, and stops it": while
    the send is held, Send shows disabled and no Stop; once accepted, Stop
    shows; pressed with the interrupt held, Send shows disabled and Stop does
    not; released, the transcript shows `Interrupted` and Send stays.
  - "a refused Stop shows Stop again with the error": unchanged in substance.
  - New, "typing while Claude works turns Stop into Send": typing shows Send
    and no Stop; clearing the text shows Stop.
  - New, "Stop returns once oxd answers the Stop while the session still
    runs": an idle session and a second session running `wait 60000`; hold
    the second's `session/interrupt` and release it with the idle session's
    `session_id`, so `oxd` answers success while the second keeps running.
    Stop is absent while held and shows after the reply. Fails on the current
    code.
- e2e, every other spec using `stop(page)` keeps working through the new
  button; `send(page)` resolves the same button by its Send label.

## Implementation plan

1. `internal/agent/agent.go`: `Interrupt(done func(error))`, documented as
   calling `done` once the stopped turn has ended, or at once when there is
   nothing to stop.
2. `internal/claude/prompt.go`: `Interrupt(done)` — move `*configuring` into
   the writing case with `*starting`, `available`, and `*prompting`; when
   nothing is written call `done(nil)`; on a failed write call `done` with the
   error; otherwise append to `a.stops`. Add `settleStops()`, which takes and
   clears `a.stops` and calls each with `nil`. Call it at the end of
   `sessionState` when Claude reports idle, after `commandStarted` takes an
   input, and in `end` after the final update. Add the `stops` field to
   `Agent` in `internal/claude/agent.go` with a comment. Update `Interrupt`'s
   doc comment.
3. `internal/ur/ur.go`, `internal/fake/fake.go`: take `done`, call
   `done(nil)` after ending the turn or finding nothing to stop.
4. `internal/server/methods.go`: `interruptSession` passes a `done` that
   replies success or `failure(id, err)`, as `configureSession` does; a session
   with no agent replies success at once.
5. `cmd/ox/web/src/ox.tsx`: `stopping` clears in a `finally` on the interrupt
   request, keeping its error for `start`; remove `stopped`, its call and its
   sentence in `setState`'s comment; rewrite the `stopping` comment.
6. `cmd/ox/web/src/SessionView.tsx`: replace the two buttons with one, as
   under UI. `cmd/ox/web/src/style.css`: `.send` uses
   `lucide-static/icons/arrow-up.svg`; style the composer's send/stop button
   as a filled primary-color circle with the icon in the primary foreground;
   drop the `.send::before` nudge if the arrow no longer needs it.
7. Tests as in the test plan.
8. Documentation and requirement below.

## Requirement changes

- **Changed: Stop.**
  - Old: "**Stop.** While Claude works, a Stop button shows. Stop ends the
    turn, the transcript shows it was interrupted, and the session takes
    prompts again; a pending prompt is dropped, and its text returns to the
    composer of the page that sent it, ahead of anything typed since. Messages
    from other sessions that Claude has not taken are dropped, and their
    senders are not told."
  - New: "**Stop.** While Claude works and the composer is empty, the Send
    button becomes Stop. Once pressed, Stop does not return until the turn it
    stopped has ended. Stop ends the turn, the transcript shows it was
    interrupted, and the session takes prompts again; a pending prompt is
    dropped, and its text returns to the composer of the page that sent it,
    ahead of anything typed since. Messages from other sessions that Claude has
    not taken are dropped, and their senders are not told."

## Documentation updates

- `docs/protocol.md`, `session/interrupt`: success arrives once the stopped
  turn has ended, after the session's `idle` or `background` state, or at
  once when there is nothing to stop; drop the clause about a configuration
  that runs.
- `docs/architecture.md`, "Interrupts" under the Claude agent: `configuring`
  joins the phases that write the interrupt; the reply waits for Claude's
  idle, a taken input, or the agent's end. Also the harness interface's
  `Interrupt` if the document describes its signature.
