# Publish an agent-started top-level session only once it holds its first message

## Goal

`create_top_level_session` publishes the new session (stored row,
`session/created` as `parked`, running goroutine) before it launches the harness
and hands it the first message. A launch or first `Send` that fails, such as Ur
without `OPENROUTER_API_KEY`, leaves an empty parked session in the sidebar and
a worktree and `ox/<name>` branch on disk. A caller that is archived or being
deleted is not refused at all.

When done, a top-level session started by Claude is stored, launched, and handed
its first message before anything is published, as `create_session` already
does for a child. Any failure, or a shutdown that arrives meanwhile, stops the
agent, deletes the stored row, and discards the worktree and branch, and the
tool returns the harness's own error. `session/create` and
`create_top_level_session` keep one creation path, `Server.createTopLevel`, and
no harness launches under `s.mu`. An archived or deleting caller is refused.

Resolves [#188](https://github.com/kkestell/ox/issues/188), audit finding
[F2](../audits/2026-11-27-main.md#f2-agent-started-top-level-sessions-publish-before-accepting-their-first-message).

## Related code

- `internal/server/methods.go:419` `createTopLevel`: admit under `s.mu`,
  optional worktree off the lock, publish under `s.mu`. The no-worktree branch
  stores and publishes in the admitting critical section.
- `internal/server/methods.go:516` `addSession`: stores, publishes, and starts
  `run` under `s.mu`. Its only callers are the two branches of
  `createTopLevel`.
- `internal/server/mcp.go:449` `addChild`: stores, `start`s, and
  `acceptMessage`s an unpublished child off the lock, publishes it under `s.mu`
  unless closed, and rolls back with `agent.Stop` and `deleteUnpublished`. The
  pattern this change shares.
- `internal/server/mcp.go:566` `createTopLevelSession`: publishes through
  `createTopLevel`, then looks the session up again and posts `acceptMessage`,
  formatting `session <id> was created, but refused its first message`.
- `internal/server/mcp.go:481` `checkChildLocked`: refuses an archived or
  deleting parent. `createTopLevelSession` has no such check, and its comment
  at `mcp.go:574` says one exists.
- `internal/server/session.go:620` `session.update`: broadcasts only for a
  member (`s.sessions[sess.id] == sess`), so an unpublished session's first
  updates are recorded in history and reach no client.
- `internal/server/server.go:160` `Close`: sets `closed`, ends members, then
  waits for `s.creations`.
- `internal/server/worktree.go:234` `discardWorktree`: removes the worktree
  with `--force` and deletes its `ox/` branch.

## Decisions

- **One creation path.** `createTopLevel` takes a fourth argument,
  `first func(*session) agent.Input`, the first message to hand the session, or
  nil for `session/create`. Its steps become, for every creation:
  1. Under `s.mu`, `admitSessionLocked`; on refusal or `existing`, return.
     Otherwise `s.creations.Add(1)` and release the lock. The no-worktree
     single-section branch goes, so both kinds of creation have one shape.
  2. Without the lock, with `worktree`, `addWorktree` as today.
  3. Without the lock, with `first`, `s.launch(w, record, first)`: store the
     row, build the session, `start` it, and `acceptMessage(first(sess))`. A
     failure has already rolled back the agent and row; discard the worktree
     and return the error.
  4. Under `s.mu`, refuse on `closed` or a removed workspace, answer `existing`
     on a same-ID session, as today. Otherwise, for a session not launched in
     step 3, store the row and build the session here, then `publishLocked`,
     release, and `go sess.run()`.
  5. On any refusal after step 3, `discardUnpublished(sess)`, then discard the
     worktree.

  The page's session stores its row in step 4 so that a same-ID retry racing
  the first creation answers `existing` at publication
  (`TestWorktreeCreationsWithOneID`). A launched session stores its row in step
  3 because its agent records history, whose `events` rows reference the
  session row. Its ID is a fresh `newID()`, so step 4's `existing` check cannot
  match it: a page creation reusing the ID would fail at its own store write.
- **Shared launch and rollback with `addChild`.** Two helpers in `mcp.go`
  replace the open-coded steps in `addChild`:
  - `launch(w, record, first) (*session, error)` sets `record.CreatedAt`,
    `store.AddSession`, `newSession`, `start`, `acceptMessage`. A failure calls
    `discardUnpublished` and returns the error.
  - `discardUnpublished(sess)` replaces `deleteUnpublished`: if `sess.agent` is
    set, `Stop` it and wait on `Reaped()`, then `store.DeleteSession`. Waiting
    for the process lets the worktree be discarded after the harness has
    exited from it, as `destroy` waits before `removeWorktree`.

  `addChild` becomes: checks under `s.mu`, `launch`, then under `s.mu` publish
  or, when closed, `discardUnpublished` and `errShuttingDown`. Rollback of an
  unpublished session stays local: it never goes through `destroy`,
  `finishDelete`, or `reporting`, because no client, family member, or
  recipient has seen the session.
- **The tool's result.** `createTopLevelSession` returns `createTopLevel`'s
  error as it is, so a harness that cannot launch answers with the harness's
  reason, such as `OPENROUTER_API_KEY is not set`, as `create_session` does.
  The `was created, but refused its first message` error and the second
  `s.sessions` lookup go. The `errShuttingDown` mismatch from that lookup goes
  with it.
- **Caller check.** A new `checkStarterLocked(caller) error` returns
  `errArchived` for an archived caller and `errDeleting` for one being deleted.
  `checkChildLocked` calls it in place of its own two checks, so the rule lives
  in one place. `createTopLevelSession`'s post to the caller's goroutine takes
  `s.mu`, calls `checkStarterLocked`, and reads `caller.labelLocked()` for the
  first message in the same critical section, then runs `startedRecord`. The
  check reads `deleting` on the caller's goroutine under `s.mu`, which is
  correct both before #187 (where `run` owns `deleting`) and after it (where
  `Server.mu` guards it). It need not hold until publication: a creator
  archived or deleted after the check leaves the started session in place, as
  the requirement says.
- **Missing workspace.** With no `workspace` argument, a caller that is no
  longer a member (its delete finished) answers `session not found` instead of
  `workspace must be the path of an Ox workspace`. The comment at `mcp.go:574`
  says so.
- **Requirements.** *Claude can start top-level sessions*: a refused start now
  leaves nothing behind, and the published session already holds its first
  message. *Worktrees*: creation that fails leaves no worktree or branch, now
  also when the harness fails. *Message delivery*: the first message to a
  session whose harness cannot launch is refused with the reason. The page
  first hears of the session in `session/created`, carrying its state with the
  message, as for a child; a reconnect lists it from `workspace/list` only once
  published, so no page can act on it before it exists. An `archive` or
  `destroy` on another page can no longer reach the session between creation
  and its first message.
- **Settling.** The one new wait is `discardUnpublished` on `Reaped()` after
  `Stop`, which ends when the process Ox killed exits. `Close` waits for every
  admitted creation through `s.creations`, now including no-worktree ones; a
  creation in step 3 during shutdown reaches step 4, sees `closed`, rolls back,
  and returns `daemon is shutting down`.
- **Construction.** A session is in `s.sessions` only once its agent holds its
  first message (for agent-started sessions) or once stored (for the page's).
  Publication is the one ownership transfer, done in step 4 of
  `createTopLevel` and in `addChild`; every other outcome runs
  `discardUnpublished` and `discardWorktree`.
- **Ownership.** No new state. `s.creations` now also counts no-worktree
  creations, so `Close` waits for a launched-but-unpublished session to be
  rolled back.
- **Compatibility with #187.** #187 makes `session.deleting` guarded by
  `Server.mu` (written only by the session's goroutine, always under the lock),
  sets `reporting` once in `newSession` and never replaces it, and changes
  `destroy`, `finishDelete`, `report`, `reportersLocked`, and `readyLocked` in
  `session.go` and only a comment in `checkChildLocked`. This plan's
  `checkStarterLocked` reads `caller.deleting` under `s.mu`, and its rollback
  touches none of those functions or `reporting`; an unpublished session's
  `reporting` from `newSession` is simply dropped with it. Which change lands
  first is unsettled. The overlap is `checkChildLocked`'s `deleting` check and
  its comment: the second to land moves the check into `checkStarterLocked`
  with #187's comment that `Server.mu` guards `deleting`.

## Check your assumptions

None.

## Test plan

- A harness that fails to launch, with and without a worktree, leaves no
  published session, stored row, worktree, or branch, and the tool returns the
  harness's error. Server; `TestCreateTopLevelSession`: add a `broken` harness
  to its configuration (models `m` and `n`, `Start` returning
  `no such harness binary`, as in `TestCreateSession`), and right after the
  plain-folder refusal, call `create_top_level_session` with
  `harness: "broken", model: "m"` once by default and once with
  `worktree: false`. Assert each is refused with `no such harness binary`,
  `nothingLeft(t, repo, worktrees)`, `c.created()` still holds one, and
  `st.Workspaces()` lists only the creator. Fails before the change, which
  publishes and keeps the worktree.
- A top-level session is not published while its launch or first `Send` is
  held, and a shutdown meanwhile refuses it and leaves no trace. Server;
  `TestChildStartHoldsUpNoOtherSession`: add a dimension over the tool,
  `create_session` and `create_top_level_session` with `worktree: false`, so
  each gate (start, send) runs for both, and rename the test
  `TestStartHoldsUpNoOtherSession`. The top-level case fails before the change,
  which broadcasts `session/created` before the gated start.
- An archived caller is refused. Server; new
  `TestCreateTopLevelSessionByArchivedCaller`: a session the user created in a
  plain workspace, idle after one prompt, is archived with `session/archive`;
  its `create_top_level_session` call with `worktree: false` through `attach`
  is refused with `session is archived`, and nothing is published. No existing
  test archives a caller and then has it call a tool; `TestCreateTopLevelSession`
  keeps its creator unarchived for its later steps. Fails before the change,
  which creates the session.
- The `create_session` refusals of an archived or deleting parent keep passing
  through `checkStarterLocked`, in the existing tests.

## Implementation plan

1. `internal/server/mcp.go`: add `checkStarterLocked`, and call it from
   `checkChildLocked`. Add `launch` and `discardUnpublished` (replacing
   `deleteUnpublished`), and rewrite `addChild` on them.
2. `internal/server/methods.go`: give `createTopLevel` the `first` argument and
   the five steps above. Drop `addSession`; step 4 stores and builds a page
   session inline. `createSession` passes nil. Update `createTopLevel`'s
   comment.
3. `internal/server/server.go`: reword the `creations` comment to count every
   top-level creation admitted under `mu` that has not returned, so `Close`
   waits for each to publish or roll back.
4. `internal/server/mcp.go` `createTopLevelSession`: in the caller post, take
   `s.mu`, run `checkStarterLocked`, read the caller's label, release, then
   `startedRecord`. Answer `session not found` when no `workspace` is given and
   the caller is no longer a member. Pass
   `func(sess *session) agent.Input { return messageInput(caller, label, sess, input.Message) }`
   to `createTopLevel`, return its error as is, and drop the second lookup and
   the partial-failure error. Fix the comments at `mcp.go:566` and `:574`.
5. Tests as in the test plan, in `internal/server/mcp_test.go`.
6. Documentation as below.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md`, `create_top_level_session` under Tools: the session is
  published once it accepts its first message, carrying its state then; add
  the refusal of a caller that is archived (`session is archived`) or being
  deleted (`session is being deleted`) after `session not found`; replace the
  last refusal with a first message the session refuses, with the harness's
  error, after which no session is created and a worktree made for it is
  discarded.
- `docs/architecture.md`, "Started sessions" under Child sessions and reports:
  the call goroutine creates the session through `createTopLevel` with its
  first message, which stores, launches, and hands it the message off the lock
  before publishing, rolling back on failure or shutdown. "Creation" under
  Worktrees: add the launch step between the worktree and publication, and say
  every admitted creation registers in `Server.creations`.
