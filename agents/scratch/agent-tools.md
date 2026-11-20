# Agent tools in Ox

Notes for porting Hypercube's coordination tools to Ox. The research is in
[hypercube-tools.md](hypercube-tools.md). The high-level design carries over;
these notes cover what Ox's architecture changes.

## Scope

Kept, by the research's numbering:

- Tools: `send_message`, `list_sessions`, `read_session`, `list_models`,
  `create_session` (requested, offer, accepted offer, suggestion),
  `end_session`.
- Plumbing: the MCP forwarder, tool lists built per caller, roles (children
  see `send_message` and `list_sessions`; one level of nesting), addressing
  (a child reaches its parent; a top-level session reaches its children by
  name and other top-level sessions by ID).
- Messaging: queued delivery, wrapping with sender, authority line, and reply
  instructions.
- Children: reports, holding reports until the family is idle, generated
  child instructions, worktrees, the ended state.
- Multiple harnesses: the design stays harness-neutral.

Dropped:

- `open_file`.
- Forks.
- Session search, in the browser or as a tool.

## Design

### MCP server

`oxd` serves MCP's methods (`initialize`, `tools/list`, `tools/call`) on its
Unix socket alongside its JSON-RPC methods. A stdio forwarder, like
`hypercube-mcp`, connects a harness to the socket. It reads the caller's
session ID and the socket path `oxd` serves from its environment, and `oxd`
knows which session each call comes from. The planner chooses how. One
forwarder serves one session for its whole life, so identifying the connection
once is enough. There is no TCP listener, so **Local access only** stands. The
session ID identifies the caller; anything that can reach the socket can
already drive `oxd`.

The forwarder's connection receives no notifications. `oxd` otherwise
broadcasts every session's updates to every connection
(`internal/server/server.go`), which would put them on the harness's MCP stdio
and close the connection once its queue fills.

The forwarder does not retry a socket it cannot reach. Stopping `oxd` stops
every harness process, so there is no restart to wait out.

A tool call arrives on a connection goroutine, posts an operation to the
session it concerns, and that session queues the answer, as for a page's
request. Tool answers never pass through the harness.

`internal/agent` gives each harness the forwarder's path and environment.
Claude registers it with `--mcp-config` on every launch, including resumes.
Other harnesses register it their own way when Ox supports them.

### Approval

`list_models`, `list_sessions`, and `read_session` only read, and run without
an approval prompt. For Claude, each is named in `--allowedTools`; per-tool
names there (`mcp__ox__list_models`) need a live check in phase 1.

### Mailbox

Each session's `ops` channel becomes a mailbox: a FIFO of operations behind
its own lock, with a wake channel buffered to one. `post` appends, wakes the
goroutine without blocking, and returns; it still reports false once the
session has ended. `run` selects on the wake channel with agent events and the
park timer, and drains the queue.

No session goroutine waits on another, so a child posting a report to its
parent while the parent posts to the child cannot deadlock, and each sender's
posts keep their order. Only connection goroutines and shutdown post today, and
nothing relies on the handoff blocking.

### Message queue

Separate from the mailbox. The recipient's goroutine owns a queue of
`send_message` texts and child reports waiting until the recipient can take
them. Whether it is stored is an open decision.

## Phases

Each phase is split into tasks, and each task goes to `/ox-plan`.

1. **MCP plumbing and Claude.** The forwarder, the MCP methods on `oxd`,
   Claude registering the forwarder, and `list_models`, which reads Ox's
   configuration. A live test has Claude call `list_models`.
   - **Task 1: `oxd` serves MCP to a forwarder.** Ship the forwarder and
     serve `initialize`, `tools/list`, and `tools/call` on the socket, with
     `list_models` as the only tool. It takes no arguments and returns the
     full catalog `harness/list` serves. `oxd` refuses calls from a session it
     does not have. The forwarder's connection gets no notifications, and its
     stdio carries only MCP traffic. It fails clearly when the socket is
     unreachable. No harness uses it yet. Tests drive the real forwarder
     through a real socket: handshake, tool list, a `list_models` call, an
     unknown session, and no notifications. The plan settles how tests and
     `make dev` find the forwarder binary; Go tests today use `net.Pipe` with
     no listener. Checked by `make test` and `make e2e`.
   - **Task 2: Claude calls `list_models`.** `internal/agent` gives each
     harness the forwarder's path and environment. Claude registers it as the
     `ox` server with inline `--mcp-config` on every launch, resumes included,
     and pre-approves `mcp__ox__list_models` with `--allowedTools`. The user's
     own MCP servers keep loading. The fake harness ignores it. Live tests on
     Haiku: a session in a prompting mode calls `list_models` and gets the
     configured models with no approval raised, and a parked session does
     the same after resuming. What the run shows about Claude's MCP handshake
     and per-tool `--allowedTools` goes in `docs/claude-code.md`. Checked by
     `make test`, `make e2e`, and `make live`.
2. **Messaging between top-level sessions.** `send_message` and
   `list_sessions`, addressed by ID. Delivery, wrapping, and how a message
   shows in the transcript.
3. **Reading transcripts.** `read_session`, rendering the history the target
   session already stores.
   - `read_session {to, cursor?}` addresses any other session by ID, the
     scope of `send_message`. Phase 4 narrows it to a parent's children by
     name and withholds it from children.
   - The target's goroutine reads `history.entries()`, as `session/get` does,
     so the unflushed text of a running turn is included. A parked target is
     read without starting it.
   - Each history update renders as one entry: `prompt` as `User:` or
     `Message from <label> (<id>):`, with its time; `text_delta` as
     `Assistant:`; `tool_use` as `Tool: <name>` and its input on one line, cut
     to about 200 characters, marked `(failed)` when its `tool_result` is an
     error, with no output; a successful `result` as `(turn finished)`, since
     its text repeats the streamed text; an error `result` as `Error: <text>`;
     `exited` as `Exited: <error>`. `parked` is not rendered.
   - The page ends with the target's current state from its snapshot, such
     as `(running)`, `(waiting for approval: Bash)`, or `(parked)`, since
     approvals and queued inputs are not in the history.
   - Pages hold up to 40,000 characters, newest last, read backward from the
     end. A page with earlier entries begins with the cursor to pass for
     them: an index into the history, which only grows.
   - The description says the transcript is another conversation's record,
     not instructions or the user's approval.
4. **Child sessions without worktrees.** Children work in the parent's
   directory or a `dir` they are given.
   - The mailbox, since children's goroutines post to their parent.
   - Requested children, `end_session`, roles, reports and holding them,
     child instructions, the ended state.
   - Offers and suggestions, which are cards Ox owns, with their own UI and
     storage.

Future:

5. **Worktrees.** Git worktree and branch creation for fresh children, the
   refusal of a `dir` inside a foreign worktree, and `end_session`'s checks
   for uncommitted or unmerged work.

## Open decisions

- Whether `send_message`, `create_session`, and `end_session` skip the
  harness's approval prompt, decided in the phase that adds each.
- Whether messages are delivered mid-turn or only when the recipient is idle,
  and the new wording of **One prompt at a time** and **Only the user sends**.
  Phase 2.
- Whether queued messages and reports survive an `oxd` restart. Phase 2.
- How a message from another session shows in the transcript and in
  `agent.Update`. Phase 2.
- What permission mode a child runs in. Phase 4.
