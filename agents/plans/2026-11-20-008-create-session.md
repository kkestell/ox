# Claude starts a child session

## Goal

Phase 4, task 1 of the agent tools port (`agents/scratch/agent-tools.md`,
Phases, item 4, "Task 1: `create_session` starts a child"): a top-level
session's Claude calls `create_session` on Ox's MCP server to start a child
session the user asked for. The child is stored with its parent, name, title,
and directory; it takes the parent's settings and directory unless the call
overrides them; its first message reaches it as a message from the parent; and
the page lists it beneath its parent. The tool answers with the child's name
and session ID. Roles and addressing, `end_session`, reports, worktrees, and
offers are later tasks.

## Related code

- `internal/server/mcp.go` — the `tools` table, `approvedTools` (every tool,
  so a new one is pre-approved for Claude), `sendMessage` (posts a
  `messageInput` to the recipient's goroutine and calls `acceptMessage`),
  `listSessions` (the call goroutine waits on a `done` channel for an
  operation on another session), and `label`.
- `internal/server/session.go` — `session`: fields fixed at creation
  (`id`, `harness`, `workspace`, the directory the harness runs in) and fields
  owned by `run`; `newSession`, `start` (passes `sess.workspace` as
  `agent.Options.Workspace`), `acceptMessage`, `post`.
- `internal/server/methods.go` — `createSession` (the `s.closed` check and
  membership change under `s.mu`, `session/created` broadcast),
  `listWorkspaces`, and `destroySession`, whose operation on the session's
  goroutine stops the agent, deletes the store row, then removes the session
  from membership under `s.mu`.
- `internal/server/server.go` — `New` loads stored sessions; `Close` ends
  every session that exists once `closed` is set.
- `internal/store/store.go` — the schema (`CREATE TABLE IF NOT EXISTS`, no
  migrations), `Session`, `AddSession`, `Workspaces`, `DeleteSession`.
  Foreign keys are on.
- `internal/config/config.go` — `Harness.CheckModel`, `CheckPermissionMode`.
- `internal/claude/claude.go` `systemPrompt`, `startProcess` — the process
  runs in `Options.Workspace`, and Ox's instructions name it.
- `cmd/ox/web/src/protocol.ts`, `ox.tsx` (`Session`, `sessionLabel`, the
  `workspace/list` reload and the `session/created` handler), `Sidebar.tsx`,
  `SessionView.tsx` (the header shows the workspace path), `style.css`
  (`nav li`).
- Tests: `internal/server/methods_test.go` `TestSendMessage` (fake sessions
  with attached MCP connections, a `broken` harness), `internal/server/live_test.go`
  `TestLive` (the `list_sessions`/`send_message` cases),
  `cmd/ox/web/e2e/session.spec.ts` (`sendMessage`, an MCP call over the
  socket).

## Decisions

- **Tool.** `create_session`, not read-only. Input: `name`, `title`,
  `message` (required strings); `harness`, `model`, `effort`, `dir`
  (optional strings). Description:

  > Start a child Ox session and send it its first message. Use it only when the user asks for another session or agent. name is the child's address among your children: lowercase letters, digits, and hyphens. title is its label in Ox. The child runs with your harness, model, effort, permission mode, and directory unless harness, model, effort, or dir is given; list_models lists the choices. dir is an existing directory, absolute or relative to yours. The message reaches the child as a message from you. Returns the child's session ID.

- **Result.** JSON `{"session_id", "name", "harness", "model", "effort",
  "permission_mode", "directory"}`, the child as created, so the parent sees
  the settings it inherited. Returned once the child has accepted the message.
- **Refusals** (`isError: true`), checked in this order:
  - arguments that do not decode → `invalid arguments`;
  - `name` not matching `^[a-z0-9][a-z0-9-]*$` →
    `name must be lowercase letters, digits, and hyphens, starting with a letter or digit`;
  - `title` trimmed empty, longer than 48 characters, or containing a newline
    → `title must be one line of 1 to 48 characters`;
  - `message` blank → `message is required`;
  - caller is a child → `a child session cannot create sessions`;
  - `dir` given and not an existing directory → `dir must be an existing directory`;
  - `harness` given and not configured → `harness "<h>" is not configured`;
  - `harness` other than the parent's with no `model` →
    `model is required with another harness`;
  - the resolved settings failing `CheckModel` or `CheckPermissionMode` →
    their error text;
  - `name` already used by one of the caller's children →
    `a child named "<name>" already exists`;
  - daemon shutting down → `daemon is shutting down`; caller or child gone
    before its operation runs → `session not found`;
  - the child's harness cannot be launched → the launch error, and the child
    is deleted again.
- **Settings.** Resolved on the parent's goroutine, which owns its settings:
  harness = `harness` or the parent's; model = `model`, or the parent's when
  the harness is the parent's; effort = `effort`, or else the parent's effort
  when the model offers it, or else the model's first effort, or `""` for a
  model with none — the rule **Effort across models** gives the page, as
  `config.Harness.Effort(model, current string) string`; permission mode =
  the parent's. All are checked against the configuration as `session/create`
  checks them, so a parent whose model or mode the configuration retired
  passes on nothing unchecked. The child is stored with the resolved settings
  and starts with them, so **Settings shown are settings used** holds.
- **Directory.** No `dir`: the parent's directory. An absolute `dir` is kept
  as given; a relative one is appended to the parent's directory with a
  separator, uncleaned, as `workspace/create` keeps paths. It must stat as a
  directory. The child's directory is fixed at creation and stored, resolved.
  The child belongs to its parent's workspace whatever its directory.
- **Construction.**
  - A session is a child exactly when it has a `child` value
    (`parent_id`, `name`, `title`, `directory`), fixed at creation:
    `session.child *child` in Go, nil for a top-level session; a row in a new
    `children` table with every column `NOT NULL`; an optional `child` object
    in the protocol and `child?: Child` in the web client. No reader checks a
    zero-valued field.
  - Where the harness runs is derived: `sess.directory()` returns
    `child.directory` for a child, else the workspace path. The `workspace`
    field stays the workspace's path.
  - A session's label is decided by `oxd` from one rule: a child's title,
    else the first user prompt's first line, else the ID's start.
    `label` takes the `*session`. The page's `sessionLabel` applies the same
    rule from the `child` it was sent.
  - One level of nesting: the only path that creates a child refuses a caller
    that is a child.
  - `name` is unique among a parent's children: `UNIQUE (parent_id, name)` in
    the store; the in-memory check under `s.mu` gives the refusal its text.
  - A parent with children cannot be deleted: the session's destroy operation
    refuses while any session's `child.parent_id` names it, checked under
    `s.mu` before the store delete. `children.parent_id REFERENCES sessions`
    with no `ON DELETE` makes the store refuse the same. `session_id
    REFERENCES sessions ON DELETE CASCADE` removes a child's row with it.
    Workspace removal already requires no sessions, children included.
- **Ownership.**
  - New stored table `children`. The user-visible behavior that needs it: a
    child keeps its place beneath its parent, its title, and its directory
    across restarts. It is written once, in the same transaction as the
    session row (`AddSession` with `Session.Child`), read by `Workspaces`
    with a `LEFT JOIN`, and deleted by cascade with the child's session. A
    new table needs no migration of existing databases.
  - Creation runs in three steps. The call goroutine checks the arguments
    and the directory. An operation on the parent's goroutine resolves the
    settings, then, under `s.mu`, checks `closed` and name uniqueness, stores
    the session and child, starts the child's goroutine, adds it to the
    workspace and `s.sessions`, and broadcasts `session/created`; it also
    takes the parent's label and builds the `messageInput`, and hands the
    child and message back through a channel. The call goroutine then posts
    to the child's goroutine, which calls `acceptMessage` and responds. No
    session goroutine posts to another.
  - Races. Creation and the parent's deletion both run on the parent's
    goroutine, so a deleted parent creates nothing (`session not found`) and
    a parent with a registered child refuses deletion. Creation and shutdown:
    `closed` is checked under `s.mu`, as `createSession` does, so `Close`
    ends every child it can see. A child's deletion and its parent's: the
    child's store row goes before its membership, so the parent's in-memory
    check sees the child until its row is gone; either outcome is valid. A
    child deleted before its message is posted: the post fails and the call
    answers `session not found`. A user prompt sent to the child before its
    message: the message waits behind it, as any message does.
  - Rollback. When `acceptMessage` fails, which happens only when the harness
    cannot be launched, the child's goroutine deletes the child through the
    same destroy operation `session/destroy` uses, so its name is free for a
    retry; the page sees `session/created` then `session/destroyed`. A
    harness that starts and fails later drops the message and shows why in
    the child's transcript, as **Message delivery** says.
- **Deletion.** `destroySession`'s operation body moves to a session method,
  `destroy() error`, used by `session/destroy` and the rollback. It refuses a
  session with children with `session has child sessions` (`-32000`, as
  `workspace has sessions`). The page hides a session's Delete button while it
  has children, as it hides a workspace's Remove button while it has
  sessions; a refusal that races the page shows in the dialog through
  `createAttempt`.
- **Requirements.**
  - **Claude can start child sessions** (added). It holds in every
    permission mode but plan, because the tool is in `approvedTools` and not
    read-only; plan mode asks, as for `send_message`.
  - **Session labels**, **Session order**, **Deleting a session**, and
    **Working directory** change (below).
  - **Messages in the transcript** and **Message delivery**: the first
    message is a `messageInput` from the parent, handed to the child's agent
    by `acceptMessage`, which starts the parked child, so it shows, waits,
    and is dropped as any message is.
  - **Only the user sends**: the only input created is the message the
    parent's Claude sent.
  - **Every page in sync** and **One transcript**: `session/created` and
    `workspace/list` carry `child`, so every page, after a reload or a
    reconnect, lists the child beneath its parent with its title. The page
    starts no request for a child it did not create, so **Creation keeps the
    user's view** holds: a new child never takes the selection.
  - **Surviving restarts**: the `children` row and the session row are one
    transaction, and `New` rebuilds the child with its directory.
  - **Removing a workspace**: unchanged; children are the workspace's
    sessions.
  - **Ox's instructions**: the child's Claude is told it is its own session
    in its own directory, through `Options.Workspace`.
- **Settling.** The call waits on two posts, each ending when the session
  takes the operation or has ended, the bound `send_message` and
  `list_sessions` already use. Every path responds once.
- **Claude Code.** A `-p` process runs in any directory without a trust
  dialog ([Behavior of `-p`](../../docs/claude-code.md#behavior-of--p)) and
  in the session's directory ([Persistent session](../../docs/claude-code.md#persistent-session),
  last line). An `--allowedTools` MCP tool runs without asking in `default`,
  `acceptEdits`, and `auto`, and plan mode asks for one not annotated
  `readOnlyHint` (same section). Haiku declines a non-read-only MCP tool in
  plan mode (same section), so the live case uses a manual session.
- **Trust boundaries.** The tool is reachable only through a forwarder
  attached to `oxd`'s socket, whose clients can already create sessions with
  `session/create`. New is that a session's Claude can, without an approval
  prompt outside plan mode, start a Claude in any existing directory it
  names, in its own permission mode, which governs what that Claude may
  change there.

## Check your assumptions

1. **Deleting a session that has child sessions.**
   - **Plan:** A session's Delete button is hidden while it has child
     sessions, as a workspace's Remove button is while it has sessions; the
     user deletes the children first. Children are never left without their
     parent, and their transcripts are never deleted as a side effect.
   - **Alternative:** Deleting a parent also deletes its children, with the
     confirmation naming them: one step, but the children's histories go with
     it. Or the children become ordinary sessions: nothing is lost, but they
     stop showing where they came from.
2. **A child session's permission mode.**
   - **Plan:** A child runs in its parent's permission mode at the moment it
     is created, and the user can change it afterward like any session's.
     Claude cannot start a child with more freedom than its parent has.
   - **Alternative:** Children start in the configured default mode for new
     sessions: a predictable mode, but a child of an auto-mode parent would
     stop for approvals, and a child of a plan-mode parent could edit. Or
     Claude chooses the mode: flexible, but Claude could start a child in a
     mode that skips approvals the user kept on for the parent.
3. **Starting a child asks no approval.**
   - **Plan:** Claude starts a child without an approval card in every mode
     except plan mode, where it asks first, as for sending a message. This
     includes a child in any existing directory Claude names, working there
     in the parent's mode.
   - **Alternative:** Ask before each child is started: the user sees the
     name, settings, and directory first, at the cost of a card for every
     child they already asked Claude to start.
4. **Session labels** (changed).
   - **Plan:** A child is labeled with the title Claude gave it, even after
     the user sends it a prompt.
     Old: "A session is labeled with the first line of the first prompt the user sent it, or a short identifier before it has one."
     New: "A child session is labeled with the title it was created with. Any other session is labeled with the first line of the first prompt the user sent it, or a short identifier before it has one."
   - **Alternative:** Label a child by the first prompt the user sends it,
     falling back to its title: the label could change mid-task.
5. **Session order** (changed).
   - **Plan:** Children are listed beneath their parent, indented.
     Old: "Each workspace lists its sessions oldest first."
     New: "Each workspace lists its top-level sessions oldest first, each followed by its child sessions, oldest first and indented beneath it."
   - **Alternative:** One flat list, oldest first, with a marker on
     children: simpler, but a parent's children scatter among later sessions.
6. **Working directory** (changed).
   - **Plan:** A child works in the directory it was created in, which may
     be outside the workspace, and its header shows that directory.
     Old: "Claude works in the session's workspace directory."
     New: "Claude works in the session's workspace directory, or in a child session, the directory it was created in."
   - **Alternative:** Children always work in the workspace directory, and
     Claude cannot choose another until worktrees arrive: less reach, but no
     child can work in another checkout.

## Test plan

- **Creation, server.** New `TestCreateSession` in
  `internal/server/mcp_test.go`, since no existing test covers children,
  their settings resolution, or a restart over the same store. Config:
  harness `fake` with models `m` (efforts `low`, `high`) and `n` (effort
  `medium`) and modes `manual`, `plan`; harness `broken` (Start fails) with
  the same models and modes. A start wrapper records each session's
  `Options.Workspace`. Parent A: fake, `m`/`high`/`manual`, first prompt
  "parent A".
  - `create_session {name: worker, title: Worker, message: "hello child"}`
    from A returns `{session_id, name: worker, harness: fake, model: m,
    effort: high, permission_mode: manual, directory: <workspace dir>}`;
    `session/created` carries `child {parent_id: A, name: worker, title:
    Worker, directory}`; the child's `prompt` update has `from: A`,
    `from_label: "parent A"`, `text: "hello child"`; the child started in
    the workspace directory.
  - `workspace/list` lists the child after A with label `Worker` and its
    `child`; A's `list_sessions` lists it labeled `Worker`.
  - Overrides: `model: n` → effort `medium`; `dir: "sub"` (created under the
    workspace) → directory `<dir>/sub` and the harness started there;
    after `session/configure` sets A's mode to `plan` while idle, a new child
    gets `plan`.
  - Refusals, each with its exact text: `name: "Bad"`, title blank, title of
    49 characters, title with a newline, blank message, `dir: "missing"`,
    `harness: "nope"`, `harness: "broken"` with no model, `model: "x"`,
    `name: worker` again, and `create_session` from the child's own
    attachment.
  - Rollback: `harness: broken, model: m, name: doomed` → the launch error,
    `session/created` then `session/destroyed` for it, and a retry with
    `name: doomed` on `fake` succeeds.
  - Deletion: `session/destroy` of A → `-32000 session has child sessions`;
    destroying every child, then A, succeeds.
  - Restart: before deleting, a second `New` over the same store (after
    `Close`) lists the children with the same `child` values, and a message
    to the `sub` child starts it in `<dir>/sub`.
- **Page, e2e.** New test in `cmd/ox/web/e2e/session.spec.ts`, "a child
  session shows beneath its parent with its title": generalize `sendMessage`
  into `callTool(from, name, arguments)`, create a parent from the page and
  a child of it through `create_session` with `dir` set to a new
  subdirectory; assert the sidebar row after the parent is the child with
  its title, the parent's row has no Delete button, selecting the child
  shows its title and directory in the header and the message from the
  parent in its transcript, and after a reload all of it holds; delete the
  child and the parent's Delete button returns. No existing test lists
  children.
- **Live.** In `TestLive`, after the `send_message` cases: a manual Haiku
  session is asked to load and call `mcp__ox__create_session` with name
  `helper`, title `Helper`, `dir` a new temporary directory, and message
  "Reply with just the word ok, without calling any tool.", using `refuse`
  for approvals. Assert the tool ran without an approval and its
  `tool_result` is not an error and contains `"session_id"`, the
  `session/created` notification carries that directory, and the child takes
  the message from the parent and finishes a turn with a non-error `result`.
  Then destroy the child.

## Implementation plan

1. `internal/store/store.go`: add the `children` table to the schema, as in
   Construction; `Child` (`ParentID`, `Name`, `Title`, `Directory`) and
   `Session.Child *Child`; `AddSession` inserts the child row in the same
   transaction when `Child` is set; `Workspaces` reads it with a `LEFT JOIN`.
2. `internal/config/config.go`: `Harness.Effort(model, current string)
   string`, per Settings.
3. `internal/server/session.go`: the `child` type with protocol JSON tags,
   `session.child` (fixed at creation, set by `newSession` from the record),
   `directory()`, used by `start`; `destroy() error`, moved from
   `destroySession`, with the children check.
4. `internal/server/methods.go`: `destroySession` posts `destroy`;
   `listWorkspaces` and `session/created` include `child`; `label` callers
   pass the session.
5. `internal/server/mcp.go`: the `create_session` tool after `read_session`
   and `createChild`, per Decisions; `label(*session)` returns the child's
   title first.
6. Web client: `protocol.ts` (`Child`, `child?` on the `workspace/list`
   session entry and `session/created`); `ox.tsx` (`Session.child`, set on
   reload and on `session/created`; `sessionLabel` returns the child's title
   first); `Sidebar.tsx` (each top-level session's row followed by its
   children's rows, class `child`, from one row component; Delete only while
   a session has no children); `SessionView.tsx` (the header path is the
   child's directory, else the workspace path); `style.css` (`nav li.child`
   indented).
7. Tests, per the Test plan.
8. Docs, per Documentation updates, and the requirement text below once
   approved.

## Requirement changes

- **Added** (after **Claude can read other sessions.**):
  **Claude can start child sessions.** A top-level session's Claude can start a child session with a name, a title, and a first message, which reaches the child as a message from its parent. The child takes its parent's harness, model, effort, permission mode, and directory unless Claude picks others Ox offers, cannot start children of its own, and is started without asking for approval, except in plan mode, where Claude asks first.
- **Changed: Session labels.**
  Old: A session is labeled with the first line of the first prompt the user sent it, or a short identifier before it has one.
  New: A child session is labeled with the title it was created with. Any other session is labeled with the first line of the first prompt the user sent it, or a short identifier before it has one.
- **Changed: Session order.**
  Old: Each workspace lists its sessions oldest first.
  New: Each workspace lists its top-level sessions oldest first, each followed by its child sessions, oldest first and indented beneath it.
- **Changed: Deleting a session.**
  Old: Deleting a session asks first, then stops Claude, deletes Ox's history of it, leaves Claude's transcript and the workspace on disk, and removes it from every page.
  New: A session can be deleted only when it has no child sessions. Deleting it asks first, then stops Claude, deletes Ox's history of it, leaves Claude's transcript and the workspace on disk, and removes it from every page.
- **Changed: Working directory.**
  Old: Claude works in the session's workspace directory.
  New: Claude works in the session's workspace directory, or in a child session, the directory it was created in.

## Documentation updates

- `docs/protocol.md`: Workspaces and sessions — a child session, its parent,
  name, title, and directory, and that it belongs to its parent's workspace;
  `workspace/list` and `session/created` — the optional `child` object;
  `session/destroy` — the `session has child sessions` refusal; MCP → Tools —
  a `create_session` paragraph with its arguments, settings and directory
  resolution, result, and refusals; `list_sessions` — a child's `label` is
  its title.
- `docs/architecture.md`, Sessions: a child's fixed `child` value and
  derived directory; creation on the parent's goroutine and the message
  handed over on the child's; deletion refused while children exist. The
  `store` row of the package table: the `children` table.
