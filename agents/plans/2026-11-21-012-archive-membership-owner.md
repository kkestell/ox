# Archive on the session's goroutine, unarchive under Server.mu

## Goal

[Deslop F4](../audits/2026-11-20-002-deslop.md#f4-archive-and-unarchive-act-outside-the-owner-of-session-membership)
(todo: "Archive and unarchive act on a stale session"). `archiveSession` looks a
session up under `Server.mu`, unlocks, and hands the pointer to `archive` or
`unarchive` on the request goroutine. `unarchive` then acts on that pointer
without checking it is still a member: after a delete and a recreate with the
same ID it stores `archived=false` for the replacement by ID while setting the
flag on the dead session, and it broadcasts `session/unarchived` after
`session/destroyed`. `archive` releases its held members and replies at once,
so the reply can reach the client before the members' `parked` updates.

When done:

- Unarchive is one critical section under `Server.mu`, the owner of membership
  and the archived flag: it looks the session up by ID, checks the parent,
  stores and sets the flags, broadcasts, and queues its response. No session
  pointer outlives the lock.
- Archive is an operation posted to the session's goroutine, like every other
  request that touches a session's goroutine-owned state. Its response is
  queued there after every member has stopped, so a successful reply follows
  every member's `parked` update.
- The two `docs/architecture.md` paragraphs that F4 and F22 name describe this.

## Related code

- `internal/server/methods.go:62-65,564-588` — dispatch and `archiveSession`,
  the shared handler with the out-of-lock lookup. `onSession` (`:96-106`) is
  the standard lookup-and-post.
- `internal/server/session.go:211-321` — `held`, `hold`, `archive`,
  `unarchive`, `setArchived`, `setArchivedLocked`.
- `internal/server/session.go:350-360` — `stop` and `parked`: `stop` calls
  `agent.Stop` and broadcasts `parked` synchronously on the goroutine.
- `internal/server/server.go:25-33,140-155,177-186` — `Server.mu`, `Close`,
  `children`.
- `internal/server/mcp_test.go:674-917` — `TestArchive`.
- `internal/server/methods_test.go:1272-1281` — `configuringAgent`, the wrapper
  `TestArchive` already uses.
- `docs/architecture.md:63-74,81-93` — the request-routing and archive
  paragraphs.
- `docs/protocol.md:404-436` — `session/archive` and `session/unarchive`.

## Decisions

- **Archive runs on the target's goroutine.** `archiveSession` uses
  `onSession`, which already answers `daemon is shutting down` and
  `session not found`. A destroyed session's goroutine has ended, so a stale
  lookup cannot reach a dead session's state: the post fails and the request
  gets `-32001`. The operation, `sess.archive()`:
  1. Walks the family breadth-first as today, reading each member's children
     under `Server.mu` and holding each child with `hold`. The target itself is
     not held; its own goroutine is running the operation, so it reads its own
     state with `sess.free()`.
  2. Refuses with `agent.ErrBusy` when any member is not free, otherwise calls
     `setArchived` for every member.
  3. On success, stops its own agent if it has one.
  4. Releases every held member with the outcome, then waits on each member's
     `done`.
  5. Returns, and the handler queues the response on the goroutine.

  The target stops before it releases the others, so the members' stops run
  after the target's own and the test's delayed stop (Test plan) separates
  them. The order among members' `parked` updates is otherwise unspecified.
- **Deadlock.** No session goroutine posts to another except archive, which
  posts only from an ancestor to its descendants. Two archives in one family
  therefore cannot wait on each other: an archive of a child runs on the
  child's goroutine, and an archive of its parent waits to hold the child until
  that operation ends. `Close` posting `end` to a member first makes that
  member's `hold` fail and skips it, and `setArchived` then refuses with
  `daemon is shutting down`, so the archive releases the rest unchanged.
- **Unarchive is one critical section.** `unarchiveLocked(sessionID string)`
  runs with `Server.mu` held. It returns `daemon is shutting down` when
  `closed`, `errSessionNotFound` when the ID has no member, and
  `parent session is archived` for a child of an archived parent. Otherwise it
  collects the session and every session beneath it and calls
  `setArchivedLocked`. The handler, `unarchiveSession`, takes the lock, calls
  it, and queues the response before unlocking. The response lands after the
  broadcasts it reflects and before any later membership or archive
  broadcast, as `createWorkspace` does. `reply` only queues on a connection,
  so it is safe under `Server.mu`.
- **Requirements.**
  - **Archiving a session:** a successful reply now means every member has
    stopped. The page already shows the archived state from `session/archived`
    and the stop from `parked`; neither its rendering nor its error handling
    changes.
  - **Archived sessions:** a stale unarchive can no longer clear a recreated
    session's stored flag, so `workspace/list` and the store agree, and the
    session stays archived through a restart.
  - **Every page in sync:** no `session/unarchived` follows a
    `session/destroyed` for the same session.
  - **Lifecycle.** A lost reply leaves the state the broadcasts already showed.
    A second page acting on the same session is ordered by the session's
    goroutine for archive and by `Server.mu` for unarchive. An unarchive racing
    an archive of the same family produces a valid end state in either order:
    members stay held until released, so nothing starts between the archive's
    flags and its stops.
- **Settling.** One new wait: the archive waits on each held member's `done`,
  which the member's held operation closes after it receives its release and,
  if released with true, stops its agent. `agent.Stop` returns at once; the
  process exits in the background, as parking does. Every member is released
  on every path, including refusal and shutdown, so the wait always ends. The
  existing waits on `hold` are unchanged.
- **Construction.**
  - Unarchive takes an ID, not a pointer, and resolves it inside the critical
    section that uses it, so no unarchive can act on a session that is not a
    member.
  - Archive reaches its target only through the target's goroutine, which no
    longer exists once the session is destroyed.
  - Each member's stop happens before the response because the response is
    queued only after every member's `done` is closed, and `stop` broadcasts
    `parked` before its held operation closes `done`.
- **Ownership.** No new state, timer, retry, lock, or stored field. `held`
  gains a `done` channel that lives only for one archive; the held operation
  closes it.

## Check your assumptions

None.

## Test plan

- **Archive replies after every member's `parked` (server, `TestArchive`).**
  In the step "archiving the parent archives and stops every session beneath
  it":
  - `c.forget` each of `a`, `k`, and `g`.
  - Send `session/archive` with `c.send`.
  - Match the response with a single `c.next` predicate that records each
    `parked` `session/update` it scans and returns true on the response.
  - Assert the response succeeded and that `parked` was recorded for all
    three before it.
  - The `session/archived` notifications stay in the backlog for the existing
    `notified` checks.

  To make the old order fail every time instead of by scheduling, `TestArchive`'s
  `start` wraps each agent in a `slowStop` type, declared beside the test, whose
  `Stop` sleeps 20 ms before delegating. The wrapper embeds the existing
  `configuringAgent`. With the target stopping before it releases the members,
  a missing wait queues the response while `k` and `g` are still sleeping. The
  test fails on the current code, which replies before any stop, and fails if
  the wait on `done` is removed.
- **Unarchive of a missing session (server, `TestArchive`).** After the final
  step destroys `g`, `session/unarchive` for `g` returns `-32001`
  `session not found`. This covers the lookup that moves into
  `unarchiveLocked`. The stale-pointer race itself has no window left to test:
  the pointer no longer exists outside the lock.
- The existing `TestArchive` steps cover busy refusals, background members,
  children, restarts, and unarchive's parent check through the rewritten
  paths.

## Implementation plan

1. `internal/server/session.go`:
   - Add `func (sess *session) free() bool` (idle, background, or parked) and
     use it in `hold`.
   - Give `held` a `done <-chan struct{}` that the held operation closes after
     its release is handled, stop included.
   - Replace `func (s *Server) archive(sess *session) error` with
     `func (sess *session) archive() error`, as in Decisions, and rewrite its
     comment.
   - Replace `unarchive` with `func (s *Server) unarchiveLocked(sessionID string) error`.
   - Keep `setArchived` and `setArchivedLocked`.
2. `internal/server/methods.go`:
   - Dispatch `session/archive` to an `archiveSession(reply, id, params)` that
     parses `session_id` and calls `onSession` with `sess.archive()`.
   - Dispatch `session/unarchive` to a new `unarchiveSession(reply, id, params)`
     that parses `session_id`, takes `s.mu`, and replies with the result of
     `unarchiveLocked` before unlocking.
   - Delete the shared handler's out-of-lock lookup.
3. `internal/server/mcp_test.go`: add `slowStop` and the two test cases above.
4. `docs/architecture.md` (Documentation updates).
5. `docs/protocol.md`, `docs/todo.md` (Documentation updates).

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, Sessions:
  - In the paragraph beginning "Each session runs on its own goroutine",
    state that every request that reads or changes a session's goroutine-owned
    state, archive included, is posted to that goroutine and queues its
    response there. Name the exceptions to "at once" by when they reply: a
    configuration from the event that finishes it, and an archive once every
    member has stopped. Unarchive changes only state `Server.mu` guards and is
    not posted to a goroutine.
  - In the `Server.mu` paragraph, add that unarchive looks its session up and
    queues its response under the lock.
  - In the paragraph beginning "An archived session has no agent":
    - The archive runs on the session's goroutine.
    - It holds every session beneath it, breadth-first, each with an operation
      that reports whether the session is idle, in the background, or parked
      and then waits.
    - With every member free, it stores and sets the flags, stops its own
      agent, releases the members, which stop theirs, and replies once each
      has stopped.
    - Unarchiving, in one critical section under `Server.mu`, looks the
      session up by ID, refuses a child whose parent is archived, clears the
      flags of the session and every session beneath it, and queues its
      response.
- `docs/protocol.md`, `session/archive`: each archived session is announced
  with `session/archived`, and its process stopped with `parked`, before the
  response.
- `docs/todo.md`: remove the entry "Archive and unarchive act on a stale
  session". The F22 entry stays, since F22 has other items; its architecture
  item is the one this change settles.
