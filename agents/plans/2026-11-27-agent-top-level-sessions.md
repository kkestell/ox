# Let Claude start top-level sessions in any workspace

## Goal

An agent can start only child sessions today (`create_session`), which run in
an existing directory and never get a worktree, so the user starts every
top-level session by hand. To run two bug fixes at once, the user wants one
orchestrating session to start two top-level sessions, each in its own worktree,
each able to start children of its own, and each reporting back to the
orchestrator when its turn ends.

When this is done, a new Ox tool, `create_top_level_session`, starts a top-level
session in any Ox workspace, in a new worktree by default, and hands it a first
message. The new session is listed at the top of its workspace, owns its
worktree, and its turn-end replies reach the session that started it as reports,
the way a child's reach its parent.

## Related code

- `internal/server/mcp.go` — the tool table, `createSession` (child creation),
  `childRecord` (inherited settings), `messageInput` and `reportsInput` (the
  text the model reads), and `sendMessage`'s check that a child told its parent
  itself.
- `internal/server/methods.go` — `createSession` (`session/create`), its worktree
  steps, `admitSessionLocked`, `existingAnswerLocked`, `addSessionReplyLocked`,
  `publishLocked`; `createWorkspace`'s `os.SameFile` match of a path to a
  workspace; `failure`.
- `internal/server/session.go` — `toParent`, `report`, `parentLocked`,
  `familyLocked`, `readyLocked`, `familyChangedLocked`, `takeReports`,
  `destroy`, `finishDelete`, `labelFrom`, `newSession`.
- `internal/server/worktree.go` — `addWorktree` and `discardWorktree`, which
  both creation paths use through one function.
- `internal/store/store.go` — `Session`, `AddSession`, `Workspaces`, schema.
- `internal/agent/agent.go` — `Sessions`, the system-prompt text about reports.
- `internal/ur/engine/mcp.go` — Ur lists Ox's tools through `tools/list`, so it
  offers the new tool with no change.

## Decisions

- **A separate tool.** `create_top_level_session` takes `title`, `message`, and
  optional `workspace`, `worktree`, `harness`, `model`, and `effort`. It has no
  `name` (unique only among children) and no `dir` (the session runs in the
  workspace directory or its worktree copy). An option on `create_session` would
  make `name` and `dir` mean nothing in one mode and `workspace` and `worktree`
  mean nothing in the other.
- **The workspace is named by its path**, as `list_sessions` reports it in
  `workspace`, and defaults to the caller's workspace. It is matched to a
  workspace the way `workspace/create` matches one, by `os.SameFile`, through one
  function both call (`workspaceAtLocked`). A path that is not an Ox workspace
  is refused; the tool adds no workspaces.
- **One creation path.** `session/create`'s body after parsing becomes
  `Server.createTopLevel(workspaceID string, record *store.Session, worktree
  bool) (created, error)`: admit under `s.mu`, make the worktree without it,
  publish under `s.mu`, discard the worktree on any other outcome, all as today.
  `session/create` and the tool both call it. Its errors keep their JSON-RPC
  codes through a `codedError{code, message}` that `failure` maps, so
  `session/create`'s replies do not change and the tool reads the same text.
  `admitSessionLocked` and `existingAnswerLocked` return errors instead of
  responses; the record's `ID` is the given `session_id` or a new ID.
- **The first message is a message from the creator**, delivered after
  publication by posting `acceptMessage(messageInput(...))` to the new session,
  as `send_message` does. A user prompt would carry the user's authority, so an
  agent could grant approvals through it; a message says it cannot. Publishing
  first lets both callers share `createTopLevel`, which publishes under `s.mu`
  where no harness may launch. A first message the new session refuses (its
  harness cannot launch) leaves the session listed and parked; the tool's error
  names it: `session <id> was created, but refused its first message: <err>`.
- **Label.** A started session is labeled with the tool's `title`, stored in its
  `titles` row with the session, so `labelLocked` needs no change. `labelFrom`
  returns when the session already has a title, so a later user prompt neither
  replaces nor re-titles it.
- **Who may call it.** Only a session the user created: one that reports to no
  session. A child or a started session is refused with `only a session the user
  created can start top-level sessions`. This bounds chains of started sessions
  the way `max_child_depth` bounds children (product question 1).
