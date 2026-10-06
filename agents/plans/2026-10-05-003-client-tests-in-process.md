# Run the client tests in process

## Goal

The terminal and client tests build both binaries, start a real `ox-server`
process, give it a home directory, four environment variables, a settings file,
and an on-disk database, and rewrite the fixture catalog's release dates so the
server's clock-based filter keeps the expected models. The tests check key
handling, client session state, and the client's side of the ACP conversation,
none of which needs a process.

When the work is done:

- `go test ./...` builds no binaries and starts no `ox-server` process.
- `internal/tui` tests talk to a scripted ACP agent in the same process over
  `io.Pipe` and inject server events directly.
- `internal/client` tests talk to the real server in the same process over
  `io.Pipe`, the way `internal/server` tests do, plus one test that runs a child
  process to cover `Start`.
- `internal/servertest` and `openroutertest.CurrentCatalog` are gone.
- `make e2e` still runs the tmux tests against both built binaries, reduced to
  the four that check what only a terminal and two processes can show.

## Related code

- `internal/client/conn.go:92-169` — `Start` runs the server process, then
  `newConn` and `initialize` connect over any reader and writer. The exported
  constructor comes from here.
- `internal/client/client_test.go:240-265` — `fakeAgent` and `connectFake`
  already connect a `*Conn` to an in-process `acp.Handler` over `io.Pipe`. The
  new tests follow this pattern.
- `internal/server/server_test.go:45-86` — `connect` runs the real server in
  process with `store.OpenMemory`, `openroutertest.Start`, and
  `fake.ParsedCatalog()`. The client tests reuse this construction.
- `internal/tui/tui_test.go:22-158` — the `driver` runs the model's commands
  synchronously. Its `newDriver`, `next`, `collectRequest`, `awaitMessage`,
  `turn`, and `hang` are replaced.
- `internal/tui/tui.go:101-138` — `update` accepts `client.Event` values as
  messages, so tests can inject updates and finished events without a
  connection. `*client.Permission` holds the pending request, so permission
  requests must still arrive through a connection.
- `internal/tui/view_test.go:246-256` — `selectConfig` builds a select config
  option. The scripted agent's options are built with it.
- `internal/servertest/servertest.go` — builds the binaries, writes the settings
  file, and sets the environment. Deleted; `cmd/ox/e2e_test.go` keeps what the
  tmux tests need.
- `internal/openroutertest/openroutertest.go:29-69,259-283` — `Catalog` and
  `CurrentCatalog`. The filter at `internal/openrouter/catalog.go:75` drops only
  models older than the cutoff, so far-future release dates pass at any clock.
- `cmd/ox/main_test.go:15,39` — `TestMain` and the bundled-server exec test,
  which needs a built `ox` next to a built `ox-server` because `run` calls
  `syscall.Exec`.
- `cmd/ox-server/main_test.go:16-30,48` — the helper-process pattern: the test
  binary re-runs itself with `-test.run` and an environment variable. The
  `Start` test uses the same pattern.
- `internal/agent/agent_test.go:26-70,351,562` — the `recorder` sees every
  event; two tests cancel on a timer instead.
- `Makefile` — `e2e` runs `-run '^TestTerminal'`.

## Decisions

**Two seams, by what each package owns.** The terminal model owns key handling
and state, so its tests use a scripted agent that answers session requests from
fixed data. The client package owns the ACP conversation, so its tests keep the
real server, in process. Both go through `client.Connect`.

**`Connect` takes the output as an `io.WriteCloser`.**
`Connect(in io.Reader,
out io.WriteCloser, directory, version string)` runs
`newConn` and `initialize` with `stop` set to close `out`. `Start` calls
`Connect` and then replaces `stop` with the one that waits for the process and
kills it after two seconds. An initialization failure inside `Connect` closes
the server's input, which is how the server shuts down; the kill fallback covers
only a connected server.

**The scripted agent lives in `internal/tui/tui_test.go`.** Only the terminal
tests need it, and it is a test type of about eighty lines. No new package.

**Permission requests cross the pipe; everything else is injected.** A
`*client.Permission` carries the request it answers, so the scripted agent sends
`session/request_permission` while answering a prompt and the test reads it with
`conn.Next()`. Updates and `Finished` events are injected through `driver.send`.
`conn.Next()` is called directly, without a goroutine or timer; a hung test
fails at the `go test` deadline.

**The fixture catalog uses far-future release dates for kept models.** The four
kept models get `created` of 4102444800. The dropped `acme/old` keeps its date.
`ParsedCatalog()` still filters at `Now`, and a server filtering at the real
clock keeps the same four models, so `CurrentCatalog` is unnecessary. The
inclusive boundary that `acme/plain` encoded moves to an inline two-model
catalog in the OpenRouter catalog test.

**`make e2e` runs the whole `cmd/ox` package.** The `TestMain` in
`cmd/ox/e2e_test.go` builds the binaries only when `OX_E2E` is set, so the
Makefile drops the `-run` filter and the bundled-server exec test joins the e2e
file under the same skip.

**The `Start` test is the test binary itself.** With `OX_TEST_AGENT` set, the
test binary serves `fakeAgent` on standard input and output, writes one line to
standard error, and exits when it receives a prompt. This covers stderr
diagnostics, `Closed` after the server exits, and shutdown on input close,
without building `ox-server`.

## Test plan

- `internal/tui`: every existing test in `tui_test.go` keeps its assertions.
  `TestOpeningASessionHoldsServerEventsUntilItOpens` scripts the load the way
  the server behaves: a replayed update before the load response and the
  available commands update after it.
