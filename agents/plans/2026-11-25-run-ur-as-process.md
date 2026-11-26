# Run Ur as its own process

## Goal

Ur runs inside `oxd`: its turn loop runs on the session goroutine, and its
OpenRouter streams, titling requests, and shell process groups belong to `oxd`.
A panic or data race in Ur takes down `oxd` and every session.

When the change is done, Ur is its own `ur` binary, built beside `oxd` and
`ox-mcp`. `oxd` starts one `ur` process per running Ur session and drives it
over stdin and stdout, as it drives Claude Code. The `ur` process owns the
conversation file, the turn loop, OpenRouter requests, and shell commands.
Titling with Ur runs `ur -ask`. A `ur` that crashes ends only its own session,
which shows why in the transcript and parks; the next send resumes the
conversation in a new `ur`. Ur keeps its shell tool, approvals, settings, and
instructions as they are.

Resolves #132.

## Related code

- `internal/ur/ur.go` — today's Ur agent: `agent.Agent` methods, the turn loop
  (`turn`, `step`, `begin`, `request`, `next`, `execute`, `answer`, `finish`),
  conversation file (`load`, `save`, `conversation`), and `Harness`. The loop and
  file move to the `ur` process; the `agent.Agent` side becomes an adapter.
- `internal/ur/stream.go`, `internal/ur/shell.go`, `internal/ur/instructions.go`
  — OpenRouter requests and stream parsing, the shell tool, and the system
  message. They move to the `ur` process unchanged, except that `startShell`
  stops registering with `children`.
- `internal/claude/claude.go:96` `startProcess`, `:191` `logStderr`, `:236`
  `read`, `:265` `deliverOutput`, `:299` `Terminate` — Claude's process I/O:
  pipes Ox owns, stdout read to its last line, exit seen while a descendant holds
  a pipe, stderr logged with its tail kept, delivery that never blocks the
  reader. The `ur` adapter needs the same guarantees; they move to one shared
  package.
- `internal/claude/ask.go` — titling through a one-shot `claude -p`, the model
  for `ur -ask`.
- `internal/agent/tracker.go` — the untaken inputs and published state an
  adapter derives from its process's reports, as `internal/claude` does.
- `internal/server/session.go:445` `start`, `:487` `stop`, `:494` `parked`,
  `:797` `generateTitle` — how the session starts, stops, and reaps an agent,
  and asks the titling harness. Unchanged.
- `cmd/oxd/main.go:57` harness map, `:107` `findForwarder` — where `oxd` builds
  the Ur harness and finds `ox-mcp` beside itself.
- `cmd/oxd/scene.go` — screenshots builds replace `ur` with the scene player;
  unchanged.
- `internal/ur/ur_test.go` — drives Ur through `agent.Harness` and `agent.Agent`
  against an `httptest` OpenRouter; it keeps doing so, now through the adapter
  and a real `ur` process.
- `internal/ur/shell_test.go` — shell tool unit tests; move with the shell code.
- `internal/server/live_test.go:1206` `TestLiveUr`, `cmd/oxd/main_test.go:180`,
  `cmd/ox/web/e2e/global-setup.ts:14`, `Makefile` `build` — build and wiring
  sites for the new binary.

## Decisions

### Shape

- **Packages.**
  - `internal/ur/engine` is the `ur` program: `Main(args []string) int` parses
    its flags with its own `flag.FlagSet` and runs agent mode or ask mode. It
    holds today's turn loop, conversation file, `stream.go`, `shell.go`, and
    `instructions.go`.
  - `cmd/ur/main.go` is `os.Exit(engine.Main(os.Args[1:]))`.
  - `internal/ur/wire` holds the stdio message types both sides use, so `oxd`
    never links the engine.
  - `internal/ur` becomes `oxd`'s Ur harness: `Harness`, the adapter `Agent`,
    and `Ask`, which runs `ur -ask`. It keeps `checkEffort`,
    `checkPermissionMode`, and `TakesProviders`, since `oxd` checks the
    configuration and requests at its boundary. `ur` trusts the settings it is
    given.
  - `internal/process` is the shared harness-process I/O that `internal/claude`
    and `internal/ur` both use.
