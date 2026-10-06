# Work log: run the client tests in process

## Plan

`agents/plans/2026-10-05-003-client-tests-in-process.md`

## Summary

`go test ./...` no longer builds the Ox binaries or starts `ox-server`. The
terminal model tests talk to a scripted ACP agent over `io.Pipe`, the client
tests talk to the real server in process, and two helper-process tests cover
`Start`. `internal/servertest` and `openroutertest.CurrentCatalog` are gone, and
`make e2e` runs the four remaining tmux tests and the bundled-server test. The
plan's goal is met.

## Departures from the plan

- `reply` takes a `turn` value holding the prompt's text, `send`, `ask`, and a
  `cancelled` channel. The plan's `func(send, ask)` cannot give `echo` the
  prompt's text or let `hang` wait for a cancellation.
- `agent` has a `replay` field for the updates a load sends. The plan's fields
  had no source for the replay that
  `TestOpeningASessionHoldsServerEventsUntilItOpens` needs.
- `agent.cancels` is the running prompt's channel, closed on `session/cancel`,
  so the prompt can respond `cancelled` after the reply returns.
- `newDriver(t, replies...)` takes no options argument. Every test uses the
  default options, which `configOptions()` builds from the fixture catalog.
- The terminal tests read whole turns with a `finish` helper that sends each
  event to the model and returns the response text. Turns run through the
  scripted agent, so their `Finished` events come from the connection and are
  not injected.
- The client tests keep `hang`: the two tests that cancel a running turn need
  the model request to send a chunk and then stay open. `awaitCommands` is gone;
  the tests check that the next event is the available commands update.
- `Start` applies the two-second kill fallback after a failed `Connect` too, so
  a server that hangs during startup cannot block the client forever.
- The agent tests use a generic `is[T]` as `cancelOn`.

## Decisions

- The scripted agent sends the available commands update only after
  `session/load`. Updates after `session/new` would sit in front of the
  permission requests that tests read with `d.conn.Next()`.
- The scripted agent assigns `session-1`, `session-2`, and so on, and
  `session/list` returns every session it created.
- `TestAServerExitFinishesThePromptAndClosesTheConnection` accepts a `Closed`
  before the `Finished`, because the connection marks itself closed before it
  fails pending calls.
- `Start` starts reading standard error after `Connect` returns, so diagnostics
  written during startup arrive after the connection opens.

## Checks run

- `make check` — The Go checks passed. `dprint check` fails on the untracked
  `evals/reports/smoke-yaml.md`, which is not part of this change. The plan
  passes after `dprint fmt`.
- `make e2e` — Passed: four tmux tests and the bundled-server test.
- `go test -race -count=20 ./internal/tui ./internal/client ./internal/agent` —
  Passed.

## Manual verification

1. `go test` builds nothing without `OX_E2E`.

   ```sh
   grep -rn '"go", "build"' --include='*.go' .
   go test -count=1 -v ./cmd/ox | grep -E '^--- '
   ```

   The only build is in the `cmd/ox` `TestMain` and runs only when `OX_E2E` is
   set. Without it, `TestServerCommandsRunTheBundledServer` is skipped and the
   tmux tests skip in `startTmux`.
