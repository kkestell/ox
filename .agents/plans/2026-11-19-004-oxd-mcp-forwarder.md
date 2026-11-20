# `oxd` serves MCP to a forwarder

## Goal

Task 1 of Phase 1 in `.agents/scratch/agent-tools.md`. Ship `ox-mcp`, a stdio
forwarder that connects a harness's MCP client to `oxd`'s Unix socket on
behalf of one session, and have `oxd` serve `initialize`, `ping`,
`tools/list`, and `tools/call` on that connection, with `list_models` as the
only tool. `list_models` takes no arguments and returns the catalog
`harness/list` serves. `oxd` refuses every MCP request from a session it does
not have. The forwarder's connection receives no notifications, its stdout
carries only `oxd`'s answers to MCP requests, and it exits with a message on
stderr when it cannot reach the socket. No harness starts the forwarder yet.

## Related code

- `internal/server/server.go` — `serveConn` registers every connection in
  `s.conns` at accept, so `broadcast` reaches it, then dispatches each line on
  its own goroutine. The forwarder's connection has to leave `s.conns`.
- `internal/server/conn.go` — the per-connection queue. FIFO, so everything
  queued before a point stays ahead of everything queued after it.
- `internal/server/methods.go` — `handle` dispatch, `harness/list` answering
  `s.config`, `sessionNotFound` (`-32001`), `shuttingDown`, `invalidParams`.
- `internal/server/session.go` — `session.start` calls the harness's `Start`
  on the session goroutine; `claude.Start` blocks there for up to a minute
  waiting on Claude's `initialize` (`internal/claude/claude.go:108`).
- `internal/jsonrpc/jsonrpc.go` — `Parse`, `Success`, `Failure`.
- `cmd/ox/main.go:81` — the relay's `/events` dials the socket and sends
  nothing, so a connection cannot wait for its first line to be registered.
- `cmd/oxd/main_test.go` — the pattern of re-executing the test binary as the
  command under test (`OXD_TEST_MAIN`), and of short socket paths from
  `os.MkdirTemp("", …)` on macOS.
- `Makefile`, `cmd/ox/web/e2e/global-setup.ts` — where `oxd` is built.

## Decisions

- **Identifying the session: `mcp/attach`.** The forwarder's first line on
  the socket is `{"jsonrpc":"2.0","id":"attach","method":"mcp/attach","params":{"session_id":"…"}}`.
  `serveConn` handles a connection's first line itself when it is an
  `mcp/attach` request with an ID: with a nonempty `session_id` it removes the
  connection from `s.conns` under `connsMu`, then queues the result `{}`; with
  a missing or empty `session_id` it answers `-32602` `session_id is required`
  and the connection stays ordinary. Every later line of an attached
  connection goes to `handleMCP` with that session ID; an `mcp/attach` on any
  later line, or on a page connection after its first line, reaches `handle`
  and gets `-32601`. Attach does not check that the session exists: every MCP
  request checks it, which also covers a session destroyed while its
  forwarder runs.
- **No notifications.** Because `broadcast` queues while holding `connsMu`,
  and the attach result is queued after the removal under that lock, every
  notification the connection ever gets is queued ahead of the attach result.
  The forwarder discards every line before the attach result and copies only
  what follows to stdout. After attaching, the connection's queue holds only
  answers to the forwarder's own requests.
