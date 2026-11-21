# Nested child sessions

## Goal

A child session's Claude can start children of its own, down to a configured
maximum depth, `max_child_depth`, 2 by default: a top-level session's children
can have children, and those grandchildren cannot. Archive and unarchive act on
every session beneath the one chosen, and the sidebar shows the family as a tree
that indents each level further.

## Related code

- `internal/server/mcp.go` — `createSession` refuses any caller with a `child`;
  `addChild` checks name uniqueness, shutdown, and an archived parent under
  `Server.mu`; the `create_session` description limits the tool to the user's
  request.
- `internal/server/session.go` — `archive` gathers children only for a
  top-level session; `unarchive` gathers direct children only. `destroy`
  already refuses while any child exists.
- `internal/server/server.go` — `children(parent)` reads membership under
  `Server.mu`.
- `internal/config/config.go`, `internal/config/default.json` — the
  configuration `oxd` loads, checks, and serves through `harness/list` and
  `list_models`.
- `internal/store/store.go` — the `children` table keys a child to any session;
  it needs no change.
- `cmd/ox/web/src/Sidebar.tsx` — renders top-level sessions and one level of
  children; `archiveBlocked` checks the session and its direct children.
- `cmd/ox/web/src/style.css` — `nav li.child` guides assume one level and
  adjacent sibling rows.

## Decisions

- **Depth is derived, not stored.** A session's depth is the length of its
  `child.ParentID` chain through `s.sessions`, read under `Server.mu`. Every
  ancestor exists while its descendant does, since `destroy` refuses a session
  with children. `createSession` replaces the `caller.child != nil` check with
  `s.depthLocked(caller) >= s.config.MaxChildDepth`, at the same place in the
  refusal order, and refuses with `child sessions nest at most <n> deep`.
  Ancestry is fixed at creation, so the check needs no session goroutine.
- **Configuration.** `Config.MaxChildDepth int` with JSON name
  `max_child_depth`, required like `defaults`: `Load` refuses a value below 1
  with `max_child_depth must be at least 1`, which also covers a file that
  omits it. The built-in file sets 2. `harness/list` and `list_models` already
  marshal the whole `Config`, so both serve it unchanged.
- **Tool wording.** The `create_session` description's "Use it only when the
  user asks for another session or agent." becomes "Use it only when the user,
  or the session that gave you your task, asks for another session or agent."
  Ox's appended instructions already tell Claude to treat a message as a
  teammate's request within its own permissions, and `create_session` is an
  approved tool, so no system prompt change is needed.
- **Requirements.** "Claude can start child sessions": a child's call follows
  the same path as a top-level caller's, on the caller's goroutine, so
  shutdown, an archived caller, and a launch failure refuse as today. "Session
  order": the page renders from `workspace/list` and `session/created`, which
  already carry `child.parent_id`, so a reload, reconnect, or second page
  rebuilds the same tree. "Archiving a session": the page enables Archive only
  when the session and every session beneath it are settled, and `oxd`
  re-checks every member while holding it, so a stale page gets `-32002` and
  archives nothing. "Configuration checks": a missing or zero
  `max_child_depth` stops `oxd` at startup with the reason. "Deleting a
  session" holds unchanged: a middle session with children cannot be deleted.
- **Construction.**
  - A child of an archived session is archived, at every level. `archive`
    walks the family breadth-first: it holds each member, then reads that
    member's children under `Server.mu`, then holds them. Children are created
    only on their parent's goroutine, so the children read while their parent
    is held are all of them. `unarchive` refuses a session whose parent is
    archived, so an unarchived session's ancestors are all unarchived, and it
    clears the flags of the session and every session beneath it.
    `addChild` refuses an archived parent, as today.
  - Concurrent archives in one family cannot deadlock: every archive holds
    ancestors before descendants, so no two wait on each other in a cycle.
  - One source for the limit: `Config.MaxChildDepth`, checked once in
    `createSession`.
- **Ownership.** No new state, timer, lock, or stored field. Depth reads
  membership, which `Server.mu` guards.
- **Settling.** No new wait. An archive holds more members, each ended by the
  release that follows the busy check, as today.
- **Sidebar tree.** The sidebar stays a flat list of `<li>` rows, so existing
  selectors and row styles keep working. A memo flattens each workspace's
  shown sessions depth-first, oldest first at every level, into rows that carry
  their depth, whether they are the last shown child of their parent, and how
  many rows up their guide starts: at the parent's row for its first shown
  child, else at the previous shown sibling's row. The row sets `--depth` and
  `--rise` as inline custom properties and the class `last`. CSS indents each
  level by the step the first level uses now (1.25rem), starts the guide in the
  parent's dot column, extends it up `--rise` rows (keeping today's 4px gap
  below a parent's dot for a first child), and curves the elbow only for
  `last`. A later sibling's guide passes through its earlier sibling's
  descendants, so the vertical line stays continuous. A shown session's
  ancestors are always shown, since an archived session's descendants are
  archived.

