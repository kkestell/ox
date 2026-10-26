# Watch workers

## Plan

`agents/plans/2026-10-26-002-watch-workers.md`

## Summary

The terminal client now splits the screen when the orchestrator has workers and
shows each worker's transcript in its own labeled pane. The server sends
`_ox/team` and tags worker updates with their session IDs while the orchestrator
is watched. Resuming replays each worker's saved transcript. The plan's goal is
met.

## Departures from the plan

- `internal/agent/agent.go` changed, which the plan did not list. A worker
  turn's user message comes from its inbox, and the turn never sent it, since an
  ACP client shows its own prompt. Without a change, worker panes would miss
  every message a worker receives while idle, including the task given by
  `create_agent`, until the next resume. When the inbox supplies the user
  message, the turn now sends it, joined with any start context, as the
  `Context` event it already sent for the context. Replay shows the same text.
  Test: `TestInboxUserMessageIsSentAsTheTurnStarts`.
- The e2e test approves `create_agent` in Ask mode instead of running in Auto
  mode, since the tmux harness writes only the model to the settings.
- At the user's request, the orchestrator gets a label line too,
  `━━ orchestrator ━━━…`, across the top of the left half. The first worker's
  corner is `┳`, where the two lines meet. All lines use the heavy box drawing
  characters.
- `loadSession` wakes workers after the watch instead of before it. A worker
  woken first could save its new turn start before the replay read it, and then
  send the same input live, so its pane showed the message twice.
- A failed worker turn sends the replay's "Turn error" update to its pane, since
  a worker view gets no prompt response. Without it, the pane showed no error
  and could keep counting a thinking placeholder. `turnError` in `convert.go`
  builds the update for both.
- The terminal client escapes worker names with `control.Escape`, as it does
  option names, and skips a pane that gets no rows.

## Decisions

- The task 4 terminal-client work was done by a Hypercube worker in this
  checkout, limited to `internal/tui`, while the server, client, agent, e2e
  test, and README were done here.
- A read-only Hypercube review worker reviewed the staged change. Its three
  findings were fixed: the duplicate input after resume, the missing turn error
  in worker panes, and an unchecked index in the inbox test.

## Checks run

- `make check` — passed.
- `make e2e` — passed, including
  `TestTerminalShowsAWorkerBesideTheOrchestrator`.
- `go test -count=300 ./internal/server/ -run 'TestLoadingWakesWorkersClosing|TestAFailedWorkerTurn|TestTheClientSees'`
  — passed.
- `go test -race -count=20 ./internal/server/ -run 'TestLoadingWakes|TestAFailedWorkerTurn|TestTheClientSees|TestTeams'`
  — passed.

## Manual verification

1. The split, the label line, and both replies in the terminal, using the tmux
   test.

   ```sh
   OX_E2E=1 go test -count=1 -run TestTerminalShowsAWorker -v ./cmd/ox
   ```

   Passed: the screen showed `━━ orchestrator ━` and `┳━ helper ━`, the worker's
   task `Help.`, the divider, and both replies.

## Follow-up work

- A worker woken by a message while its orchestrator is being reloaded can still
  show its turn's input twice, once in the replay and once live. The window is
  the moment between saving the turn start and sending it.