- **MCP dispatch (`internal/server/mcp.go`).** `handleMCP(reply, sessionID, line)`:
  `jsonrpc.Parse`; a line without an ID (`notifications/initialized`,
  `notifications/cancelled`) is ignored, as `handle` ignores one; then the
  session check under `s.mu` — `shuttingDown` when `s.closed`,
  `sessionNotFound` when `s.sessions[sessionID]` is nil — then the method:
  - `initialize`: requires a string `protocolVersion` (else `-32602`
    `protocolVersion is required`) and answers
    `{"protocolVersion":<the client's>,"capabilities":{"tools":{}},"serverInfo":{"name":"ox","version":"0"}}`.
    Echoing the client's version is correct because the three methods Ox
    serves have the same shape in every published MCP version.
  - `ping`: `{}`. MCP requires every receiver to answer it.
  - `tools/list`: `{"tools":[{"name":"list_models","description":"List the harnesses Ox offers, with their models, effort levels, and permission modes, and the defaults for a new session.","inputSchema":{"type":"object","properties":{}}}]}`.
    A `cursor` is ignored; there is one page.
  - `tools/call`: `name` other than `list_models` answers `-32602`
    `unknown tool "<name>"`. `arguments` are ignored. The result is
    `{"content":[{"type":"text","text":<json.Marshal(s.config)>}]}`, the same
    JSON `harness/list` returns.
  - anything else: `-32601` `Method not found`.
- **Answered on the connection goroutine, not the session's.** Everything
  these answers report is session membership, owned by `s.mu`, and
  `s.config`, which never changes, so no session state is read and nothing is
  posted to a session. This also keeps an MCP handshake from waiting on a
  session goroutine blocked in `claude.Start`, which Claude's own startup
  could otherwise deadlock against in Task 2. Tools that read or change a
  session's state post to its goroutine when a later phase adds them.
- **The forwarder (`cmd/ox-mcp/main.go`).** Reads `OX_SOCKET` and
  `OX_SESSION_ID` from its environment. Logs with prefix `ox-mcp: ` and no
  timestamp to stderr; stdout carries only lines from `oxd`.
  1. Dial `OX_SOCKET`; on failure `log.Fatalf("connect to oxd at %q: %v", socket, err)` (exit 1).
  2. Write the attach line.
  3. Read lines until the one whose `id` is `"attach"`, discarding the rest.
     An `error` in it: `log.Fatalf("attach to session %q: %s", sessionID, message)`.
     EOF first: `log.Fatal("oxd closed the connection before attaching")`.
  4. Copy stdin to the socket on a goroutine, then `CloseWrite` the socket,
     so `oxd` sees EOF and closes the connection when the harness closes
     stdin.
  5. Copy the rest of the socket, from the same buffered reader, to stdout.
     Return, exit 0, when the socket closes.
  It does not retry: stopping `oxd` stops every harness process.
- **Finding the binary.** Every build of `oxd` puts `ox-mcp` beside it:
  `make dev` builds it into `bin/`, and the e2e global setup builds it into
  its temporary bin directory. Task 2 resolves it in `cmd/oxd` beside
  `oxd`'s own executable and hands the path down, so a test can pass its own.
  The forwarder's own tests re-execute the test binary as `ox-mcp` when
  `OX_MCP_TEST_MAIN=1`, as `cmd/oxd`'s tests do, against an in-process
  `server.Server` serving a real Unix listener.
- **Requirements:** **Local access only** holds: there is no new listener,
  and an attached connection can do less than a page's. **Every page in
  sync** holds: page connections never send `mcp/attach` and keep every
  notification. No requirement changes; nothing a user sees changes until a
  harness starts the forwarder.
- **Settling:** the forwarder's wait for the attach result ends with that
  result or the socket closing; `oxd` answers attach inline in the read loop.
  Each MCP request is answered at once on its goroutine.
- **Ownership:** the attached session ID is a local of `serveConn`, set by
  its read loop before it dispatches any later line, so no lock is needed. The
  only shared change is the connection leaving `s.conns` under `connsMu`
  before the attach result is queued; the deferred `delete` on close is then a
  no-op. No timer, retry, or stored field.
- **Claude Code:** none in this task. Task 2 checks Claude's MCP handshake
  live; answering without the session goroutine keeps the startup ordering
  it finds from mattering here.
- **Trust boundaries:** the socket and its 0700 directory are unchanged. The
  session ID is not a credential; anything that can reach the socket can
  already drive `oxd`. The forwarder reads two environment variables and
  touches no file but the socket.

## Check your assumptions

None.