## Check your assumptions

1. **Starting children from a child.**
   - **Plan:** Change "Claude can start child sessions" from: "A top-level
     session's Claude can start a child session with a name, a title, and a
     first message, which reaches the child as a message from its parent. The
     child takes its parent's harness, model, effort, permission mode, and
     directory unless Claude picks others Ox offers, cannot start children of
     its own, and is started without asking for approval, except in plan mode,
     where Claude asks first." to: "A session's Claude can start a child session
     with a name, a title, and a first message, which reaches the child as a
     message from its parent, without asking for approval except in plan mode,
     where Claude asks first. The child takes its parent's harness, model,
     effort, permission mode, and directory unless Claude picks others Ox
     offers, and can start children of its own down to the configured depth,
     two levels below a top-level session by default."
   - **Alternative:** Keep the depth out of the requirement and leave it to the
     configuration docs; the user would not see the default depth stated among
     the behaviors they rely on.
   - **Changed:** to the alternative. The new text ends "and can start children
     of its own up to the configured depth."; the depth and its default live in
     the configuration docs.
2. **Archiving a middle session takes its children with it.**
   - **Plan:** Change "Archiving a session" from: "While a session and its child
     sessions are idle or parked, the user can archive it from its menu without
     confirmation, which archives its children too and stops their Claude
     processes, schedules, and background tasks. Unarchiving a session
     unarchives its children; a child can be archived alone, but not unarchived
     while its parent is archived." to: "While a session and every session
     beneath it are idle or parked, the user can archive it from its menu
     without confirmation, which archives the sessions beneath it too and stops
     their Claude processes, schedules, and background tasks. Unarchiving a
     session unarchives the sessions beneath it; a child can be archived without
     its parent, but not unarchived while its parent is archived."
   - **Alternative:** Archive a middle session alone and leave its children
     running; they would show under an archived row, and Ox would have to allow
     an active session beneath an archived one.
   - **Kept.**
3. **The tree in the sidebar.**
   - **Plan:** Change "Session order" from: "Each workspace lists its top-level
     sessions oldest first, each followed by its child sessions, oldest first
     and indented beneath it." to: "Each workspace lists its top-level sessions
     oldest first, each followed by its child sessions, oldest first and
     indented beneath it; a child's own children follow it the same way,
     indented one level further."
   - **Alternative:** None beyond the flat list the user already ruled out.
   - **Kept.**
4. **A configuration file must set the depth.**
   - **Plan:** Change "Configuration checks" from: "`oxd` refuses to start with a
     malformed configuration, defaults it does not offer, an unknown harness, or
     an effort level, permission mode, or provider order its harness does not
     take, and says why." to: "`oxd` refuses to start with a malformed
     configuration, defaults it does not offer, a maximum child depth below 1,
     an unknown harness, or an effort level, permission mode, or provider order
     its harness does not take, and says why." A configuration file without
     `max_child_depth` stops `oxd` with that reason.
   - **Alternative:** Treat a missing setting as the built-in 2; a file written
     before this change keeps working, at the cost of one setting that is
     silently defaulted while every other one is required.
   - **Kept.**

## Test plan

- Depth limit and nested creation, server, in `TestCreateSession`
  (`internal/server/mcp_test.go`): set `MaxChildDepth: 2`; replace the "a
  child's create_session" refusal with: the worker creates a grandchild, whose
  `session/created` carries `parent_id` the worker and the worker's directory,
  and which takes the worker's message; the grandchild's `create_session` is
  refused with `child sessions nest at most 2 deep`. Fails before the change,
  which refuses the worker.
- Archive across levels, server, in `TestArchive`
  (`internal/server/mcp_test.go`): set `MaxChildDepth: 2`; the kid `k` creates
  a grandchild `g`. A busy `g` (first message `approve Bash {}`) makes archiving
  `a` return `-32002` and archive none; once `g` is idle, archiving `k` alone
  archives `k` and `g`, not `a`; unarchiving `k` unarchives both; archiving `a`
  announces and parks `a`, `k`, and `g`; unarchiving `a` unarchives all three.
  Each fails on the code before the change, which gathers no grandchildren.
- Configuration check, config, in `TestDefaultsMustBeConfiguredChoices`
  (`internal/config/config_test.go`): add `"max_child_depth":2` to its valid
  files, and refuse a file with `max_child_depth` 0 and one without it.
- Sidebar tree and archive gate, e2e, in "a child session shows beneath its
  parent with its title" (`cmd/ox/web/e2e/session.spec.ts`): the child calls
  `create_session` for a grandchild whose first message is `approve Bash {}`.
  The grandchild's row follows the child's row, its title starts further right
  than the child's, and the parent's Archive is disabled with "Wait until it and
  its child sessions are idle" until the grandchild's approval is answered. The
  rest of the test runs with the grandchild in the family: archiving the parent
  hides all three rows, and the grandchild is deleted before the child.
