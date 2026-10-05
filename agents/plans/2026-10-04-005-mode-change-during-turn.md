# Apply a mode change during a turn

## Goal

A prompt copies the session's mode when it starts, so changing between Ask and
Auto while a turn runs has no effect until the next turn. After this change, the
turn reads the session's current mode before it runs each batch of tool calls,
so a mode change during a turn applies from the next batch.

The terminal client already sends mode changes while a turn runs (Tab calls
`cycle`), so only the server and the agent change.

## Related code

- `internal/agent/agent.go` — `Input.Mode`, `Turn.mode`, `Start` (records the
  mode in the turn start), `execute` (runs one batch), and `approve` (skips
  permission in Auto mode). `Client` is the interface the turn uses to reach the
  ACP client.
- `internal/server/server.go` — `activeSession.selections` holds the latest
  selections under `Server.mu`; `setConfigOption` updates them; `prompt` copies
  them into `agent.Input`; `acpClient` implements `agent.Client`.
- `cmd/ox-server/main.go` — `runHeadless` passes `transcript.ModeAuto`;
  `headless` implements `agent.Client`.
- `internal/transcript/transcript.go` — `TurnStart.Mode`.
- `internal/agent/agent_test.go` — `recorder` implements `Client`;
  `fixture.input` takes a mode.
- `internal/server/server_test.go` — `client.await` answers permission requests
  during a prompt.

## Decisions

- The `Client` interface gets `Mode() transcript.Mode`, which returns the
  session's current mode. It replaces `Input.Mode`, so the turn has one source
  for the mode. `Start` records `client.Mode()` in the turn start, and `execute`
  calls it once at the start of each batch.
- The mode is read once per batch, not once per call. Every call in a batch uses
  the same mode. A permission request that is open when the mode changes still
  waits for its answer.
- Model and effort still apply from the next turn. They are fixed in
  `openrouter.Request` for the whole turn.
- `acpClient` keeps the `*activeSession` from `prompt` and reads
  `session.selections.Mode` under `Server.mu`. A session cannot be closed or
  deleted while its prompt runs, so the pointer stays valid.
- `TurnStart.Mode` keeps its meaning: the mode when the turn started.

## Implementation plan

1. In `internal/agent/agent.go`:
   - Add `Mode() transcript.Mode` to `Client`, with a doc comment saying it
     returns the session's current mode, read before each batch. Update the
     `Client` type comment.
   - Remove `Input.Mode` and update the `Input` comment so it names only Model
     and Effort.
   - Remove `Turn.mode`. In `Start`, set the turn start's mode from
     `client.Mode()`; update the `Start` comment.
   - In `execute`, read `mode := t.client.Mode()` before the call loop and pass
     it to `approve(ctx, call, mode)`.
2. In `internal/server/server.go`:
   - Add a `session *activeSession` field to `acpClient` and set it in `prompt`.
     Drop `Mode` from the `agent.Input` there.
   - Add `func (c *acpClient) Mode() transcript.Mode` that locks `c.server.mu`
     and returns `c.session.selections.Mode`.
   - Change the `activeSession.selections` comment to: a prompt copies the model
     and effort when it starts, so changing them during the prompt applies to
     the next turn; the turn reads the mode before each batch of tool calls.
3. In `cmd/ox-server/main.go`, drop `Mode` from the `agent.Input` in
   `runHeadless` and add `func (headless) Mode() transcript.Mode` returning
   `transcript.ModeAuto`. Keep the `headless` comment accurate.
4. In `internal/transcript/transcript.go`, change the `TurnStart` comment to say
   the mode is the session mode when the turn started.
5. In `internal/agent/agent_test.go`:
   - Give `recorder` a `mode transcript.Mode` field guarded by `mu` and a
     `Mode()` method. Add a `modeAfterCall transcript.Mode` field: when set,
     `Send` switches `mode` to it on the first `ToolFinished`, the same way
     `cancelAfterCall` works.
   - Remove the mode parameter from `fixture.input`. Each test sets the mode on
     its `recorder` instead (`&recorder{mode: transcript.ModeAsk}`).
6. Add the tests below.

## Test plan

- `internal/agent/agent_test.go`, `TestAModeChangeAppliesFromTheNextBatch`:
  replies are `fake.Shell("printf one", "printf two")`,
  `fake.Shell("printf three")`, and `fake.Text("Done.")`. The recorder starts in
  Ask mode with answers `[true, true]` and `modeAfterCall: transcript.ModeAuto`.
  Both calls in the first batch ask for permission, the call in the second batch
  runs without asking, all three outcomes are `completed`, and the saved turn
  start has mode `ask`.
- `internal/server/server_test.go`,
  `TestAModeChangeDuringAPromptAppliesFromTheNextBatch`: a session in Ask mode
  is prompted with replies `fake.Shell("printf one")`,
  `fake.Shell("printf two")`, and `fake.Text("Done.")`. When the first
  permission request arrives, the test sets `mode` to `auto`, then approves the
  request. The prompt ends with `end_turn`, exactly one permission request was
  sent, and both tool calls completed.
- Existing agent and server tests pass with the mode set through the client.