## Test plan

New `cmd/ox-mcp/main_test.go`, since no existing test has a real listener or
runs the forwarder. Its `TestMain` runs `main()` when `OX_MCP_TEST_MAIN=1`. A
helper starts `oxd` in-process: a store and the built-in configuration
(`config.Load` on a missing file) in `os.MkdirTemp("", "oxmcp")`,
`server.New(st, cfg, 0, nil)`, `net.Listen("unix", …)`, `go s.Serve(l)`, and
cleanup that closes the listener, the server, and the store. A small line
client on the socket plays the page. Sessions are created parked and never
started. Another helper runs the forwarder with `OX_SOCKET` and
`OX_SESSION_ID`, piping stdin and stdout and capturing stderr.

- **`TestForwarder`** — one forwarder on an existing session:
  - `initialize` returns the requested `protocolVersion`, a `tools`
    capability, and `serverInfo.name` `ox`; `notifications/initialized` gets
    no answer (the next line read is the next request's answer); `ping`
    returns `{}`.
  - No notifications: the page creates a second workspace and waits for its
    `workspace/created`; the forwarder's next stdout line is its `tools/list`
    answer, listing only `list_models` with an object `inputSchema`.
  - `tools/call` `list_models` returns one text item whose JSON equals the
    page's `harness/list` result.
  - `tools/call` of an unknown tool answers `-32602`; `harness/list` sent
    through the forwarder answers `-32601`.
  - The page sending `mcp/attach` after its first line gets `-32601`.
  - Closing the forwarder's stdin ends its stdout with no further lines,
    exit status 0, and empty stderr.
- **`TestForwarderRefusesUnknownSession`** — with `OX_SESSION_ID=missing`,
  `initialize` and `tools/call` both answer `-32001` `session not found`.
  With `OX_SESSION_ID` unset, the forwarder exits 1 with
  `session_id is required` on stderr and nothing on stdout.
- **`TestForwarderWithoutOxd`** — `OX_SOCKET` names a path nothing serves:
  exit 1, stderr names the path, stdout empty.
- **`TestForwarderDropsLinesBeforeAttach`** — a listener in the test plays
  `oxd`: reads the attach line, writes a `session/update` notification, then
  the attach result, then closes. The forwarder's stdout is empty and it
  exits 0. This is the only deterministic way to put a notification ahead of
  the attach result.

## Implementation plan

1. `internal/server/server.go`: in `serveConn`, give the first line to a new
   `s.attach(c, reply, line)` as described under Decisions, and dispatch the
   later lines of an attached connection to `go s.handleMCP(reply, sessionID, line)`
   instead of `go s.handle(reply, line)`.
2. `internal/server/mcp.go` (new): `attach`, `handleMCP`, and the
   `list_models` tool definition and result.
3. `cmd/ox-mcp/main.go` (new): the forwarder.
4. `cmd/ox-mcp/main_test.go` (new): the tests above.
5. `Makefile`: `dev` builds `./cmd/ox-mcp` with `./cmd/ox` and `./cmd/oxd`.
   `cmd/ox/web/e2e/global-setup.ts`: build `./cmd/ox-mcp` with the others.
6. Documentation updates below.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md`: in Connection and messages, every connection receives
  every notification except one attached with `mcp/attach`. A new MCP section
  after Connections: `mcp/attach` (first line only, `session_id`, result
  `{}`, the connection then receives no notifications and serves only MCP
  methods), the methods served and the session check, the `list_models`
  tool and its result, and `ox-mcp` with `OX_SOCKET` and `OX_SESSION_ID`.
- `docs/architecture.md`: `cmd/ox-mcp` in the package graph and table (the
  forwarder between a harness's MCP client and the socket); the `server` row
  owns MCP connections as well as client connections.
- `AGENTS.md`: the source map gains `bin/ox-mcp`, `cmd/ox-mcp/main.go`,
  `cmd/ox-mcp/main_test.go`, and `internal/server/mcp.go`.