- **Command line.** Agent mode:
  `ur -dir <conversations> [-resume <token>] -model <id> [-effort <e>] -permission-mode <m> [-providers <json>] [-endpoint <url>]`,
  run with its working directory set to the session's directory, which is the
  workspace the engine uses. Ask mode:
  `ur -ask -model <id> [-effort <e>] -instructions <text> [-providers <json>] [-endpoint <url>]`
  reads the user's text on stdin and prints the reply on stdout. `-providers` is
  the configured map from model ID to provider order, as `Providers()` returns
  it, so one shape serves both modes and later model changes. `-endpoint`
  defaults to OpenRouter's (the `Endpoint` constant moves to the engine). `oxd`
  passes none, and tests pass their `httptest` URL.
- **Finding `ur`.** `oxd` finds `ur` beside itself as it finds `ox-mcp`.
  `findForwarder` becomes `findBeside(name)`. `oxd` refuses to start without
  `ur` and says so: `ur not found beside oxd: …`.

### The stdio protocol

One JSON object per line each way, in `internal/ur/wire`. `oxd` writes:

| `type`      | Fields                                                            | `ur` does                                                                                                                                                                                                                             |
| ----------- | ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `input`     | `message_id`, `content`, `images` (each `mime_type`, `data`)      | Queues the input, and starts a turn with it when none runs. A running turn takes it before its next request, as today.                                                                                                              |
| `approve`   | `request_id`, `behavior` (`allow` or `deny`), `message`           | Answers the pending call when `request_id` matches it; otherwise ignores it.                                                                                                                                                          |
| `interrupt` | none                                                              | Drops every untaken input, cancels any pending approval, ends any turn with the error result `Interrupted` and `state` `idle`, then sends `interrupted`.                                                                               |
| `configure` | `settings` (`model`, `effort`, `permission_mode`), all of them    | Uses the settings for the next request.                                                                                                                                                                                               |

`ur` writes:

| `type`        | Fields              | Meaning                                                                                                                                                                                   |
| ------------- | ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `taken`       | `message_id`        | The oldest untaken input entered the running turn.                                                                                                                                       |
| `dropped`     | `message_id`        | The oldest untaken input was dropped by an interrupt.                                                                                                                                    |
| `state`       | `state`             | `running` (an approved call resumes), `requires_action`, or `idle` (the turn ended).                                                                                                    |
| `update`      | `update`            | An `agent.Update` of type `text_delta`, `reasoning_delta`, `tool_use`, `tool_result`, `result`, `approval`, or `approval_cancelled`, without `time` or `message_id`. |
| `resumable`   | `token`             | The conversation file now has its first entry (the token is its ID), or, empty, the conversation to resume could not be read.                                                            |
| `interrupted` | none                | One `interrupt` has been carried out. Every update it caused precedes it.                                                                                                                |

`ur` stops when stdin reaches EOF or on `SIGTERM`. It cancels the turn, which
kills each command's process group, waits for its commands to be reaped, and
exits. It ignores `SIGINT`, so a Ctrl-C in `make dev`'s terminal reaches it
only as `oxd`'s orderly stop. Images travel as base64 in `data` (a `[]byte`
field), since `agent.Image` marshals as its ID alone.

### The adapter

`internal/ur.Agent` implements `agent.Agent`. The session goroutine makes every
call and handles every event. It keeps:

- `tracker *agent.Tracker`: inputs written and not yet reported taken or
  dropped. `Send` writes the input, then `Accept`s it, and returns the write's
  error without publishing anything if the write fails. `taken` calls `Take`,
  and `dropped` calls `DropFirst`, each after checking that the ID is the
  tracker's first.
- `approvals []agent.Approval`, in arrival order, from `approval` and
  `approval_cancelled` updates. An `approval_cancelled` for an approval no
  longer held, because it was answered meanwhile, is not published.
- `turn string`: the latest input `ur` took. It is set before `Take`, so the
  state change `Take` publishes names it, and cleared after `state` `idle`.
  `publish` sets `Time` and tags an update with no `MessageID` with `turn`.
  The adapter is the only place that tags updates.
- `stops []func(error)`: interrupts written and not yet acknowledged, oldest
  first. Each `interrupted` calls the oldest. The end calls the rest.
- `settings`, and `lost bool` (`ur` reported the conversation gone).

Methods:

- **`Start`** refuses at once when `OPENROUTER_API_KEY` is unset, as today, so
  that send is still refused with an error. Otherwise it starts `ur` through
  `internal/process` and returns.
- **`Approve`** parses `behavior` and `message` at the boundary and returns
  today's error for anything else. It returns `ErrApprovalNotFound` for an ID it
  does not hold. Otherwise it removes the approval, publishes
  `approval_answered`, and writes `approve`.
