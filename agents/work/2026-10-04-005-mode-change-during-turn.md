# Apply a mode change during a turn

## Plan

`agents/plans/2026-10-04-005-mode-change-during-turn.md`

## Summary

The turn now reads the session's mode from `Client.Mode()` when it starts and
before each batch of tool calls, so switching between Ask and Auto during a turn
applies from the next batch. The plan's goal is met.

## Decisions

- `acpClient.session` has a comment saying why the pointer stays valid during
  the prompt.
- In the server test, the client sends the mode change before the permission
  answer. The connection reads messages in order, so the change is saved before
  the second batch reads the mode.

## Checks run

- `make check` — Passed.
- `make e2e` — Not run; terminal behavior did not change.

## Manual verification

1. Both new tests fail when `execute` uses a fixed Ask mode instead of reading
   `t.client.Mode()`.

   ```sh
   sed -i '' 's/	mode := t.client.Mode()/	mode := transcript.ModeAsk/' internal/agent/agent.go
   go test -run ModeChange ./internal/agent ./internal/server
   git checkout internal/agent/agent.go
   ```

   `TestAModeChangeAppliesFromTheNextBatch` failed with a third permission
   request, and `TestAModeChangeDuringAPromptAppliesFromTheNextBatch` failed.
