# A child holds its first message before it is published

## Goal

`create_session` publishes the child, starting its goroutine and broadcasting
`session/created`, and only afterwards posts the first message to it. A request
that reaches the child in that gap breaks it:

- An archive of the parked child makes the message fail with `session is
  archived`, and creation then deletes the child the page just archived.
- A shutdown ends the child's goroutine, so the post fails with `session not
  found`, nothing is rolled back, and after a restart an empty child holds the
  name.

From
[Deslop F3](../audits/2026-11-20-002-deslop.md#f3-create_session-publishes-the-child-before-its-first-message-has-an-owner)
(`docs/todo.md`).

When this change is done, the parent's goroutine builds the child, launches its
harness, publishes it, and hands its agent the first message before the child's
goroutine starts. No request can reach a child that does not yet hold its first
message. A harness that cannot be launched leaves nothing stored or published.

## Related code

- `internal/server/mcp.go:308-421`: `createSession`. It checks arguments on the
  call goroutine. It resolves, stores, and publishes the child on the parent's
  goroutine. It then posts `acceptMessage` to the child, and destroys the child
  on failure.
- `internal/server/methods.go:352-375`: `session/create` stores the record and
  calls `addSession`. `addSession` builds the session through `newSession`,
  which starts its goroutine, then adds it to membership and broadcasts
  `session/created`. The caller holds `Server.mu`.
- `internal/server/session.go:75-93`: `newSession` builds a session and starts
  `run` at once.
- `internal/server/session.go:155-161,195-203,315-340`: `acceptMessage`,
  `startForInput`, and `start`. `startForInput` takes `Server.mu` through
  `isArchived`, so it cannot run under the lock.
- `internal/server/server.go:50-77,139-154`: `New` builds stored sessions
  through `newSession`. `Close` sets `closed` under `Server.mu`, then ends every
  member.
- `internal/agent/agent.go`: `Harness.Start` returns at once, with an error only
  when the harness cannot be launched. `Send` publishes the input's updates
  through `OnUpdate`.
- `internal/server/mcp_test.go:410-423,466-528`: the launch-failure part of
  `TestCreateSession`, and `TestCreateSessionDuringShutdown` with its sleep and
  its `c.received("")` that matches no `session/created`.
- `internal/server/client_test.go:86-106`: `received` filters by session ID.

## Decisions

- **One owner from construction to the first message.** A session belongs to
  whoever builds it until its goroutine starts, and to its goroutine afterwards.
  `newSession` only builds the session, and whoever publishes it starts `run`.
  For a child, `addSession` does the following on the parent's goroutine under
  `Server.mu`, in order:
  1. Store the record.
  2. Build the session.
  3. Launch its harness with `start`.
  4. Add the session to membership and broadcast `session/created`.
  5. Hand the agent the first message.
  6. Start `go sess.run()`.

  An archive, destroy, shutdown, or message for the child can only post to its
  goroutine, which does not exist until the child holds its message. The parent
  answers the call from the same operation. The child's harness is launched
  before publication, and `session/created` precedes the message's updates. The
  design works at any depth, since it depends only on creation running on the
  caller's goroutine.
- **Launch before publishing; delete only an unpublished row.** `Start`
  publishes nothing, so launching it between the store write and publication
  shows nothing. A launch error deletes the stored row and returns the error.
  Nothing was broadcast, so pages see neither `session/created` nor
  `session/destroyed`. A failed delete is logged by session ID, and the call
  still returns the launch error. The store is not rolled back further, as
  **Database failures** allows. The row is written before the launch because an
  agent stopped after a failed store write would publish its last update for a
  session no page knows.
- **The launch happens under `Server.mu`.** Shutdown and publication must be
  atomic. Otherwise `Close` can set `closed` and snapshot the members between
  the check and the publication, and a child published afterwards is never
  ended. Holding the lock across `start` costs other requests one process launch
  (an exec and a pipe write for Claude, no I/O for a new Ur conversation or the
  fake). It also makes the child's MCP forwarder, whose `handleMCP` takes the
  lock, wait until the child is a member. Nothing posts to a session under the
  lock, so this rule still holds. `start` is called directly, not through
  `startForInput`, which would take the lock again. A new child of an unarchived
  parent is not archived.
- **`Send` failing after publication.** A just-started agent writes no input at
  once: Claude queues it until initialized, and the fake never fails. If `Send`
  does fail, the child stays published, its goroutine starts, and the call
  returns the error, as `send_message` returns a failed delivery. No extra
  rollback is built for it.
- **Top-level sessions use the same path.** `session/create` calls
  `addSession(w, record, nil)`. `addSession` now owns the store write for both
  callers, so neither repeats it. With no first message, steps 3 and 5 are
  skipped, and the session is published parked, as now. `New` starts `run` for
  each stored session it builds.
- **Requirements.**
  - **Claude can start child sessions:** a call answers with the child holding
    its message, or with a refusal that leaves no child. A shutdown during
    creation either refuses before storing or publishes the child, which `Close`
    then ends like any member. Its message is recorded once taken, or dropped
    with `prompt_cancelled` as any queued input is at shutdown. Either way the
    child survives a restart with what it received.
  - **Archiving a session**, **Archived sessions:** an archive that races
    creation finds the child busy with its message and returns `agent is busy`,
    or, for the parent, waits for creation on the parent's goroutine. It never
    succeeds and is then undone.
  - **Deleting a session:** only a destroy the user asks for deletes a published
    child.
  - **Every page in sync:** each page sees `session/created` before any update
    of the child, and never sees a child that a launch failure removes.
- **Settling.** The new work adds no waits. The call still waits only on the
  parent's goroutine, which runs `Start` (returns at once) and `Send` (no
  blocking I/O at start), and answers from that operation.
- **Construction.** Invariant: a published child has an agent holding its first
  message. It is enforced by ownership: the session is unreachable until
  `addSession` starts its goroutine, after the message is handed over. The
  `session not found` refusal for a child gone before its message, and the
  destroy after a failed start, are removed, because neither can happen.
- **Ownership.** No new state, timer, retry, lock, or stored field. The only
  change is that `Server.mu` is held across one harness launch during child
  creation.

## Check your assumptions

None.

## Test plan

- **Not published while launching** (server, `TestCreateSessionDuringShutdown`,
  rewritten).
  - Setup: the fake harness's `Start` signals `starting` and waits on `release`.
    The parent is never prompted, so the first `Start` is the child's.
  - Call `create_session` and wait for `starting`, then sync the page connection
    with `harness/list`, which answers without `Server.mu`.
  - Assert no `session/created`. This fails on the current code, which
    broadcasts it before the child's `Start`.
  - Start `Close` in a goroutine, which must wait for creation's lock, then
    close `release`.
  - Assert that the call returns the child, and that the page's first
    notification naming the child is `session/created`.
  - After `Close` returns, reopen the store with `New`. Assert that the child is
    listed and that its `session/get` history holds the parent's message as a
    `prompt`.
  - Remove the sleep and the old refusal scenario. A gate cannot order that call
    against `Close`'s end, and the new case covers creation racing shutdown.
- **A failed launch leaves no trace** (server, the launch-failure part of
  `TestCreateSession`). After the `no such harness binary` refusal, sync with
  `harness/list` and assert that no `session/created` names `doomed`. This
  replaces the `session/created`/`session/destroyed` expectation and fails on
  the current code. Creating `doomed` again on the working harness still
  succeeds.
- **Matching notifications by method** (`client_test.go`). Add
  `notifications(method string) []message`, which drains pending reads and
  returns the backlog's notifications with that method. Both tests above use it.

## Implementation plan

1. `internal/server/session.go`:
   - Make `newSession` build the session without starting `run`, and document it
     as building the session.
   - Add to the `session` comment that the session belongs to whoever builds it
     until `run` starts.
2. `internal/server/server.go`: in `New`, start `go sess.run()` for each stored
   session after building it.
3. `internal/server/methods.go`:
   - Change `addSession` to
     `addSession(w *workspace, record *store.Session, first *agent.Input) (*session, error)`.
     It runs the steps in Decisions: store the record, build the session, and,
     with `first`, call `start` (on error, delete the row, log a failed delete
     by session ID, and return the launch error). It then adds the session to
     membership, broadcasts `session/created`, and, with `first`, sets
     `MessageID` and calls `sess.agent.Send`. Last, it starts `go sess.run()`
     and returns any `Send` error with the session.
   - Update its comment.
   - Make `session/create` call it with `nil` and drop its own
     `store.AddSession`.
4. `internal/server/mcp.go`, `createSession`:
   - In the parent's operation, build `message` with `messageInput` before
     calling `addSession(w, record, &message)` under the lock, and respond from
     the operation with the result or the error.
   - Remove the `done` channel, the `created.post` step, and its destroy on
     failure.
   - Rewrite the doc comment to describe the single owner: the call goroutine
     checks; the parent's goroutine resolves, stores, launches, publishes, and
     hands over the first message.
5. `internal/server/client_test.go`, `internal/server/mcp_test.go`: the tests
   above.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, the child-session paragraph (lines 104-114). Under
  `Server.mu`, the parent's goroutine checks shutdown and name uniqueness,
  stores the child, launches its harness, adds it to membership, hands its agent
  the first message, and then starts its goroutine. A harness that cannot be
  launched leaves the child unstored and unpublished. Add the rule that a
  session belongs to whoever builds it until its goroutine starts.
- `docs/protocol.md`:
  - `create_session` refusals: "the caller or child gone first" becomes "the
    caller gone first: `session not found`".
  - The launch-failure bullet becomes "a harness that cannot be launched: the
    launch error; no child is created".
  - In the `session/created` row, "A session was created, parked." becomes "A
    session was created: a top-level session parked, a child started with its
    first message, whose updates follow."
- `docs/todo.md`: remove the F3 entry.