- **`Interrupt`** calls `done(nil)` at once when the tracker is idle. Otherwise
  it writes `interrupt` and appends `done` to `stops`.
- **`Configure`** returns `ErrExited` after the end and `ErrBusy` unless idle.
  Otherwise it writes `configure` with `patch.Apply(settings)`. When that
  differs from the current settings, it adopts them and calls `OnConfigured`,
  then `done(nil)`. A failed write calls `done` with the error and adopts
  nothing.
- **`Idle`** is the tracker's. **`Parkable`** is `Idle`.
- **`Stop`** terminates `ur` and ends the agent with the reason `stopped` when
  the tracker's reported state is not `idle`, and with no reason otherwise, as
  today's Ur publishes.
- **`Reaped`** is the process's.

The end (`Stop`, `ur`'s exit, or a protocol error) runs once:

1. `tracker.End()`.
2. Publish `approval_cancelled` for each held approval.
3. Publish `exited` when the reason is not empty. It names `turn`, else the
   latest untaken input.
4. Call every waiting stop with `nil`.
5. Mark the agent exited and close `done`.

The exit reason:

- After `resumable ""`: `OnResumable("")`, and the reason is the lost
  conversation notice, today's `errLost` text: "Ur could not resume the earlier
  conversation; the next prompt starts a new one without its context".
- Otherwise: `Ur exited: <wait error>`, then `ur`'s stderr tail on the next
  line when it has one.

A line from `ur` that does not decode, an unknown `type`, an `update` of a type
not listed above, or a `taken` or `dropped` that does not name the tracker's
first input is a protocol error. The adapter terminates `ur` and ends the agent
with `Ur broke its protocol: <detail>`.

### Shared process I/O

`internal/process` takes Claude's `startProcess` pipe setup, `logStderr`,
`read`, `deliverOutput`, and `Terminate` as they are, parameterized by:

- the log name (`claude` or `ur`), which goes beside the session ID in each
  stderr log line;
- a `parse func([]byte) agent.Event`, which the reader runs on each nonblank
  stdout line. Claude passes its `message`-or-`plainText` parse, so Claude's
  `Handle` is unchanged;
- the signal `children.KillAll` sends: `SIGKILL` for Claude, as today, and
  `SIGTERM` for `ur`, which then kills its commands' process groups before it
  exits.

It delivers `process.Exit{Err, Stderr}` after the last line. `Terminate(after)`
sends `SIGTERM` and kills the process after `after`, so Claude keeps its
`killAfter` and `ur` uses the same ten seconds. `internal/claude` keeps its
argument building, environment, and stdin writer.

### Concerns

- **Requirements.**
  - *Ur sessions*, *Ur's shell commands*, *Ur's instructions*: behavior
    unchanged. The one change is the lost conversation, under Requirement
    changes.
  - *One prompt at a time* and *Message delivery*: `Send` accepts at once, so a
    second prompt sees the first as pending. `ur` takes inputs in order, at
    the same points as today.
  - *Stop*: the reply waits for `interrupted`, which follows the stopped turn's
    `result` and `idle`. If the turn ended on its own and `ur` started a
    pending input before reading `interrupt`, that turn is the one stopped, and
    the reply still waits for it, as Claude's interrupt does.
  - *Approvals wait* and *One answer per approval*: approvals live in the
    adapter, which refuses a second answer synchronously.
  - *Pending setting changes* and *When settings can change*: `Configure` runs
    only while the adapter is idle. Idle means `ur` has reported `idle` and
    holds no untaken input, and `ur` never starts a turn by itself, so the
    `configure` line reaches `ur` before any later input.
  - *Parking*: the next send starts a new `ur` with `-resume`.
  - *Unexpected exits*: the change in its text is under Requirement changes.
  - *Stopping oxd*: the session's `Stop` terminates `ur`, and `children.WaitAll`
    waits for it. A second stop sends `ur` `SIGTERM`, and `oxd`'s exit closes
    `ur`'s stdin. Either way `ur` kills its commands' process groups. If `oxd`
    dies without stopping, stdin EOF does the same.
  - *Independent sessions*: a crash or race report in `ur` ends one session.
  - *Titling model*: titling with Ur runs `ur -ask`, bounded by `titleLimit`
    through its context.
  - Across reconnects, reloads, and a second page, the session's history and
    snapshot behave as today: the adapter publishes the same updates in the
    same order.
- **Settings.** `ur` starts with the session's settings as flags, and each
  `configure` line carries the full settings the adapter adopts, so `ur` and the
  page cannot disagree. A failed write adopts nothing. A restart starts from
  `sess.settings`.
- **Settling.**
  - An interrupt's reply waits for `interrupted` or the end.
  - `Stop` kills `ur` ten seconds after `SIGTERM`.
  - `ur -ask` ends with its context: `CommandContext` kills it, and
    `WaitDelay` is one second.
  - Every input written ends as `taken`, `dropped`, or the end.
  - `ur` reads stdin continuously on its own goroutine, and `oxd` drains `ur`'s
    stdout into an unbounded queue, so neither side's writes wait on the other.
- **Process I/O.** `internal/process` gives `ur` Claude's guarantees: the last
  line before EOF is read, exit is seen, and the stderr tail reaches the
  `exited` reason. `ur`'s commands write to pipes `exec` creates, never to
  `ur`'s own stdout. `ur -ask`'s error is its stderr, trimmed, as `claude.Ask`
  reports.
- **Construction.**
  - Only `ur` decides when an input is taken or dropped. The adapter's tracker
    changes only on `ur`'s reports, `Send`, and the end, and rejects a report
    out of order.
  - Only the adapter publishes updates: it tags and times them, and `ur` sends
    none of the lifecycle types (`prompt*`, `state`, `exited`, `parked`,
    `approval_answered`).
  - `wire.Command` and `wire.Event` are decoded once at each boundary.
  - `ur` owns the conversation file, and `oxd` holds only the token.
- **Ownership.**
  - The adapter's `stops`: needed so Stop returns only after the stopped turn
    ends, even when `ur`'s turn ends on its own first. Each `interrupt` gets
    one `interrupted`, so the FIFO pairs them. The end clears it.
  - `turn`: tags updates, as `ur`'s `turn.prompt` did. Cleared on `idle` and
    by the end.
  - `approvals` and `lost`: cleared by the end.
  - `ur`'s `queued`: inputs read and not taken, owned by its loop goroutine.
    `taken` or `dropped` clears each one, and exit clears the rest.
  - The commit body repeats this.
- **Claude Code.** No new assumption. Claude's process I/O moves unchanged, and
  `internal/claude`'s process tests (`TestExitReportedWhileAChildHoldsStdout`,
  `TestOutputDrainsBeforeSlowDelivery`, `TestStartupFailureOnStderr`,
  `TestDeliveryEndsWithTheAgent`) guard the move.
