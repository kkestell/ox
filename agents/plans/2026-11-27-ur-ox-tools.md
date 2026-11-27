# Give Ur the Ox tools

## Goal

An Ur session offers its model only `shell`, so it cannot list, read, or message
other sessions, look up Ox's models, or start a child session. When this change
is done, Ur's model is offered `list_models`, `list_sessions`, `send_message`,
`read_session`, and `create_session` beside `shell`. `ur` gets them from
`ox-mcp` exactly as Claude does: it starts the forwarder `oxd` names, speaks
MCP to it over stdio, and ends it when `ur` stops. Ur's instructions tell it
that it is an Ox session and how to reach the others. Resolves
[#50](https://github.com/kkestell/ox/issues/50).

## Related code

- `internal/server/mcp.go` — the tool table, `mcp/attach`, and `handleMCP`,
  which answers `initialize`, `tools/list`, and `tools/call`, each request on
  its own goroutine, so answers can arrive out of request order. Unchanged.
- `cmd/ox-mcp/main.go` — the forwarder: reads `OX_SOCKET` and `OX_SESSION_ID`,
  attaches, then copies lines both ways; writes only `oxd`'s answers to stdout;
  on stdin EOF it half-closes the socket, and exits once `oxd` closes the
  connection. Unchanged.
- `cmd/ox-mcp/main_test.go` — `startOxd` serves a real `server.Server` on a
  short unix socket path; `TestMain` runs the test binary as the forwarder.
- `internal/agent/agent.go` — `Options.MCP` (`MCPServer{Command, Env,
  Approved}`), filled by `internal/server/session.go` (`start`) for every
  harness. Unchanged.
- `internal/claude/claude.go` (`systemPrompt`) — Claude's Ox instructions.
- `internal/ur/agent.go` (`startAgent`) — builds `ur`'s command line.
- `internal/ur/engine/engine.go` — the turn loop: `serve`, `stop`, `next`
  dispatches each tool call, steps `asking` and `executing`, `handle`,
  `answer`, `interrupt`, `body`.
- `internal/ur/engine/shell.go` — `shellTool`, and `environment()`, which
  drops `OPENROUTER_API_KEY` from a command's environment.
- `internal/ur/engine/instructions.go` — Ur's system message.
- `internal/server/session.go` (`report`, `told`) — a child's report is skipped
  when its last step's `send_message` to the parent carried its tool_use ID in
  `_meta["claudecode/toolUseId"]`.

## Decisions

- **How `ur` receives the forwarder.** `ur` takes one flag, `-mcp=<json>`, the
  JSON of `agent.MCPServer` as `oxd` hands it to the harness. The Ur agent
  passes it when `options.MCP.Command` is set; the engine uses `Command` and
  `Env` and ignores `Approved`. One flag carrying the one type keeps the
  forwarder's command and env defined only in `session.go`. Without `-mcp`,
  agent mode offers only `shell` and its instructions leave out the Ox
  paragraph; `internal/ur` tests and server tests with `MCP{}` start `ur`
  that way. `oxd` always passes it.
- **Starting the forwarder.** In `serve`, after starting its stdin reader and
  before the loop, `ur` starts `Command` with `environment()` plus `Env`, its
  stdin and stdout as pipes and its stderr as `ur`'s own, so a forwarder's
  failure (`connect to oxd …`, `attach to session …`) reaches the exit reason
  the Ur agent publishes. It sends `initialize` (protocolVersion
  `2025-06-18`) and waits for its answer, sends `notifications/initialized`,
  then sends `tools/list` and waits for its answer: one request at a time,
  because `oxd` may answer concurrent requests in either order. Any failure
  here, including the forwarder exiting, makes `ur` exit 1 with the error on
  stderr. Each tool becomes an OpenRouter function definition
  (`{"type":"function","function":{name, description, parameters: inputSchema}}`),
  which `body` offers after `shellTool`. For a child, `tools/list` runs before
  the child is published; `oxd` answers it without a session.
- **Matching calls and responses.** Request IDs are integers from a counter the
  loop goroutine owns, continuing after the startup requests. Only the loop
  writes to the forwarder's stdin. After startup a reader goroutine reads the
  forwarder's stdout, decodes each line as a JSON-RPC response, and delivers
  `answered{id, outcome}` with `deliver`. A tool result's text and `isError`
  become the outcome; a JSON-RPC error becomes an error outcome with its
  message.
- **Dispatch.** `parseCall` becomes: `shell` → `parseShell`; an Ox tool → its
  arguments must be one JSON object (otherwise answered at once with
  `arguments: …`); anything else → `Ur has no tool %q`. An Ox call publishes
  `tool_use` with its arguments as `Input`, runs without approval in both
  modes, writes `tools/call` with
  `_meta: {"claudecode/toolUseId": call.ID}`, and sets the step
  `calling{calls []toolCall, id int}`. `_meta` lets a child Ur's
  `send_message` to its parent suppress the report as Claude's does; Ur's
  published `tool_use` already carries `call.ID`.
- **Dropping a late answer.** `handle` answers `answered` only when
  `e.turn != nil` and the step is the `*calling` with that `id`; any other
  answer belongs to a call an interrupt or stop ended, whose turn already
  finished, and is dropped. The next request answers that call with `stopped`
  through `conversation`, as for an interrupted shell command. Nothing is
  cancelled in `oxd`: every Ox tool settles on its own.
- **Ending the forwarder.** `ur` owns it. `stop`, after the shells are reaped,
  closes the forwarder's stdin and waits for the reader goroutine, which ends
  once stdout reaches EOF and then reaps the forwarder with `Wait`. The wait is
  bounded: on stdin EOF the forwarder half-closes the socket, `serveConn` reads
  EOF and closes the connection, and the forwarder exits; with `oxd` gone the
  socket is already closed. If `ur` is killed, the forwarder's stdin closes
  with `ur`'s pipe end and it exits by the same path. If the forwarder exits
  while `ur` runs (only when `oxd` closes the connection), the reader delivers
  `forwarderExited{err}`, and the loop stops as for stdin EOF and exits 1 with
  `ox-mcp exited` on stderr; a call waiting on it gets no answer, as at any
  stop.
- **Approval.** Settled: Ur's Ox tools never ask, in either mode. Children take
  the parent's permission mode, so a manual Ur session cannot start a child
  that runs commands without asking.
- **Instructions.** The part of Claude's Ox instructions that does not name
  tools moves to `internal/agent` as `agent.Sessions` (the paragraphs from "A
  message from another session arrives…" to the end), so Claude's prompt stays
  byte-identical. With `-mcp`, Ur's system message adds: "You are Ox session
  `<id>`. "Another session" or "the other agent" means another Ox session. Find
  them with `list_sessions` and message them with `send_message`. To hand work
  to another agent, start a child session with `create_session`." followed by
  `agent.Sessions`. The engine takes the ID from a new flag `-session-id`,
  which the Ur agent passes from `options.SessionID` with `-mcp`.
- **Requirements.** *Ur works with other sessions* (added) holds through the
  existing tool paths: each tool settles on the target session's goroutine,
  refuses archived, deleted, and missing sessions with a reason, and answers
  `shutting down` while `oxd` stops. A message to a parked Ur session already
  starts it (*Message delivery*). *Child reports* holds for an Ur child through
  `_meta`. *Ur's instructions* changes as below. *Ur's shell commands* is
  unchanged: Ox calls are not shell commands.
- **Settling.** Startup waits on two answers `oxd` gives from the connection
  goroutine without touching a session, or on the forwarder's exit. A call
  waits on `oxd`'s answer, which every tool path gives; an interrupt or stop
  ends the wait at once by finishing the turn. `stop` waits on the forwarder as
  bounded above.
- **Process I/O.** The forwarder's stderr is `ur`'s stderr, which `oxd`
  already reads to its end and puts in the exit reason. Its stdout is read to
  EOF by the reader goroutine, including a last line without a newline.
- **Construction.** The tools a request offers and the tools `next` dispatches
  are one list, read once from `tools/list`. The session a call acts for is
  fixed by the forwarder's env; the model cannot name another caller. Only
  the `*calling` step names the one answer the turn waits on.
- **Ownership.** New engine state: the forwarder's process, stdin, and reader,
  owned by `serve`/`stop`, ended by `stop` or by its own exit; the Ox tool list,
  set at startup and read-only after; the request counter and the `calling`
  step, owned by the loop goroutine and cleared by `answer`, `finish`, or
  `interrupt` like the other steps. No state in `oxd` changes. Race: an answer
  that arrives after its turn finished, dropped by ID.
- **Trust boundaries.** The forwarder runs as the user with the env `oxd`
  already gives Claude's forwarder, and reaches only `oxd`'s socket. It does not
  get `OPENROUTER_API_KEY`. Shell commands do not get `OX_SOCKET` or
  `OX_SESSION_ID`: they are in the forwarder's env, not `ur`'s.

## Check your assumptions

1. **Ur's Ox tools never ask for approval.** Kept, as the user settled.
2. **Requirement text.** Kept, as the user approved: adds *Ur works with other
   sessions* and changes *Ur's instructions*, as written under Requirement
   changes.

## Test plan

- **Forwarder handling (Ur, new `TestOxTools` in `internal/ur/ur_test.go`).**
  No existing Ur test starts a forwarder. A stand-in forwarder is the test
  binary under `OX_UR_FORWARDER=1` in `TestMain`: it writes its PID to a file
  named by an env var, answers `initialize`, lists one tool `echo`, and answers
  `tools/call` for `echo` with its `text` argument, except that `text: "hold"`
  is answered only when the next `tools/call` arrives, just before it. The
  harness gets `MCPServer{Command: os.Args[0], Env: {…}}`. Cases, each failing
  before the change:
  - the request offers `shell` and `echo`, and a manual-mode `echo` call runs
    with no approval update; its result reaches the next request as a tool
    message;
  - an `echo` call whose arguments are not a JSON object is answered at once
    with an error and the turn goes on;
  - interrupt during `hold`, then a turn that calls `echo "two"`: its tool
    message is `two`, the `hold` answer appears in no update or request, and
    the interrupted call reaches the model as `stopped`;
  - after `Stop` and `Reaped`, the forwarder's PID no longer exists;
  - a forwarder that exits at once (`Command: "false"`) ends the agent with an
    exit reason.
- **Without `-mcp` Ur offers only shell (Ur).** Add to `TestInstructions`
  "fresh each request" that the request's `tools` is just `shell` and the
  system message has no Ox paragraph.
- **End to end through `oxd` (new `TestUr` in `cmd/ox-mcp/main_test.go`).**
  `startOxd` already serves a real server on a socket and runs the test binary
  as the forwarder. Symlinks named `ur` and `ox-mcp` to the test binary in the
  test's temp dir, with `TestMain` switching on `filepath.Base(os.Args[0])`,
  let the one binary be both, since `ur` passes its environment to the
  forwarder. The server gets `MCP{Forwarder: <ox-mcp link>, Socket: socket}`
  and `ur.Harness(<ur link>, endpoint, dir, nil)` against an `httptest`
  OpenRouter that streams tool calls by request number. Cases:
  - the system message names the session ID;
  - `send_message` to a fake session delivers a message `From` the Ur session,
    and the next request carries `Message queued for <id>.`;
  - `create_session` creates a child of the Ur session in `manual` mode;
  - an Ur child whose last step is `send_message` to its fake parent sends the
    parent no report.
- **Claude's prompt is unchanged.** Byte-identical; `TestLive` already covers
  *Ox's instructions*.
- **Live:** extend `TestLiveUr` to serve the socket and build `ox-mcp` as
  `TestLive` does, with one turn asking Ur to call `list_sessions` and reply
  with the count; assert a `tool_use` named `list_sessions` and a successful
  result.

## Implementation plan

1. `internal/agent/agent.go`: add `Sessions`, the shared Ox paragraphs moved
   from `internal/claude/claude.go`; `systemPrompt` appends it, same bytes.
2. `internal/ur/agent.go` (`startAgent`): when `options.MCP.Command` is set,
   pass `-mcp=<json of options.MCP>` and `-session-id=<options.SessionID>`.
3. `internal/ur/engine/mcp.go` (created): the forwarder: start with
   `environment()` plus `Env`; synchronous `initialize`, `notifications/
   initialized`, and `tools/list` returning the function definitions and
   names; `write(id, name, arguments, toolUseID)`; the reader goroutine
   delivering `answered` and `forwarderExited`; `close` that closes stdin and
   waits for the reader and `Wait`.
4. `internal/ur/engine/engine.go`: flags `-mcp` and `-session-id`; start the
   forwarder in `serve` after `e.read`; the counter, `calling` step, and
   `answered`/`forwarderExited` handling; dispatch in `next`/`parseCall`;
   `body` offers `shellTool` plus the Ox definitions; `stop` ends the
   forwarder after the shells; update `Main`'s usage comment.
5. `internal/ur/engine/instructions.go`: take the session ID and add the Ox
   paragraph plus `agent.Sessions` when the engine has a forwarder.
6. Tests as in the test plan, then the docs below.

## Requirement changes

- **Added** (under Ur, after *Ur's shell commands*): **Ur works with other
  sessions.** Ur can do what Claude does under Sessions working together: look
  up Ox's models, list, read, and message other sessions, and start child
  sessions, in either permission mode without asking for approval.
- **Changed:** *Ur's instructions.*
  - Old: Ur knows the session's directory, its OS, and today's date, follows
    Ox's style, and follows the `AGENTS.md` in that directory as it stands when
    each request is sent.
  - New: Ur knows the session's directory, its OS, and today's date, that it is
    an Ox session, which one, and how to reach the other sessions, follows Ox's
    style, and follows the `AGENTS.md` in that directory as it stands when each
    request is sent.

## Documentation updates

- `docs/architecture.md`: the runtime diagram gains `ur -- "MCP stdio" -->
  oxmcp`; *The Ur agent* says `ur` starts the forwarder `oxd` names, offers
  its tools from `tools/list` beside `shell` without approval, matches answers
  by request ID, drops an answer whose call its turn no longer waits on, and
  ends the forwarder when it stops.
- `docs/protocol.md` (*MCP*, *Tools*): `ox-mcp` serves Claude's and Ur's MCP
  clients; Ur runs every tool without an approval prompt in both modes.
