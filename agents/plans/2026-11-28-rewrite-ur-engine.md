# Rewrite Ur's engine around one loop, typed events, and turn steps that report errors

## Goal

`internal/ur/engine` is the `ur` program. It works, but its rules are spread
across functions that each re-decide them, which makes it costly to change:

- `serve` runs two select loops, one for a lost conversation and one for normal
  work, and picks the exit status case by case in four branches.
- `handle` takes `any` and checks whether output is stale in three different
  ways: by turn pointer, by step pointer, and by request ID.
- Turn steps end their own turn. `request`, `answer`, and `handle` each call
  `finish` deep in a call chain, and every caller has to `return` right after.
  `finish` can then `begin` the next turn, so the stack recurses through turns.
- A turn starts in two places: `command` when idle, and `finish`.
- Both entry points build a request body, `engine.body` for a session and `ask`
  for `-ask`, and each copies the effort and provider-order rules.
- `read` in `stream.go` handles SSE line framing, chunk decoding, assembling
  tool calls from fragments, and mapping the finish reason, all in one
  function.
- oxd's commands are checked inside the loop (`command`), so a protocol error
  and a turn's own failure come back through the same `error` return.
- The `engine` struct mixes fields set at start, which request goroutines read,
  with fields that only the loop owns. A comment is the only thing that
  separates them.