- **Trust boundaries.**
  - `ur` is found beside the resolved `oxd` executable, as `ox-mcp` is.
  - `ur` inherits `oxd`'s environment, including `OPENROUTER_API_KEY`, and
    strips the key from its commands' environment, as today.
  - Only `oxd` holds `ur`'s pipes.
  - `ur` writes only the conversation directory `-dir` names, and its commands
    run in the session's directory.

## Check your assumptions

1. **An Ur conversation that can no longer be read.**
   - **Plan:** The send goes through, then the prompt is dropped with a notice
     in the transcript and its text returns to the composer, as Claude's lost
     conversation behaves. The next send starts a new conversation. Only `ur`
     reads its conversation file, and it reads it after it starts, so `oxd`
     learns of the loss only after the send has been accepted.
   - **Alternative:** Keep today's error at the composer. Either the send waits
     for `ur` to start and read its file, which holds that session's requests
     on a child process, or `oxd` reads Ur's file itself, which splits the
     file's format across two programs.
   - Requirement *Ur sessions*. Old: "If its saved conversation can no longer
     be read, the send is refused with an error saying so, and the next send
     starts a new conversation without the earlier context." New: "If its saved
     conversation can no longer be read, the prompt is dropped with a notice in
     the transcript and its text returns to the composer, and the next send
     starts a new conversation without the earlier context."
2. **A crashed Ur shows why.**
   - **Plan:** When `ur` exits unexpectedly, the transcript shows the reason
     and the session parks. The next send resumes the conversation. Today the
     same fault takes down `oxd` and every session.
   - **Alternative:** None that keeps the other sessions running.
   - Requirement *Unexpected exits*. Old: "A Claude process that exits without
     Ox stopping it leaves the reason in the transcript." New: "A Claude or Ur
     process that exits without Ox stopping it leaves the reason in the
     transcript."

## Test plan