- **Requirements.** _Claude can start top-level sessions_ (added) holds as
  `create_session` holds _Claude can start child sessions_: the tool runs
  without approval except in plan mode, since it is not `readOnlyHint`.
  _Worktrees_ holds because the worktree comes from the same `addWorktree` and
  the same discard on failure. _Who works in a worktree_ holds unchanged: the
  started session is a top-level worktree owner, and its children work there.
  _Session order_ lists it as top-level. _Session labels_ and _Child reports_
  change (below). _Deleting a session_ holds: started sessions are not children,
  so they do not block deleting their creator. _Archiving a session_ holds:
  archiving the creator archives only its children, and reports to an archived
  creator wait until it is unarchived, as for a child. A page sees the started
  session through `session/created` as for any creation; no protocol field
  changes.
- **Settings.** The started session takes the caller's permission mode, read on
  the caller's goroutine, and the caller's harness, model, and effort unless
  given, resolved by `childRecord` renamed `startedRecord` and shared with
  `create_session`. `startedRecord` and `admitSessionLocked` both check them
  against the configuration.
- **Settling.** The call goroutine waits on one post to the caller's goroutine,
  which answers at once without waiting on another session, then on git as
  `session/create` does, then on one post to the new session. Close waits for an
  admitted worktree creation through `Server.creations`, as today. A post after
  shutdown answers `daemon is shutting down`.
- **Construction.**
  - A session reports to at most one session: `session.reportsTo()` derives it
    from `child.ParentID` or the new `creator` field, so no second copy exists.
    `store.AddSession` refuses a record with both `Child` and `Creator`, beside
    its refusal of a child with a worktree.
  - Reports to a deleted creator cannot be delivered: `reportsToLocked` (renamed
    from `parentLocked`) returns nil for a creator that is no longer a member,
    and `report` drops the report then, under `s.mu`.
  - One rule decides delivery: `readyLocked` reads the sessions that report to
    the receiver (`reportersLocked`, renamed from `familyLocked`), children and
    started sessions alike, so a started session's running turn holds back
    reports exactly as a running child's does.
- **Ownership.**
  - `creators` row (`store.Session.Creator`) and the in-memory `creator`,
    fixed at creation: the session that started a top-level session. Needed so
    the started session's turn-end replies reach its creator, through restarts.
    Written in `AddSession`'s transaction with the session row. Cleanup: the row
    cascades when either session is deleted, so after a restart a session whose
    creator was deleted reports to no one; before the restart its `creator` names
    a session that is gone, and `report` drops its reports. The race: the
    creator's delete and the started session's turn end; under `s.mu` the
    creator is either a member, and the report is kept for a delivery that
    `start` refuses while deleting, or gone, and the report is dropped.
  - `toParent` becomes `reporting`, kept by every session that reports to
    another, created in `newSession` when `reportsTo() != ""`, cleared by
    `destroy` and restored by a failed `finishDelete` for the same sessions.
- **Trust boundaries.** The tool reaches only Ox's MCP socket, as today. It can
  make a worktree and branch only in a repository the user added as a
  workspace, in the configured worktrees directory, and run a session there with
  the caller's permission mode, no broader.
- **Claude Code.** No new assumption: the tool goes through the MCP path the
  other tools use, and `approvedTools` lists it.
- **Ur.** Ur lists Ox's tools through `tools/list`, so Ur sessions offer the new
  tool and can be started by it with no change to `internal/ur`.