The rewrite keeps every behavior in `docs/functional-requirements.md` ("Ur
sessions", "Ur's shell commands", "Ur's instructions") and the wire protocol
as they are. It gives each rule one owner:

- One loop decides when ur stops and with what status.
- One function starts turns.
- One place turns a step's error into the turn's result.
- One function builds a request body.
- Commands are checked once, where they are read.

## Related code

- `internal/ur/engine/engine.go`: today's `run`, `serve`, `read`, `command`,
  `handle`, turn steps, conversation file, and `conversation` merge.
- `internal/ur/engine/stream.go`: the OpenRouter message types, `ask`, `post`,
  `statusError`, and `read`, the SSE parser.
- `internal/ur/engine/mcp.go`: the forwarder. Its `read` closes `exited`, and
  `serve` selects on that channel.
- `internal/ur/engine/shell.go`, `instructions.go`: these stay. The shell
  command's lifetime (`startShell`, `wait`, `killGroup`) is already owned in
  one place.
- `internal/ur/engine/engine_test.go`: `TestEndOfInputEndsCommands` and the
  white-box `TestStaleOutput`.
- `internal/ur/ur_test.go`: the black-box suite. It drives the real `ur`
  through the harness against a canned OpenRouter and a stand-in forwarder. It
  is what protects the rewrite.
- `internal/ur/agent.go`: the oxd side of the wire. Its parsing sets the order
  and validity of events that the rewrite must keep.
- Prior art: the abandoned `split-ur-engine` worktree moved declarations into
  `cli.go` and `conversation.go` without changing who owns what. Its review
  found nothing to fix because nothing changed. This plan changes ownership,
  not just file layout.

## Decisions

### Files

- `main.go` (created) holds the package doc, `Main`, and `run`.
- `engine.go` holds the engine, its loop, the command reader, and stop.
- `turn.go` (created) holds the turn and its steps.
- `conversation.go` (created) holds the conversation file and the merge into
  request messages.
- `stream.go` holds the OpenRouter types, `client`, and the SSE reader.
- `mcp.go`, `shell.go`, and `instructions.go` keep their contents, apart from
  the forwarder change below.

### State, split by who reads it

```go
// set before the loop starts; request and command goroutines read them
type engine struct {
	client    client       // endpoint, key, providers
	workspace string
	ox        *forwarder   // nil without -mcp
	out       *json.Encoder
	stderr    io.Writer
	events    chan event   // every goroutine's output, read only by the loop
	stopped   chan struct{}
	shells    sync.WaitGroup
	loop      loopState    // only the loop goroutine touches this
}

type loopState struct {
	settings agent.Settings
	conv     *conversation
	queued   []wire.Input
	turn     *turn // nil while idle
}
```

The exact field layout is the implementer's call. What matters is that the
fields the loop owns sit in one named value, separate from the fields set at
start.

### One event channel, sealed

The reader goroutine, request goroutines, command goroutines, and the
forwarder's reader all deliver through `deliver(event)`. It blocks until the
loop takes the event or `stopped` closes, as it does today.

`event` is a `//sumtype:decl` interface with these variants:

- the typed commands: `input`, `approval`, `interruption`, `configuration`
- `inputEnded{err}`
- `delta{turn, text, reasoning}`
- `streamed{turn, calls, err}`
- `ran{turn, group, outcome}`
- `answered{id, outcome}`
- `forwarderExited{err}`

`serve`'s second select loop and the `readErr` pointer go away.

### Commands checked once, by the reader

The reader decodes each line and converts it to a typed command:

- `input` carries `wire.Input`.
- `approval{requestID, allow bool, message}`. Anything but `"allow"` denies, as
  today.
- `interruption`.
- `configuration{agent.Settings}`.

If a line fails to decode or is not a valid command, the reader delivers
`inputEnded{err}` and stops. The error texts stay as today: `read command: …`
for a decode error, and `input command without an input`,
`configure command without settings`, and `unknown command "…"` for invalid
commands.

Past the reader, handling a command cannot fail on a protocol error.

### One loop decides stopping and status

```go
func (e *engine) loop(terminate <-chan os.Signal) error // nil: exit 0
```

`loop` handles the events as follows:

- **`terminate`:** returns nil.
- **`inputEnded`:** returns its error, or nil at EOF.
- **`forwarderExited`:** returns `ox-mcp exited: <err>`, or `ox-mcp exited`.
  This always counts as an error.
- **Every other event:** calls `e.handle(ev)`. An error from `handle` ends the
  running turn with `agent.Result{Text: err.Error(), IsError: true}`.

After every event, `loop` calls `startNext`.

`serve` does the following, in order:

1. Opens the conversation.
2. On a lost conversation, emits `resumable` with an empty token and waits for
   `inputEnded` or `terminate`, ignoring other events. It returns 1, as today.
3. Starts the forwarder.
4. Runs `loop`, then `stop`.
5. Prints a non-nil error and returns 1, or returns 0.

`forwarder.read` closes `exited`, which `close` waits on, and then delivers
`forwarderExited{f.err}`.

### One place starts turns

`startNext` begins a turn while none runs and inputs wait:

```go
for e.turn == nil && len(e.queued) > 0 {
	if err := e.begin(); err != nil {
		e.finish(failed(err))
	}
}
```

`command(input)` only queues the input. `finish` only ends the turn: it
publishes the result, emits `state idle`, cancels the turn, and clears it. It
never begins another.

Events keep their current order:

- `result` and `idle` come before the next `taken`.
- `interrupted` comes after `idle`, because `interrupt` empties the queue
  first.

Between events, `queued` is non-empty only while a turn runs.

### Steps return errors and do not end turns

These functions return an `error` instead of calling `finish`: `begin`,
`request`, `next`, `answer`, `execute`, `callOx`, `approve`, and the handlers
for `delta`, `streamed`, and `ran`.

They wrap a save failure with the text the user sees today, for example
`Ur could not save the reply: %w`, `Ur could not save the prompt: %w`,
`Ur could not save the command's result: %w`, and
`Ur could not read AGENTS.md: %w`.

These are the only functions that return turn errors, and each runs only
while a turn runs, so the loop's `finish` always has a turn.

A failure the model is told about stays an `outcome` and does not become an
error. This covers a bad call, a bash process that fails to start, an
unreachable forwarder, and a denial.

### Steps hold the calls they wait on, never empty

```go
type pending struct { call toolCall; rest []toolCall } // call is the one awaited
type streaming struct{}
type asking    struct { pending; command shellCommand; requestID string }
type executing struct { pending }
type calling   struct { pending; id int }
```

Because `call` is a field rather than `calls[0]`, an awaited call always
exists. `asking.command` is what the approval card showed, and `approve` runs
exactly that value (deslop F1).

### One staleness rule

Output from a goroutine applies only while the turn it was started for is the
running turn: `e.turn == ev.turn`. This is enough, because a turn runs one
goroutine at a time and leaves the step that started it only when that
goroutine's last event arrives or the turn ends. The handler for each event
type checks it the same way, through one helper.

`ran` always emits `command_ended` first, whether or not its turn is current,
because that event is about the process group, not the turn.

A forwarder answer applies only when the current step is `*calling` with its
`id`. The forwarder cannot know which turn a call belongs to, and request IDs
are unique within the process.

### One request body

```go
type client struct { endpoint, key string; providers map[string][]string }
func (c client) body(s agent.Settings, messages []message, tools []json.RawMessage) []byte
func (c client) post(ctx context.Context, body []byte, onDelta func(string, []reasoningDetail)) ([]toolCall, error)
```

`body` is the only place that applies the effort and provider-order rules.
The session loop builds the body, since it reads history and settings, and
the request goroutine only posts it.

`ask` calls `c.body(agent.Settings{Model: model, Effort: effort}, …, nil)` and
`c.post`. Both entry points end through the same finish-reason mapping
(11-26 F24).

### SSE reader in three parts

- `sseData(r io.Reader) iter.Seq2[string, error]` yields each `data: ` payload.
  - It trims `\r\n`.
  - It skips blank lines and `:` comments.
  - It stops at `[DONE]` or EOF, and still reads a last line that has no
    newline.
  - It yields an error when a read fails.
- `type completion struct { reason string; calls map[int]*toolCall }` has two
  methods:
  - `add(c chunk, onDelta)` returns the error from an error chunk, and
    otherwise passes on the text and reasoning and merges tool-call fragments.
  - `end() ([]toolCall, error)` maps the finish reason through
    `finishError(reason)` and checks each call's ID and name.
- `read` is the loop that joins them.

All error texts stay as they are.

### Conversation

```go
type conversation struct { dir, token string; entries []message }
func openConversation(dir, token string) (*conversation, error) // "" makes a new token, without reading anything
func (c *conversation) append(m message) error                   // writes, then appends to entries
func (c *conversation) messages(turnStart int) []message         // merge, stopped answers, earlier turns' reasoning stripped
func (c *conversation) reply() string                            // text of the trailing assistant run, or ""
```

`engine.save` is the one wrapper. It appends and emits `resumable` with the
token when the first entry is written.

The merge builds fresh slices for text, reasoning, and tool calls. A request
never writes into the saved entries.

### Requirements

"Ur sessions", "Ur's shell commands", and "Ur's instructions" keep holding
through the whole lifecycle:

- **Park and resume:** the file format and the merge rules are unchanged.
- **Reply cut off partway:** each delta is still saved before it is published.
- **Lost conversation:** the lost path is unchanged.
- **Interrupt and stop:** these still cancel the turn's context, which kills
  the running command's group. `stop` still waits on `shells` before it closes
  the forwarder.
- **Command timeouts and output limits:** `shell.go` is untouched.

No requirement changes.

### Process I/O

ur's own I/O does not change:

- stdout is written only by the loop, through `emit`.
- stdin is read to EOF by one goroutine.
- A forwarder that exits is still reported with its error on stderr, which the
  harness folds into the exit reason.

### Settling

No new waits are added. The existing waits are unchanged:

- `deliver` blocks until the loop takes the event or `stopped` closes.
- `stop` waits on `shells`, which ends because the canceled context kills each
  group and `WaitDelay` bounds the pipe.
- `stop` waits on the forwarder, which exits once its stdin closes.

### Ownership

This change adds no state, timer, retry, lock, or stored field. It moves
state, and the moves are these:

- The reader's commands now reach the loop through `events`, not a channel of
  their own.
- `forwarderExited` replaces the select on `exited`.

Neither move adds a race. Each producer still blocks on `deliver` until the
loop takes the event or `stopped` closes.

### Defect evidence (from past audits), checked against this design

1. **Command lifetime (#147, deslop F9).** Inside ur, a command has one owner
   and one ending path:
   - Its turn's context. Interrupt, a finished turn, and stop all cancel it.
   - `shell.wait`, which kills the group and reaps it before `ran` or
     `shells.Done`.

   The crash path lives in `internal/ur/agent.go`, which kills reported
   groups, and is unchanged. `TestCrashEndsCommands` and `TestShellStop` cover
   it.
2. **Outcomes (#156).** A single `finishError` maps the finish reason, and both
   entry points use it through `client.post`. `shell.wait` maps a command's
   exit.
3. **Approved and run values (deslop F1).** `asking.command` is the value that
   runs.
4. **Tool names (#189).** The engine uses the bare names that `tools/list`
   returns and adds no other form.
5. **First input.** An input command without an input ends ur with a clear
   error at the reader. A valid input is queued and taken. ur never accepts an
   input halfway.

## Check your assumptions

None.

## Test plan

The black-box suite in `internal/ur/ur_test.go` protects the rewrite. It
drives a real `ur` process through the harness and covers every path the
rewrite restructures:

- turns, interrupts, queued inputs, configure, and stop
- resume and lost conversations
- save failures
- stream errors
- shell approval, auto mode, denial, interrupt, and stop
- Ox tools, including stale answers and forwarder exit or failure
- crashes and `-ask`

It stays unchanged, apart from the pins below.

Pin these first, on the current code. Each one must pass before any rewrite
starts:

- **Bad commands from oxd end ur with status 1 and a clear error.** This is a
  new table test, `TestBadCommand`, in `internal/ur/engine/engine_test.go`. It
  drives `run` with these lines:
  - `not json`
  - `{"type":"bogus"}`
  - `{"type":"input"}`
  - `{"type":"configure"}`

  For each one, `run` returns 1 and stderr contains `read command:`,
  `unknown command "bogus"`, `input command without an input`, or
  `configure command without settings`. No existing test sends ur a bad
  command, and the rewrite moves this check into the reader.
- **Reasoning fragments merge by index.** Extend `TestShellAuto`'s fragments:
  the first `reasoning.text` fragment carries an `id`, the last carries a
  `signature`, and an encrypted detail is added at index 1.

  Assert two things:
  - The calling message's `reasoning_details` in the second and third requests
    is two details: index 0 with the joined text, the first id, and the last
    signature, and the encrypted detail, unchanged.
  - The published reasoning deltas leave the encrypted detail out.

  Today only the text join is checked.
- **SSE lines ending in CRLF parse.** Add a `CRLF` case to
  `TestLastLineWithoutNewline` (`\r\n\r\n` after each event, with a final
  `data: [DONE]\r\n`), and widen its comment.

Replace one white-box test in the rewrite:

- **`TestStaleOutput`** moves to the new event types. A `delta`, `streamed`, or
  `ran` for a turn that is not running saves and publishes nothing. `ran` still
  emits exactly `command_ended`, and the running turn goes on.

Checks:

- `make test`.
- `make live`, for `TestLiveUr`. It must run, because the rewrite changes how
  Ox drives Ur end to end.
- `make metrics` as a sanity read. The metrics are not a target.

## Implementation plan

1. **Pins.** Add `TestBadCommand`, the reasoning-merge assertions in
   `TestShellAuto`, and the CRLF case. Run `make test` on the unchanged engine.
   All of them pass.
2. **`stream.go`.**
   - Add `client` with `body` and `post`, and make `ask` use them.
   - Split `read` into `sseData`, `completion.add` and `completion.end`, and
     `finishError`.
   - Remove `engine.stream`. The goroutine moves to `turn.go`.
3. **`conversation.go`.** Move the file I/O and the merge into the
   `conversation` type. `save` becomes the engine's wrapper that emits
   `resumable`. The merge builds fresh slices.
4. **`turn.go`.**
   - Add `pending` and the step types.
   - Convert `begin`, `request`, `next`, `answer`, `execute`, `callOx`,
     `approve`, and the stream, command, and answer handlers so they return
     errors.
   - Add the staleness helper.
   - Write `finish` without the call to `begin`.
   - `interrupt` keeps its event order.
5. **`engine.go`.**
   - Define the `event` sum type and the typed commands.
   - Have the reader decode and check commands and deliver `inputEnded`.
   - Add `loop` and `startNext`.
   - `serve` handles the lost path, starts the forwarder, runs `loop`, and
     calls `stop`.
   - Split the struct into the fields set at start and `loopState`.
6. **`mcp.go`.** `read` delivers `forwarderExited` after it closes `exited`.
7. **`main.go`.** Move `Main` and `run` here, along with the package doc. `run`
   parses flags into a `client` and `agent.Settings`, then hands off to the
   ask path or to `serve`. Flag errors and exit codes are unchanged.
8. **`engine_test.go`.** Rewrite `TestStaleOutput` for the new types. Keep
   `TestEndOfInputEndsCommands`.
9. **`AGENTS.md` Source Map.** Add `main.go`, `turn.go`, and `conversation.go`
   under `internal/ur/engine`, and retitle `engine.go` to
   `# event loop, commands, and stop`.
10. Run `make test` and `make live`.

## Requirement changes

None.

## Documentation updates

- `AGENTS.md` Source Map: the engine's new files. `docs/architecture.md` ("The
  Ur agent") states only facts the rewrite keeps.