- **Ur behavior through the adapter and a real `ur`.** `internal/ur/ur_test.go`
  keeps every behavior test (two turns, providers, stream errors, interrupt,
  input during a turn, configure, stop, resume, images, instructions, save
  failure, shell approved, denied, auto, invalid, interrupt, stop, reaped,
  input between steps, permission modes, ask). Its `start` helper builds
  `Harness(os.Args[0], server.URL, dir, providers)` and sets `OX_UR_MAIN=1`. A
  new `TestMain` runs `engine.Main(os.Args[1:])` when `OX_UR_MAIN` is `1`, the
  pattern `cmd/oxd` and `cmd/ox-mcp` use. `TestStartWithoutKey` and `TestAsk`
  change only for the new `Harness` signature. `TestAsk`'s errors come from
  `ur`'s stderr.
- **Lost conversation.** `TestResumeLost` changes: `Start` succeeds, and a
  `Send` is followed by `prompt_cancelled` and `exited` with the lost notice,
  with `OnResumable("")`, for both the missing and the truncated file.
- **Unexpected exit.** New `TestUnexpectedExit` in `internal/ur` uses a
  stand-in script as `ur` that reads one line, prints a reason on stderr, and
  exits 3. A sent prompt ends with `prompt_cancelled` and `exited` whose error
  holds `Ur exited: exit status 3` and the reason, and `Exited()` is true. No
  existing test can carry it, since today's Ur has no process to exit.
- **Protocol error.** New case in `TestUnexpectedExit`: the stand-in prints
  `{"type":"taken","message_id":"other"}`, and the agent ends with the protocol
  reason.
- **Interrupt race.** `TestInterrupt` gains the assertion that `done` runs only
  after the stopped turn's `result` and `idle` are published.
- **Second stop kills commands.** `TestKillAllEndsCommands` moves from
  `shell_test.go` to `ur_test.go`, still in its own process. It starts a
  command in auto mode that leaves a child, calls `children.KillAll()`, and
  checks that the command and its child are gone. It now guards `ur`'s
  `SIGTERM` handling.
- **`oxd` gone.** New `TestEndOfInputEndsCommands` in `internal/ur/engine` runs
  `Main` in-process on pipes with a canned OpenRouter, starts a command in auto
  mode, closes stdin, and checks that `Main` returns and the command's group is
  gone. Only the engine's own test can close its stdin without also sending
  `SIGTERM`.
- **Shell unit tests.** `shell_test.go`, minus `TestKillAllEndsCommands`, moves
  to `internal/ur/engine` unchanged.
- **Claude process I/O.** The existing `internal/claude` tests, unchanged, guard
  the move to `internal/process`.
- **`oxd` refuses without `ur`.** New `TestRefusesToStartWithoutUr` in
  `cmd/oxd/main_test.go` copies the test binary into a temporary directory as
  `oxd` and `ox-mcp`, runs it, and expects the refusal naming `ur`.
  `TestRefusesToStartWithoutForwarder` still names `ox-mcp`, since `oxd`
  checks `ox-mcp` first.
- **Live.** `TestLiveUr` builds `ur` into its `bin` directory as it builds
  `ox-mcp`, and builds its harness from that path. It already titles with Ur, so
  it covers `ur -ask` against OpenRouter.
- **Page.** `make e2e` runs with `ur` built, which shows `oxd` starts. The fake
  harness covers the page.

## Implementation plan

1. **`internal/process`.** Move Claude's pipe setup, `logStderr`, `read`,
   `deliverOutput`, `Terminate`, and the reaped channel into a `Process` type,
   parameterized as Decisions say. Switch `internal/claude/claude.go`
   `startProcess` to it, keeping Claude's parse and `processExit` handling. Run
   `internal/claude` tests.
2. **`internal/ur/wire`.** Add `Command`, `Event`, `Input`, `Image`, and the
   type constants.
3. **`internal/ur/engine`.** Move `ur.go`, `stream.go`, `shell.go`,
   `instructions.go`, and `shell_test.go` here (`git mv`), then:
   - Replace the `agent.Agent` methods with a loop over stdin commands, internal
     events (`delta`, `ended`, `ran`), and `SIGTERM`.
   - Replace `agent.Tracker` with `queued []wire.Input`: `Accept` becomes an
     append, `Take` emits `taken`, `DropFrom(0)` emits `dropped` for each,
     `Report` emits `state`.
   - `publish` emits `update`, and `onResumable` emits `resumable`.
   - `Approve` ignores an unmatched ID. `Interrupt` ends with `interrupted`.
     `Configure` replaces the settings.
   - Stop on EOF or `SIGTERM`: cancel the turn, wait on `shells`, return.
     Ignore `SIGINT`.
   - `startShell` uses `cmd.Start` instead of `children.Start`, and `wait` drops
     `children.Done`.
   - `Main` parses flags. Agent mode refuses a missing key on stderr with exit
     1. A resume that cannot load logs the cause to stderr, emits
     `resumable ""`, and exits 1. Ask mode runs `ask` and prints the reply.
   - Add `TestEndOfInputEndsCommands`.