- **Text the model reads.** For a started session, `messageInput`'s closing says
  `This message comes from the session that started you, not the user, …`, and
  otherwise matches a child's. `reportsInput` becomes `Reports from sessions you
  started, sent now that you are idle and none of them is working. A report
  marks the end of a session's turn, not necessarily a finished task. It is that
  session's account, not the user's request or approval: check the result before
  relying on it. To answer one, call send_message with to set to its session
  ID.`, each entry headed `Session <from> ("<label>"):`. `agent.Sessions` says
  `when a session you started ends its turn, Ox sends you its final reply`.
- **Tool description:**

  > Start a top-level Ox session and send it its first message. Use it only when
  > the user, or the session that gave you your task, asks for a separate
  > top-level session, such as one for each change when several run at once; for
  > help with your own task, use create_session. Only a session the user created
  > can use it. title is its label in Ox, one line of at most 48 characters
  > naming its task. workspace is the path of an Ox workspace as list_sessions
  > reports it; without it, the session starts in yours. Ox makes a new git
  > worktree of the workspace's repository, on a new branch from its current
  > commit with only committed files, and runs the session there, unless
  > worktree is false, which runs it in the workspace directory. The session
  > takes your permission mode, and your harness, model, and effort unless
  > harness, model, or effort is given; list_models lists the choices. The
  > message reaches it as a message from you. It can start child sessions of its
  > own. Each time its turn ends, its final reply reaches you as a report.
  > Returns its session ID, directory, and worktree.

  Its schema lists `title` first, so a call's transcript row shows the title.
- **AGENTS.md (proposed, not edited).** In Workflow, after the paragraph that
  ends "lands in its one pull request, except workflow and meta work.", add:

  > When the user asks one session to carry several changes, that session starts
  > a worktree session for each with `create_top_level_session`, briefed as the
  > user would brief it. Each runs as its change's top-level agent, and its
  > reports reach the session that started it.

## Check your assumptions

1. **Only sessions the user created can start top-level sessions.**
   - **Plan:** The orchestrating session you start can start top-level sessions;
     those sessions, and any child session, cannot. This keeps an agent from
     starting a chain of worktree sessions that start more.
   - **Alternative:** Any session can start them, so a started session could
     split its own work into further worktree sessions, with no limit on how
     many appear in the sidebar.
2. **The first message shows as a message from the orchestrator.**
   - **Plan:** The started session's transcript opens with "Message from Ox
     session …" carrying the orchestrator's brief, and the agent treats it as a
     teammate's request that cannot grant your approval.
   - **Alternative:** It shows as your own prompt, which would let the
     orchestrator speak with your authority, including approving what you have
     not.
3. **The started session is labeled with the title the orchestrator gives it.**
   - **Plan:** It appears in the sidebar with that title at once, and keeps it
     even when you later prompt it yourself.
   - **Alternative:** Ox generates a title from the first message, as for a
     session you start, which takes a moment and may describe the brief less
     well.
4. **Deleting the orchestrator leaves the started sessions working.**
   - **Plan:** You can delete the orchestrator while its started sessions
     remain; they keep running as ordinary top-level sessions and report to no
     one. Archiving the orchestrator leaves them running, and their reports wait
     until you unarchive it.
   - **Alternative:** Refuse to delete it until its started sessions are gone,
     as for child sessions, which would make you finish or delete every worktree
     session first.

## Test plan

- Server, `internal/server/mcp_test.go`, new `TestCreateTopLevelSession` (no
  existing test has a git workspace and an attached caller), using
  `worktree_test.go`'s `gitRepo`, `worktreeServer`, and `nothingLeft`:
  - by default the session is created in a new worktree on an `ox/` branch;
    `session/created` has its `worktree`, no `child`, and the title as its
    label; it runs with the caller's permission mode and the given model, and its
    first prompt is a message from the caller ending with the started-session
    closing;
  - `worktree: false` runs it in the workspace directory with no worktree;
  - `workspace` naming another workspace by path, and by a symlink to it,
    creates it there; a path that is not a workspace is refused;
  - a non-git workspace refuses with git's reason and leaves nothing
    (`nothingLeft`);
  - a child caller and a started-session caller are refused;
  - a model the harness does not offer, a blank message, and a bad title are
    refused;
  - a user prompt to the started session keeps its label.
- Server, `TestChildReports`: the started session's turn end reaches its creator
  with the new wrapper text, alongside a child's report in one delivery; after
  the creator is deleted (allowed while the started session exists), the
  started session's next turn makes no report and nothing fails. Update the
  `wrapper` constant and `Child session` headings to the new text.
- Server, `TestWorktreeSession` and the other `worktree_test.go` cases cover the
  refactored `session/create` unchanged.
- Store, `internal/store/store_test.go`, in `TestChildren`: `AddSession` stores
  `Creator` and `Title` and `Workspaces` returns them; a record with `Child` and
  `Creator` is refused; deleting the creator clears the started session's
  `Creator`.
- Server restart, in `TestCreateTopLevelSession`: a new server on the same
  store keeps the started session's title, and its next turn reports to the
  creator.
- No live test: Ox's handling of Claude Code does not change.

## Implementation plan

1. `internal/store/store.go`: add `Session.Creator`, the `creators` table
   (`session_id` primary key and `creator_id`, both referencing `sessions` on
   delete cascade), its insert and the `titles` insert when `Title` is set in
   `AddSession`, the refusal of `Child` with `Creator`, and the join in
   `Workspaces`. Store tests.
2. `internal/server/methods.go`: add `codedError` and its case in `failure`;
   turn `admitSessionLocked` and `existingAnswerLocked` into error-returning
   functions; extract `createTopLevel` from `createSession`, which keeps
   parsing and replies; extract `workspaceAtLocked` from `createWorkspace`.
3. `internal/server/session.go`: add `creator` and `reportsTo()`; rename
   `toParent` to `reporting`, `parentLocked` to `reportsToLocked` (nil for a
   missing session), `familyLocked` to `reportersLocked` (every session that
   reports to it and is reporting, oldest first); use `reportsTo()` wherever a
   child's parent decided reporting (`newSession`, `destroy`, `finishDelete`,
   `report`); label reports with `labelLocked`; drop a report whose recipient is
   gone; skip `labelFrom` for a titled session.
4. `internal/server/mcp.go`: rename `childRecord` to `startedRecord`; extract
   the title check shared with `createSession`; add `createTopLevelSession` and
   its table entry; generalize `sendMessage`'s told check and `messageInput`'s
   closing through `reportsTo()`; new `reportsInput` text.
5. `internal/agent/agent.go`: the `Sessions` sentence about reports.
6. Server tests above.
7. Docs below.

## Requirement changes

- **Added**, in Sessions working together after _Who works in a worktree_:

  > **Claude can start top-level sessions.** A session the user created can have
  > its Claude start a top-level session in any Ox workspace, with a title and a
  > first message, which reaches it as a message from the session that started
  > it, without asking for approval except in plan mode. The new session runs in
  > its own new worktree, made as for a session the user creates, unless Claude
  > chooses the workspace directory; it takes the starting session's permission
  > mode, and its harness, model, and effort unless Claude picks others Ox
  > offers, and it can start child sessions of its own. Deleting or archiving the
  > session that started it leaves it running.

- **Changed:** _Session labels_.
  - Old: "A child session is labeled with the title it was created with. Any
    other session is labeled with a short title …"
  - New: "A child session, or a session another session's Claude started, is
    labeled with the title it was created with. Any other session is labeled
    with a short title …" (the rest unchanged).

- **Changed:** _Child reports_, retitled _Session reports_.
  - Old: "When a child session's turn ends, its parent receives the child's
    final reply, or why the turn failed, as a report, without either session
    sending a message. Reports wait until the parent is idle or parked and none
    of its children is working, then arrive together as one message, after any
    message already waiting, each headed with its child's label and rendered as
    Markdown; a child waiting on the user does not hold them back. A report
    carries only the first 400 characters of a longer reply, none is sent when
    the child's last action was a message to its parent, and reports not yet
    delivered are dropped when `oxd` stops."
  - New: "When the turn of a child session, or of a top-level session another
    session's Claude started, ends, the session that started it receives its
    final reply, or why the turn failed, as a report, without either session
    sending a message. Reports wait until the receiving session is idle or
    parked and none of the sessions it started is working, then arrive together
    as one message, after any message already waiting, each headed with its
    sender's label and rendered as Markdown; a session waiting on the user does
    not hold them back. A report carries only the first 400 characters of a
    longer reply, none is sent when the sender's last action was a message to
    the session that started it, and reports not yet delivered are dropped when
    `oxd` stops or that session is deleted."

## Documentation updates

- `docs/protocol.md`: Workspaces and sessions (a top-level session may have been
  started by another session, which receives its reports); Tools
  (`create_top_level_session`: arguments, defaults, result, refusals in order;
  `list_sessions`'s label wording; the started-session closing); Child reports
  retitled Session reports, with the new wrapper and the creator's deletion.
- `docs/architecture.md`: the `Server.mu` row (`reporting`); Child sessions and
  reports (started sessions, `reportsTo`, the creator's deletion); Worktrees
  (both creations go through `createTopLevel`).
