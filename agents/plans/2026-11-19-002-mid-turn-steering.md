# Mid-turn steering

## Goal

While Claude works, the user can send a prompt, and it joins the running
turn. A session holds at most one prompt Claude has not taken yet. Until
Claude takes it, the prompt shows as a pending bubble at the end of the
transcript and Send is unavailable. Once Claude takes it, it becomes a normal
prompt and the user can send another. Stop drops a pending prompt and puts
its text back in the composer of the page that sent it. Phase 2 of
`agents/scratch/agent-tools.md` (messages between sessions) will deliver
through the same path. This plan covers steering only.

## Related code

- `internal/agent/agent.go`: `Agent.Send` and `Interrupt` contracts, `Snapshot`, the update type constants.
- `internal/claude/agent.go`: `phase` (`available`, `queued`, `prompting`, `configuring`, `exited`), `idle`, `exit`, `handleOutput` (`command_lifecycle`, `result`, which clears `turn`).
- `internal/claude/prompt.go`: `submission`, `Send` (refuses unless `idle()`), `Interrupt` (plain interrupt), `commandStarted`, `sessionState` (ignores `idle` while `queued`), `endPrompt`, `publish`.
- `internal/claude/park.go`: `Parkable` is built on `idle()`, which stays false while a prompt is pending.
- `internal/fake/fake.go`: the echo harness that server and e2e tests use. Its `publish` overwrites `MessageID` unconditionally.
- `internal/server/methods.go`: `sendSession` and `getSession`. `getSession` embeds `agent.Snapshot`, so a new snapshot field reaches the page without server changes. `history.go` records only `prompt`, `tool_use`, `tool_result`, `result`, `exited`, `parked`. The server needs no code change.
- `cmd/ox/web/src/ox.tsx`: `Session`, `apply`, `load`, `receive`, `createSessionControl` (`idle`, `available`, `post`).
- `cmd/ox/web/src/SessionView.tsx`: Send/Stop button, `Transcript`, `SettingsForm` (uses `available`).
- `cmd/ox/web/src/protocol.ts`: `Update`, `Methods["session/get"]`.
- Tests: `internal/claude/prompt_test.go` (`TestPromptReleasedAfterClaudeTakesIt`, `TestTurnAfterPromptResultIsClaudes`), `configure_test.go` (`TestConfigureRequiresIdle`), `internal/server/methods_test.go` (`TestSessionLifecycle` asserts a second send is busy), `live_test.go` (`TestLive`), `cmd/ox/web/e2e/session.spec.ts` (Stop tests, `ask`).

## Decisions

### Shape