4. **`cmd/ur/main.go`.** One line, as Decisions say.
5. **`internal/ur`.** Write `harness.go`:
   - `Harness(command, endpoint, dir, providers)` with `Start`, `CheckEffort`,
     `CheckPermissionMode`, `TakesProviders`, and `Ask`.
   - `agent.go`: the adapter, as Decisions describe.
   - `ask.go`: runs `ur -ask` with the text on stdin, registered with
     `children` under `SIGKILL`, `WaitDelay` one second, and stderr as the
     error.
   - Update `ur_test.go` as the test plan says, and add `TestMain`,
     `TestUnexpectedExit`, and the moved `TestKillAllEndsCommands`.
6. **`cmd/oxd`.** `findBeside("ox-mcp")`, then `findBeside("ur")`.
   `"ur": ur.Harness(urPath, "", *urDir, cfg.Harnesses["ur"].Providers())`.
   Update `main_test.go` line 180 and add `TestRefusesToStartWithoutUr`.
7. **Builds.** `Makefile` `build` adds `./cmd/ur`. `e2e/global-setup.ts` adds
   `./cmd/ur`. `TestLiveUr` builds `ur` beside `ox-mcp` and passes its path.
8. **Docs.** As listed below. Run `make docs`.
9. Run `make test`, `make e2e`, and `make live`. Check by hand in `make dev`
   that an Ur session chats, asks for approval, runs a command, stops, parks,
   and resumes. Check that `kill -9` of its `ur` shows the exit in the
   transcript while a Claude session keeps working.

## Requirement changes

- **Changed:** *Ur sessions*.
  - Old: "**Ur sessions.** Ur chats through OpenRouter with the user's
    `OPENROUTER_API_KEY`, streaming its replies; a send to an Ur session without
    the key is refused with an error. An Ur session parks and resumes its
    conversation like a Claude session, including a reply cut off partway. If
    its saved conversation can no longer be read, the send is refused with an
    error saying so, and the next send starts a new conversation without the
    earlier context."
  - New: "**Ur sessions.** Ur chats through OpenRouter with the user's
    `OPENROUTER_API_KEY`, streaming its replies; a send to an Ur session without
    the key is refused with an error. An Ur session parks and resumes its
    conversation like a Claude session, including a reply cut off partway. If
    its saved conversation can no longer be read, the prompt is dropped with a
    notice in the transcript and its text returns to the composer, and the next
    send starts a new conversation without the earlier context."
- **Changed:** *Unexpected exits*.
  - Old: "**Unexpected exits.** A Claude process that exits without Ox stopping
    it leaves the reason in the transcript."
  - New: "**Unexpected exits.** A Claude or Ur process that exits without Ox
    stopping it leaves the reason in the transcript."

## Documentation updates

- `docs/architecture.md`:
  - The two diagrams: `oxd` runs a `ur` process, which writes the JSONL files,
    and `internal/process` sits under `claude` and `ur`.
  - The component table: rows for `cmd/ur`, `ur/engine`, and `process`, and the
    `ur` row becomes the adapter.
  - The `Agent.Reaped` bullet: `ur` reaps its commands' process groups before
    it exits.
  - "The Ur agent": the process, its stdio boundary in a few lines pointing to
    `internal/ur/wire`, and what the adapter keeps.
- `docs/protocol.md`: the lost-conversation paragraph under workspaces and
  sessions says "the harness" instead of "Claude", since it now holds for Ur.
- `README.md`: "`oxd` requires `ox-mcp` and `ur` in the same directory".
- `AGENTS.md`:
  - The source map gains `cmd/ur`, `internal/process`, `internal/ur/engine`, and
    `internal/ur/wire`, and `internal/ur` becomes "Ur harness".
  - "A harness's own goroutines only read its process, fire timers, and do the
    I/O its turn needs, such as Ur's HTTP requests and shell commands" drops the
    Ur example.
  - This is workflow text, which lands as `AGENTS.md` says.
