# A child's launch and first message happen outside the membership lock

## Goal

`create_session` holds `Server.mu` on the parent's goroutine while it stores
the child, launches its harness, publishes it, and hands its agent the first
message. Every request that looks up a session (`session/get`,
`session/send`, every MCP request) takes the same lock, so a slow launch or
first `Send` stalls requests to unrelated sessions in every workspace. Ur's
first `Send` already does file I/O and a store write in that scope.

From
[Audit F2](../audits/2026-11-22-001-conversation-launch-and-layout.md#f2-a-childs-first-send-holds-the-daemons-membership-lock)
(`docs/todo.md`, "A slow child start stalls unrelated sessions").

When this change is done, `Server.mu` covers only the child's checks, its
publication, and the shutdown decision. The store write, harness launch, and
first `Send` run on the parent's goroutine without the lock. The guarantees of
the [child first-message plan](2026-11-21-007-child-owns-first-message.md)
still hold: a failed launch leaves no stored or published child, no request
reaches a child before it holds its first message, and a child whose creation
races shutdown is either refused and removed or ended by `Close` like any
member.

## Related code

- `internal/server/mcp.go:380-401`: `addChild` takes `Server.mu` for its whole
  body: name, shutdown, archive, and depth checks, then `addSession` with the
  first message.
- `internal/server/methods.go:361-397`: `addSession` stores the record, builds
  the session, launches it when given a first input (deleting the row on
  failure), adds it to membership, broadcasts `session/created`, hands the
  agent the input, and starts `run`. Its caller holds `s.mu`.
- `internal/server/methods.go:295-359`: `session/create` calls
  `addSession(w, record, nil)` under `s.mu`.
- `internal/server/mcp.go:48-60`: `handleMCP` looks up the session under
  `s.mu` and refuses every request, `initialize` included, when the session is
  not a member or the daemon is closed.
- `internal/server/session.go:155-161`: `acceptMessage` sets the input's
  `MessageID` and calls `Send`; `startForInput` returns at once when the agent
  is running.
- `internal/server/session.go:338-363`: `start`, which launches the harness and
  publishes nothing.
- `internal/server/server.go:140-154`: `Close` sets `closed` and snapshots the
  members under `s.mu`, then posts `end` to each and waits for its goroutine.
- `internal/claude/agent.go:372-395`, `internal/ur/ur.go:319-328`,
  `internal/fake/fake.go:256-270`: `Stop` on an agent holding no input
  publishes nothing.
- `internal/server/mcp_test.go:584-675`: `TestCreateSessionDuringShutdown`,
  whose `Close` waits on creation's lock.
- `internal/server/mcp_test.go:544-582`:
  `TestFailedLaunchKeepsItsErrorWhenDeleteFails`, which matches the log text
  `delete after failed launch`.
- `internal/server/mcp_test.go:22-30`: `snapshotReadAgent`, the wrapper
  pattern for gating one agent method.

## Decisions

- **Creation order on the parent's goroutine.** `addChild` runs, in order:
  1. Under `s.mu`: find the parent's workspace, check name uniqueness, archive,
     and depth, and build the message with `parent.labelLocked()`.
  2. Without the lock: store the record, build the session with `newSession`,
     and launch it with `sess.start()`. A launch error deletes the row and
     returns the error.
  3. Under `s.mu`: if `s.closed`, release the lock, stop the agent with
     `sess.agent.Stop()`, delete the row, and return `daemon is shutting
     down`. Otherwise add the session to membership and broadcast
     `session/created`.
  4. Without the lock: `sess.acceptMessage(message)`, then `go sess.run()`.
     The `acceptMessage` error is returned with the published child, as now.

  The checks in step 1 cannot go stale before step 3: a parent's children are
  created only on its goroutine, which is running this; its archived flag
  changes only by an archive running on or holding its goroutine; its depth is
  fixed. Only `closed` can change in between, so it is checked once, at
  publication, where the shutdown decision is made.
- **Disposal of an unpublished child.** Both refusals after the store write (a
  failed launch, shutdown at publication) delete the row, log a failed delete
  as `session <id>: delete unpublished child: <err>`, and return their own
  error. Shutdown calls `sess.agent.Stop()` directly, not `sess.stop()`, which
  would publish `parked` for a session no page knows. `Stop` on an agent with
  no input publishes nothing in any harness, and its process exits in the
  background, as every stopped agent's does.
- **Shutdown racing creation.** If `Close` takes the lock before step 3, the
  child is refused and removed, and `Close` waits for it: `Close` posts `end`
  to the parent, whose goroutine is running the creation, so the row is deleted
  before `Close` returns. If step 3 publishes first, the child is in `Close`'s
  snapshot; `Close`'s post to it waits until `run` starts after the first
  `Send`, then ends it like any member.
- **No request reaches a child before its first message.** Unchanged in kind:
  the child is reachable only through `post`, which waits on the unbuffered
  `ops` channel until `run` starts in step 4. Between steps 3 and 4 a request to
  the child waits rather than fails; a request to any other session does not
  wait. An archive of the parent runs on the parent's goroutine, after the
  creation.
- **MCP handshake needs no membership.** With the launch before publication, a
  child's forwarder could send `initialize` before step 3. `handleMCP` answers
  `initialize`, `ping`, and `tools/list` without looking up the session, since
  none of them reads it, and looks up the session and the shutdown flag only
  for `tools/call`. Claude calls a tool only within a turn, and the child's
  first turn starts from the input handed over in step 4, after publication, so
  every `tools/call` from a child finds it a member.
- **Top-level sessions.** `session/create` keeps its single critical section,
  since it launches nothing. `addSession` becomes
  `addSession(w, record) (*session, error)`: store, build, publish, start
  `run`. Publication (membership append and the `session/created` broadcast)
  moves into `publishLocked(w, sess)`, which both paths call, so the broadcast
  shape stays in one place.
- **Requirements.**
  - **Independent sessions:** a child's launch and first message hold up only
    requests to the parent, whose own tool call is creating it, and to the child
    itself.
  - **Claude can start child sessions:** the call still answers with the child
    holding its message, or with a refusal that leaves no child stored or
    published. A shutdown during creation now refuses with `daemon is shutting
    down` when it wins the race to publication.
  - **Stopping oxd:** a child launched while `Close` runs is either a member
    that `Close` ends or an agent its creator stops before `Close` returns.
  - **Every page in sync:** each page sees `session/created` before any update
    of the child, and never sees a child that a failed launch or shutdown
    removes.
- **Settling.** No new waits. A request posted to the child waits for its first
  `Send`, which ends as it does today.
- **Construction.** Invariants kept by ownership: an unpublished child is
  reachable only from the parent's goroutine; a published child's goroutine
  starts only after its agent holds the first message; a `tools/call` can only
  come from a session that is a member. The shutdown decision is made in one
  place, at publication.
- **Ownership.** No new state, timer, retry, lock, or stored field. `Server.mu`
  is held for less.
- **Claude Code.** The forwarder may connect any time after launch:
  [stdin protocol](../../docs/harnesses/claude-code.md#2-the-stdin-protocol)
  records that the `initialize` response does not wait for MCP servers and that
  the first turn does. A tool call needs a turn, which needs an input.
- **Trust boundaries.** `initialize`, `ping`, and `tools/list` on an attached
  connection answer for any session ID. They return only the fixed server info
  and tool list, and the socket is reachable only by the user.

## Check your assumptions

None.

## Test plan

- **A slow child start holds up no other session; shutdown during it**
  (server, `TestCreateSessionDuringShutdown` rewritten and renamed
  `TestChildStartHoldsUpNoOtherSession`). Two cases in a table, each with a
  fresh server and store:
  - Setup: two workspaces; the parent in one, an unrelated top-level session in
    the other, both parked. The harness's `Start` wraps `fake.Start`: for the
    gated stage, either `Start` itself signals and waits, or it returns a
    `sendGateAgent` (embedding `agent.Agent`, like `snapshotReadAgent`) whose
    `Send` signals and waits. The parent is never prompted, so the only start
    is the child's.
  - Both cases: call `create_session`, wait for the gate's signal, and assert
    that `session/get` of the unrelated session answers. This fails on the
    current code, which holds `s.mu` there. Then start `Close` in a goroutine
    and wait until `session/get` of the unrelated session answers `daemon is
    shutting down`, which shows `Close` has taken its snapshot.
  - Gated at `Start`: sync with `harness/list` and assert no `session/created`
    names the child before the gate is released. Release. Assert the call is
    refused with `daemon is shutting down` and no `session/created` names the
    child. After `Close` returns, reopen the store with `New` and assert
    `workspace/list` lists only the parent and the unrelated session.
  - Gated at `Send`: before closing, sync with `harness/list` and assert the
    first notification naming the child is `session/created`. Release. Assert
    the call returns the child. After `Close` returns, the child's last update
    is `parked`; reopen the store and assert the child is listed and its
    history holds the parent's message as a `prompt`.
- **The MCP handshake needs no session** (server, new
  `TestMCPHandshakeNeedsNoSession` in `mcp_test.go`). Attach for an ID that is
  not a session; assert `initialize`, `ping`, and `tools/list` succeed and
  `tools/call` returns `-32001` `session not found`. Fails on the current code,
  which refuses `initialize`. No existing test attaches without a session.
- **Disposal log** (`TestFailedLaunchKeepsItsErrorWhenDeleteFails`): match
  `delete unpublished child`.
- The launch-failure part of `TestCreateSession` stays as it is and still
  covers a failed launch leaving no trace.

## Implementation plan

1. `internal/server/methods.go`:
   - Extract `publishLocked(w *workspace, sess *session)`: append to
     `w.sessions` and `s.sessions`, and broadcast `session/created`. The caller
     holds `s.mu`.
   - Reduce `addSession` to `addSession(w, record) (*session, error)`: store the
     record, `newSession`, `publishLocked`, `go sess.run()`. Update its comment
     and the `session/create` call.
2. `internal/server/mcp.go`:
   - Rewrite `addChild` to the four steps in Decisions. Keep its signature.
   - Update the `createSession` doc comment: the parent's goroutine checks
     under `Server.mu`, stores and launches the child without it, publishes it
     under `Server.mu` unless the daemon is shutting down, then hands it the
     first message and starts its goroutine.
   - In `handleMCP`, move the session lookup and the shutdown and not-found
     refusals into the `tools/call` case.
3. `internal/server/server.go`: add to the `Server.mu` comment that no harness
   is launched or handed input while it is held.
4. `internal/server/mcp_test.go`: the tests above.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`:
  - The child-session paragraph (lines 111-122): the parent's goroutine checks
    name uniqueness, archive, and depth under `Server.mu`; stores the child and
    launches its harness without it; under `Server.mu`, refuses if the daemon is
    shutting down, stopping the agent and deleting the row, and otherwise adds
    the child to membership; then hands its agent the first message and starts
    its goroutine. A request posted to the child waits until then.
  - The `Server.mu` paragraph (lines 78-82): add that no harness is launched or
    handed input while it is held.
- `docs/protocol.md`:
  - MCP methods: `initialize`, `ping`, and `tools/list` answer for any attached
    session; `tools/call` returns `-32001` when the session does not exist and
    `-32000` when the daemon is shutting down.
  - `create_session` refusals: the caller gone first stays `session not
    found` where it is; move `daemon is shutting down` after the launch error,
    since it is now checked at publication.
- `docs/todo.md`: remove the "A slow child start stalls unrelated sessions"
  entry.