- **A prompt enters the transcript when Claude takes it.** The Claude harness publishes `prompt` from `commandStarted` only, so history records a prompt only once it is in Claude's conversation, at the point Claude took it. That point is accurate, since a steered prompt is taken after the running tool call's result. Until then the prompt exists only as the agent's queued prompt. It shows live through a `prompt_queued` update and after a reload through `Snapshot.Queued`, the way pending approvals use `approval` and `Snapshot.Approvals`. Neither `prompt_queued` nor `prompt_cancelled` is recorded. A dropped prompt therefore leaves nothing in the history to undo.
- **New protocol values.** `agent.Prompt{MessageID string "message_id"; Text string "text"}`. `Snapshot.Queued *Prompt` (`"queued,omitempty"`). Update types `UpdatePromptQueued = "prompt_queued"` (`message_id`, `text`: written to the harness, not taken yet) and `UpdatePromptCancelled = "prompt_cancelled"` (`message_id`, `text`: dropped before Claude took it, by Stop or by the process exiting). `prompt` now means "Claude took this client prompt". `session/send` keeps its result.
- **Claude agent phases.** Drop the `queued` phase. `prompting` now means "client prompts hold the agent until Claude reports idle with none queued", and `a.prompt *submission` exists exactly in that phase:

  ```go
  // submission holds the agent in the prompting phase, from the first client
  // prompt written until Claude reports idle with none queued.
  type submission struct {
      queued *agent.Prompt // written; Claude has not taken it yet
      last   string        // the latest client prompt Claude took or dropped, which the releasing update names
  }
  ```

  - `Send`: `ErrBusy` in `configuring` or `exited`, or when `a.prompt != nil && a.prompt.queued != nil`. Otherwise write the prompt (`priority: "next"`, as today). Enter `prompting` with a new `submission` if `available`, set `queued`, and publish `prompt_queued`. If the state was `idle`, set it to `running` and publish that, as today. Sending while Claude runs or waits on an approval, in a client prompt's turn or in one Claude started itself, writes the prompt without a state change.
  - `commandStarted(id)` for `queued.MessageID`: publish `prompt` (the prompt's ID and text), set `last = id`, `turn = id`, `queued = nil`.
  - `commandCancelled(id)` (new, `command_lifecycle` `cancelled`) for `queued.MessageID`: publish `prompt_cancelled`, set `last = id`, `queued = nil`. A `cancelled` line for any other ID is ignored. Claude also reports the taken prompt it interrupted as `cancelled`.
  - `sessionState("idle")`: in `prompting` with `queued != nil`, keep waiting (today's `queued`-phase rule, which covers Claude's stale idle and a prompt written as a turn ends). In `prompting` with nothing queued, release through `endPrompt`, whose update names `last`.
  - `exit`: in `prompting`, publish `prompt_cancelled` for a queued prompt first, then `exited` naming `last`.
  - `Interrupt`: when the state is not idle, write `{"subtype":"interrupt"}`, adding `"cancel_queued":true` only while a prompt is queued. When nothing is queued, Stop stays exactly as it is today and Claude's own queued work is untouched.
  - `turn` still clears on `result`. A steered prompt taken mid-turn tags the rest of that turn with its own ID.
- **Fake harness** mirrors this. `Send` while idle publishes `prompt` at once, as today. `Send` while running or waiting on an approval with nothing queued stores `queued` and publishes `prompt_queued`. At its next tick (never during an approval wait, as Claude does), it takes the queued prompt: it publishes `prompt`, makes it the current prompt, and streams "You said: <text>" in place of the rest of the earlier reply. `Interrupt` and `Stop` publish `prompt_cancelled` for a queued prompt before finishing. `publish` sets `MessageID` only when the update names none. `Snapshot` includes `Queued`.
- **Page.**
  - `Session.queued?: { message_id: string; text: string }`, set from `session/get`'s `queued` in `load` (including the in-place reload) and from `prompt_queued`. It is cleared by `prompt_cancelled` and by a `prompt` with the same `message_id`.
  - `createSessionControl` splits `available` into `canSend` and `canConfigure`. Both need `ready() && !creating() && !sending() && requested() === undefined`. `canSend` also needs a loaded session with no `queued`, in any state. `canConfigure` also needs `idle()`.
  - The control remembers the `message_id` returned by its latest successful `session/send`. Its new `restore(messageId, text)` puts the text back when the ID matches: `text` alone, or `text + "\n" + text()` when the composer holds text typed since. `receive` calls `controls.get(session_id)?.restore(...)` for each live `prompt_cancelled`.
  - The send reply always reaches the page before that prompt's `prompt_cancelled` on the same connection, because the session goroutine queues the reply at `Send` and the cancellation from a later event. If the reply is lost, the send failed from the page's view, so the text is still in the composer.
  - `SessionView` shows Send alone when not running. While running, it shows Stop and then Send (Lucide `square`, `send`). Send is disabled unless `canSend()`. `SettingsForm` uses `canConfigure()`.
  - `Transcript` takes the session and renders `session.queued` after the entries as `<div class="prompt queued">`, styled as a dimmed prompt bubble. `.pending` is taken by the approvals section.

### Concerns

- **Requirements.**
  - **One prompt at a time** and **Stop** change (below).
  - **One transcript** and **Every page in sync**: the pending bubble comes from `prompt_queued` on every page and from `queued` after a reload. The taken prompt is recorded once, as `prompt`.
  - **Only the user sends**: unchanged. A steered prompt is sent only by the user's submit. A dropped prompt is restored to the composer, never resent.
  - **Composer after a send**: unchanged. **Unsent text**: restored text is composer text from then on.
  - **When settings can change**: settings stay idle-only. A queued prompt keeps the state `running`.
  - **Parking**: a queued prompt keeps the agent in `prompting`, so it does not park.
  - **Lost connection**: `ready()` still holds Send and Stop's companion Send. A prompt dropped while the page is disconnected is not restored on that page, because its update was missed. This is rare and costs only the retyping.
  - **Every request settles**: `session/send` and `session/interrupt` still reply at once. A send refused because a prompt is queued shows `session is busy`. That happens only when another page queued first, because this page holds Send while `queued` is set.
  - Request lifecycle:
    - A send in flight holds Send (`sending`). `prompt_queued` arrives before its reply, so Send stays held with no flicker.
    - Two pages sending at once: the second gets `-32002`.
    - Navigation away and back keeps the control's remembered ID.
    - A process exit drops the queued prompt with `prompt_cancelled`, and the sender page restores its text, the same as Stop.
- **Settling.** No new waits. Claude ends a queued prompt with `command_lifecycle` `started` or `cancelled`, or by exiting. A queued prompt keeps the state `running`, so Stop is always offered, and Stop drops it.
- **Construction.**
  - At most one queued prompt: `submission.queued` is one pointer, and `Send` refuses while it is set.
  - A queued prompt exists only in `prompting`: it is a field of `submission`, which exists only in that phase.
  - A prompt is in the history only once taken: `prompt` is published only from `commandStarted`.
  - The page stores no copy of "can send": `canSend` derives from `session.queued` and the request flags.
  - The page's single `queued` mirrors the agent's single pointer.
- **Ownership.**
  - New state: `submission.queued`, owned by the agent on the session goroutine. It is needed for the pending bubble, for refusing a second untaken prompt, and for Stop's `cancel_queued`. Its only race is Claude taking the prompt as an interrupt arrives. Claude settles it, and Ox follows `started` or `cancelled`. It is cleaned up by take, cancel, or exit.
  - `submission.last`: names the releasing update. It is cleaned up with the submission.
  - The page control's latest sent `message_id`: needed to return dropped text to the sending page. A stale ID matches nothing. It is replaced by the next send.
  - The commit body repeats this.
- **Claude Code.** Probed live on Haiku with 2.1.328 (record in `docs/claude-code.md`):
  - A prompt written during a turn gets `command_lifecycle` `queued` at once. Claude takes it (`started`) right after the running tool call's `tool_result`, with no new `system init`, and the turn ends with one `result`.
  - While a `can_use_tool` request waits, a queued prompt stays untaken. It is taken after the answer's `tool_result`.
  - A turn with no further tool calls does not take it. After the turn's `result`, Claude starts the prompt as its own turn (`started`, new `system init`, its own `result`) with no `idle` in between. `idle` follows only the last turn.
  - Lifecycle also reports `completed` for each prompt, the steered one before the original, around the `result`.
  - `interrupt` with `cancel_queued: true` mid-turn: `command_lifecycle cancelled` for the queued prompt, then the response `{"still_queued":[],"cancelled":["<uuid>"]}`, then the usual interrupt sequence (`tool_result` error, `[Request interrupted by user for tool use]`, `result` `aborted_tools`). After that comes `command_lifecycle cancelled` for the taken prompt, then `idle`.
  - Already recorded under [When a prompt is taken](../../docs/claude-code.md#when-a-prompt-is-taken) and [Interrupt](../../docs/claude-code.md#interrupt): `idle` can arrive before a prompt written to an idle process is taken, and `cancel_queued` on such a prompt gives `cancelled` and `idle` with no `result`.
  - The design depends only on `started`, `cancelled`, and `idle`, whatever turn is running, including one Claude started itself.

## Check your assumptions

1. **Sending while Claude works (requirement One prompt at a time).**
   - Old: "A session takes one prompt at a time: Send is available only when the session can take one, and prompts are not queued."
   - New: "While Claude works, the user can send a prompt, which Claude takes into the running turn after its current step, or runs next if the turn ends first. Until Claude takes it, the prompt shows as pending at the end of the transcript and Send is unavailable; once taken, it becomes a normal prompt where Claude took it."
   - **Plan:** Claude takes a prompt sent mid-turn when its current tool call finishes, or once an open approval is answered. If it makes no further tool calls, the prompt runs right after the reply as its own turn.
   - **Alternative:** none open. The user decided the one-pending-prompt rule and the pending bubble.
2. **Stop beside Send, and where dropped text goes (requirement Stop).**
   - Old: "While Claude works, Send becomes Stop. Stop ends the turn, the transcript shows it was interrupted, and the session takes prompts again."
   - New: "While Claude works, Stop shows beside Send. Stop ends the turn, the transcript shows it was interrupted, and the session takes prompts again; a pending prompt is dropped, and its text returns to the composer of the page that sent it, ahead of anything typed since."
   - **Plan:** Stop and Send both show while Claude works, so Stop is always one click away. A dropped prompt's text goes back to the page that typed it, even if Stop was pressed on another page.
   - **Alternative:** one button that is Stop while the composer is empty and becomes Send once the user types. This takes less space, but stopping then needs an empty composer or a separate key. Recommendation: keep the plan.

## Test plan

- **Claude harness** (`internal/claude/prompt_test.go`):
  - `TestPromptReleasedAfterClaudeTakesIt` changes. `Send` publishes `prompt_queued` and `running`, and no `prompt`. `prompt` with the text and ID is published at `started`. A second `Send` while queued is `ErrBusy`.
  - New `TestPromptSentWhileClaudeWorks`, starting from `promptingAgent`:
    - `Send` succeeds, writes `priority: "next"`, publishes only `prompt_queued`, and `Snapshot().Queued` holds it. A further `Send` is `ErrBusy`.
    - `requires_action`, then a stale `idle`, do not release.
    - `started` publishes `prompt` and clears `Queued`, and the next `Send` succeeds.
    - After `result` and `idle` with nothing queued, the release names the latest taken prompt.
    - A second case sends during a turn Claude started itself (no client prompt; `running` reported) and gets no state update.
  - New `TestStopDropsQueuedPrompt`:
    - `Interrupt` with a prompt queued writes `cancel_queued: true`. `cancelled` for the taken prompt changes nothing. `cancelled` for the queued one publishes `prompt_cancelled` with its text. `idle` releases.
    - `Interrupt` with nothing queued writes no `cancel_queued`.
    - A process exit with a prompt queued publishes `prompt_cancelled` before `exited`.
  - `TestTurnAfterPromptResultIsClaudes`: its "not released before idle" check becomes `Configure` returning `ErrBusy`, since `Send` is now allowed there.
  - `configure_test.go` `TestConfigureRequiresIdle`: the case names `queued` and `prompting` become "prompt not taken" and "prompt taken".
- **Server** (`internal/server/methods_test.go`):
  - `TestSessionLifecycle`: drop the "second prompt is busy" assertion, which the new test replaces.
  - New `TestPromptQueuedWhileClaudeWorks` with the fake harness. No existing test sets up a queued prompt.
    - Send `approve Bash {}`, so the turn waits on an approval. A second send succeeds with `prompt_queued`, and a third returns `-32002`. `session/get` returns `queued`.
    - `session/interrupt` gives `prompt_cancelled` with the text, then `idle`, and the history holds no `prompt` for it.
    - Repeat with Allow instead: `prompt` for the queued ID, then its reply.
- **e2e** (`session.spec.ts`, fake harness):
  - New "a prompt sent while Claude works stays pending until Claude takes it". `ask` gives an approval card. Stop and Send both show. Sending "steer" clears the composer, shows `.prompt.queued` with "steer", and disables Send. After a reload the bubble is still there. Allow removes it, and "steer" shows as a normal `.prompt` followed by "You said: steer".
  - New "Stop drops a pending prompt and returns its text to the composer". `ask`, send "steer", type "more", then Stop. The bubble is gone, the composer holds "steer\nmore", and "Interrupted" shows.
  - Existing Stop tests: keep their assertions, which still hold with Send beside Stop.
- **Live** (`TestLive`, Haiku, manual mode):
  - "a prompt sent while Claude works joins the turn". Ask Haiku to run `sleep 3 && echo ok` with Bash. On its approval, send a second prompt: `prompt_queued`, and a third send returns `-32002`. Then allow. Expect `prompt` for the second ID before `idle`.
  - "Stop drops a prompt Claude has not taken". Same start. On the approval, send a second prompt, then `session/interrupt`. Expect `prompt_cancelled` with its text, `idle`, and no `prompt` for it.

## Implementation plan

1. `internal/agent/agent.go`: add `Prompt`, `Snapshot.Queued`, `UpdatePromptQueued`, and `UpdatePromptCancelled`. Reword `Send`: it writes a prompt unless one is queued or the agent is configuring or exited. Reword `Interrupt`: it drops a queued prompt with `prompt_cancelled`.
2. `internal/claude/agent.go`, `prompt.go`: the phase and `submission` changes, `commandCancelled` wired from `command_lifecycle`, `Snapshot` with `Queued`, `exit`, and `Interrupt` as decided. Then update the claude tests.
3. `internal/fake/fake.go`: queued prompt, take at the next tick, cancel on interrupt and stop, `publish` keeping a named ID, `Snapshot`.
4. `internal/server/methods_test.go`, `live_test.go`: tests as planned.
5. `cmd/ox/web/src/protocol.ts`: `message_id` on `prompt`, the two new update types, `queued` on `session/get`.
6. `cmd/ox/web/src/ox.tsx`: `Session.queued`, `apply`, `load`, `receive` restore, control `canSend`, `canConfigure`, remembered ID, and `restore`.
7. `cmd/ox/web/src/SessionView.tsx`, `style.css`: Stop beside Send, the pending bubble (`.log .prompt.queued`, dimmed), `SettingsForm` on `canConfigure`.
8. `cmd/ox/web/e2e/session.spec.ts`: specs as planned.
9. Docs as listed below.

## Requirement changes

- **Changed: One prompt at a time.**
  - Old: "A session takes one prompt at a time: Send is available only when the session can take one, and prompts are not queued."
  - New: "**One prompt at a time.** While Claude works, the user can send a prompt, which Claude takes into the running turn after its current step, or runs next if the turn ends first. Until Claude takes it, the prompt shows as pending at the end of the transcript and Send is unavailable; once taken, it becomes a normal prompt where Claude took it."
- **Changed: Stop.**
  - Old: "While Claude works, Send becomes Stop. Stop ends the turn, the transcript shows it was interrupted, and the session takes prompts again."
  - New: "**Stop.** While Claude works, Stop shows beside Send. Stop ends the turn, the transcript shows it was interrupted, and the session takes prompts again; a pending prompt is dropped, and its text returns to the composer of the page that sent it, ahead of anything typed since."

## Documentation updates

- `docs/protocol.md`:
  - `session/send`: sends to an idle or working session, and returns `-32002` while a prompt is queued or a configuration runs.
  - `session/get`: the `queued` field. The update table: `prompt` means Claude took the prompt; add `prompt_queued` and `prompt_cancelled`.
  - The `message_id` rule: a prompt taken mid-turn tags the rest of the turn.
  - `session/interrupt`: drops a queued prompt.
  - The recorded update list is unchanged.
- `docs/architecture.md`: the phase table without `queued`, and `prompting` as described. The paragraph on `available` and `idle`: Send needs no queued prompt and no configuration, and configuration needs `available` and `idle`. The control remembers its latest sent prompt so dropped text returns to its composer.
- `docs/claude-code.md`:
  - The `priority: "next"` bullet in [User messages](../../docs/claude-code.md#user-messages): when a mid-turn prompt is taken, during `can_use_tool`, and after a text-only turn.
  - [`command_lifecycle`](../../docs/claude-code.md#command_lifecycle): `completed`.
  - [Interrupt](../../docs/claude-code.md#interrupt): the mid-turn `cancel_queued` sequence.
- `docs/functional-requirements.md`: the approved text above.