- A child starts a child on its parent's request, live, in `TestLive`'s "Claude
  starts a child session without asking" case
  (`internal/server/live_test.go`): the parent's message to the child tells it
  to load and call `create_session` with name `leaf` and a one-word reply
  message; the test waits for a `session/created` whose `child.parent_id` is the
  child, then destroys the grandchild before the child. It guards the new
  description wording.

## Implementation plan

1. `internal/config/config.go`: add `MaxChildDepth int` with
   `json:"max_child_depth"` to `Config`; `Load` refuses a value below 1 with
   `max_child_depth must be at least 1`. `internal/config/default.json`: add
   `"max_child_depth": 2`. Update `internal/config/config_test.go`, and any Go
   test that writes a configuration file through `Load` (`cmd/oxd/main_test.go`,
   `cmd/ox-mcp/main_test.go`), to include it.
2. `internal/server/server.go`: add `depthLocked(sess)`, the number of
   ancestors, walking `child.ParentID` through `s.sessions`; the caller holds
   `s.mu`.
3. `internal/server/mcp.go`: in `createSession`, replace the `caller.child !=
   nil` case with the depth check under `s.mu`; change the description sentence
   as Decisions gives it; update the `createSession` comment if it names
   top-level callers.
4. `internal/server/session.go`: `archive` drops the `sess.child == nil` gate
   and walks the family breadth-first, holding each member before reading its
   children; `unarchive` gathers the session and every session beneath it by the
   same walk under `s.mu`. Update both comments and the `archived` field
   comment if needed.
5. `cmd/ox/web/src/Sidebar.tsx`: replace the two nested `<For>` loops with one
   `<For>` over the flattened rows memo from Decisions; `SessionRow` takes the
   row and sets `--depth`, `--rise`, and `last`. `archiveBlocked` checks the
   session and every session beneath it (a `descendantsOf` helper built on
   `childrenOf`).
6. `cmd/ox/web/src/style.css`: rewrite the `nav li.child` rules for any depth as
   Decisions describes, removing the adjacent-sibling rules.
7. Tests as the test plan gives them; add `max_child_depth: 2` to `fakeConfig`
   and `haikuConfig` in `cmd/ox/web/e2e/services.ts` and to any other e2e
   configuration written without spreading them.
8. Requirement and documentation updates below.

## Requirement changes

- **Changed:** **Claude can start child sessions.**
  - Old: A top-level session's Claude can start a child session with a name, a
    title, and a first message, which reaches the child as a message from its
    parent. The child takes its parent's harness, model, effort, permission
    mode, and directory unless Claude picks others Ox offers, cannot start
    children of its own, and is started without asking for approval, except in
    plan mode, where Claude asks first.
  - New: A session's Claude can start a child session with a name, a title, and
    a first message, which reaches the child as a message from its parent,
    without asking for approval except in plan mode, where Claude asks first.
    The child takes its parent's harness, model, effort, permission mode, and
    directory unless Claude picks others Ox offers, and can start children of
    its own up to the configured depth.
- **Changed:** **Archiving a session.**
  - Old: While a session and its child sessions are idle or parked, the user can
    archive it from its menu without confirmation, which archives its children
    too and stops their Claude processes, schedules, and background tasks.
    Unarchiving a session unarchives its children; a child can be archived
    alone, but not unarchived while its parent is archived.
  - New: While a session and every session beneath it are idle or parked, the
    user can archive it from its menu without confirmation, which archives the
    sessions beneath it too and stops their Claude processes, schedules, and
    background tasks. Unarchiving a session unarchives the sessions beneath it;
    a child can be archived without its parent, but not unarchived while its
    parent is archived.
- **Changed:** **Session order.**
  - Old: Each workspace lists its top-level sessions oldest first, each followed
    by its child sessions, oldest first and indented beneath it.
  - New: Each workspace lists its top-level sessions oldest first, each followed
    by its child sessions, oldest first and indented beneath it; a child's own
    children follow it the same way, indented one level further.
- **Changed:** **Configuration checks.**
  - Old: `oxd` refuses to start with a malformed configuration, defaults it does
    not offer, an unknown harness, or an effort level, permission mode, or
    provider order its harness does not take, and says why.
  - New: `oxd` refuses to start with a malformed configuration, defaults it does
    not offer, a maximum child depth below 1, an unknown harness, or an effort
    level, permission mode, or provider order its harness does not take, and
    says why.

## Documentation updates

- `docs/protocol.md`: the child session paragraph ("Only a top-level session
  has children" becomes children nesting to `max_child_depth`); Configuration
  (the required `max_child_depth`, at least 1, which the built-in configuration sets to 2, and the example); `harness/list`
  (it returns `max_child_depth` too); `session/archive` and `session/unarchive`
  (every session beneath, not a top-level session's children); the
  `create_session` refusal list (the depth refusal replaces the child-caller
  refusal).
- `docs/architecture.md`: the archive paragraph (each member is held before its
  children are read, ancestors first) and the unarchive sentence (the session
  and every session beneath it).