- `internal/client`: the eight real-server tests keep their assertions against
  the in-process server. `TestServerExitReleasesPendingWork` becomes
  `TestAServerExitFinishesThePromptAndClosesTheConnection`: start the helper
  process, prompt, and expect a `Finished` with an error followed by `Closed`. A
  second helper test, `TestDiagnosticsArriveFromStandardError`, expects
  `Diagnostic("agent started")`.
- `internal/openrouter`: the catalog test gains a case parsing two models dated
  `Now-183*24*60*60` and one second earlier, keeping the first and dropping the
  second.
- `cmd/ox`: `go test ./cmd/ox` without `OX_E2E` runs only the pure tests and
  builds nothing. `make e2e` runs the four tmux tests and the exec test.
- `internal/agent`: the two cancellation tests cancel on an event and pass
  without a timer.

## Implementation plan

1. `internal/client/conn.go`: add
   `Connect(in io.Reader, out io.WriteCloser,
   directory, version string) (*Conn, protocol.NewSessionResponse, error)`
   from the body of `newConn` plus `initialize`, with `stop` closing `out`. Make
   `Start` call it and then set the process-aware `stop`. Delete `newConn`.
2. `internal/openroutertest/openroutertest.go`: set `created` to 4102444800 for
   `acme/plain`, `z-ai/glm-5.3-flash`, `deepseek/deepseek-v4.1-flash`, and
   `meta/muse-spark-1.3-contributor`; delete `CurrentCatalog` and the `time`
   import; update the `Catalog` comment.
3. `internal/openrouter/openrouter_test.go`: add the boundary case to
   `TestCatalogKeepsRecentUsableModelsByNameWithKnownEfforts`.
4. `internal/tui/tui_test.go`: delete `TestMain`, `next`, `collectRequest`,
   `awaitMessage`, `turn`, and `hang`. Add the scripted agent:

   ```go
   // agent is a scripted ACP server. It answers session requests from its
   // fields and each prompt with the next reply.
   type agent struct {
       conn     *acp.Conn
       options  []protocol.SessionConfigOption
       sessions []protocol.SessionInfo
       replies  []reply
       cancels  chan struct{}
   }

   // reply answers one prompt. send delivers a session update and ask sends
   // a permission request and returns the chosen option ID, or "" when the
   // request was cancelled.
   type reply func(send func(protocol.SessionUpdate), ask func() string)
   ```

   `HandleRequest` answers `initialize` with the protocol version and the list,
   load, and close capabilities; `session/new` and `session/load` with
   `options`, after `session/load` sends the scripted replay and before the
   available commands update; `session/set_config_option` with `options` changed
   by `chosen`; `session/list` with `sessions`; `session/close` with an empty
   response; and `session/prompt` by running the next reply and responding with
   `end_turn`, or `cancelled` when the reply returned after a cancel.
   `HandleNotification` records `session/cancel` on `cancels`. Replies: `echo`
   sends "you said: " and the prompt's text; `ask(then
   reply)` sends one
   permission request with the Yes and No options from `view_test.go`'s
   `permission` helper and then runs `then`; `hang` waits for a cancel.

   `newDriver(t, options, replies...)` connects with `Connect` over two pipes,
   as `connectFake` does. Default options: model with the four fixture names and
   the metadata `configOptions` sends, effort with the default model's efforts,
   and mode with ask and auto. Rewrite each test to inject events with `d.send`
   and to read permission requests with `d.send(d.conn.Next())`.
5. `internal/client/client_test.go`: delete `TestMain`, `start`, `startServer`,
   `awaitCommands`, and `hang`. Add `start(t, replies...)` that builds the
   in-process server as `server_test.go` does and connects with `Connect`.
   Replace `TestServerExitReleasesPendingWork` with the two helper-process tests
   and a `TestAgentProcess` helper that serves `fakeAgent` when `OX_TEST_AGENT`
   is set.
6. `cmd/ox/e2e_test.go`: add `TestMain` that builds both binaries into a
   temporary directory when `OX_E2E` is set; add `binary(name)` and
   `environment(t, root, endpoint)` from `servertest`; answer the catalog
   request with `openroutertest.Status(200, openroutertest.Catalog)`; move
   `TestServerCommandsRunTheBundledServer` here under the skip; delete
   `TestTerminalModelPickerShowsProvidersAndPricesAndChangesTheModel`,
   `TestTerminalResumeDuringAPromptWaitsForTheTurnToEnd`,
   `TestTerminalTranscriptRendersThinkingToolsAndWrappedReplies`,
   `TestTerminalStatusLineShowsTheSessionSettingsAndUsage`,
   `TestTerminalTabAndShiftTabCycleModes`, `TestTerminalControlECyclesEffort`,
   and `TestTerminalTabCompletesASlashCommandFromGhostText`, with the helpers
   only they used.
7. `cmd/ox/main_test.go`: delete `TestMain` and the moved test.
8. Delete `internal/servertest`.
9. `Makefile`: `e2e` becomes `OX_E2E=1 go test -count=1 ./cmd/ox`.
10. `internal/agent/agent_test.go`: replace `cancelAfterCall` with
    `cancel
    context.CancelFunc` and `cancelOn func(Event) bool` on
    `recorder`; cancel on `TextDelta` in
    `TestCancellationDuringTheStreamDiscardsProvisionalOutput`, on
    `CompactionStarted` in `TestCancellationDuringCompactionSavesNothing`, and
    on `ToolFinished` where `cancelAfterCall` was used.

## Documentation updates

- `AGENTS.md` — `internal/servertest` is not listed, so nothing changes. Confirm
  the `make e2e` line still describes the target.
