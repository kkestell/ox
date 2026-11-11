# Workspaces and saved sessions

## Goal

The user adds a workspace once by choosing a directory. The sidebar groups
sessions under their workspaces, and New Session in a workspace starts a
session there without asking for a directory. Workspaces and sessions are
saved, so after `oxd` exits and starts again, the sidebar shows the same
workspaces and sessions, and opening a session brings its conversation back.

## Decisions

- `oxd` owns the saved state, in one SQLite database. Every client sees the
  same workspaces and sessions through the protocol, and `oxd` is the only
  process that writes the database.
- A session belongs to a workspace by ID. Its directory is stored separately
  and is the workspace's directory when the session starts.
- `workspace.remove` refuses while any session belongs to the workspace,
  including exited ones.
- The database holds no environment variables. A client passes its
  environment each time it starts an agent, as `session.new` does today.

## Sequencing

Implementation starts after two pieces of work land on `main`:

- The `claude-code-acp` rewrite now in progress, which also changes `oxd`,
  `ox-protocol`, `oxctl`, `scripts/smoke-acp.py`, and the e2e specs.
- The sidebar styling branch `hypercube/9bf98b51/sidebar-styles`, whose
  placeholder workspace group this plan replaces with real workspaces.

## Storage

- `rusqlite` with the `bundled` feature, added to `oxd`.
- The database sits beside the socket as `<socket>.db`, next to the
  `.lock` and `.startup` files, so the dev daemon and every test daemon get
  their own.
- A `Store` type in `crates/oxd/src/store.rs` wraps one
  `Mutex<rusqlite::Connection>`. Every query is a small local read or write,
  so it runs inline without a pool or a blocking thread.
- On open, `Store` runs one SQL batch that creates the tables if they do not
  exist. The schema has no migrations: before production, a schema change
  means deleting the dev database.

```sql
CREATE TABLE IF NOT EXISTS workspaces (
    id   TEXT PRIMARY KEY,
    path TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS sessions (
    number           INTEGER PRIMARY KEY AUTOINCREMENT,
    id               TEXT NOT NULL UNIQUE,
    workspace_id     TEXT NOT NULL REFERENCES workspaces(id),
    command          TEXT NOT NULL,  -- JSON array
    cwd              TEXT NOT NULL,
    title            TEXT,
    agent_session_id TEXT
);
```

Workspaces list in insertion order (`rowid`). Sessions list by `number`, which
replaces the in-memory `next_session` counter, so creation order survives a
restart.

## Protocol (`ox-protocol`)

- New `WorkspaceInfo { id, path }`.
- `SessionInfo` gains `workspace_id`.
- New methods:
  - `workspace.add { path }` → `WorkspaceInfo`. `path` must be absolute and
    a directory. A path that is already a workspace is refused with
    `INVALID_REQUEST`.
  - `workspace.remove { workspace_id }` → null. Refused with `INVALID_STATE`
    while any session belongs to the workspace, and `NOT_FOUND` for an
    unknown ID.
  - `workspace.list` → `Vec<WorkspaceInfo>`.
- Changed methods:
  - `session.new { workspace_id, command, env }`. The session's `cwd` is
    the workspace's path.
  - `session.load { session_id, env }` restarts the agent of a saved session
    from its stored command, directory, and `agent_session_id`. It replaces
    the current form, which takes a command, directory, and agent session ID.
  - `sessions.watch` becomes `watch`, and each message carries both lists:
    `ServerMessage::List { subscription, workspaces, sessions }`. The daemon
    sends it at once and again whenever either list differs from the last
    one sent. `Client::watch_sessions` and `SessionsWatch` become
    `Client::watch` and `Watch`.
- `Client` gains `add_workspace`, `remove_workspace`, and `list_workspaces`,
  and its `new_session` and `load_session` take the new parameters.

## `oxd`

- **Startup.** `oxd` opens the store and registers every saved session as a
  `Session` in state `exited`, with no agent and no events, carrying its
  workspace, command, directory, title, and `agent_session_id`. It deletes
  saved sessions that have no `agent_session_id`, because they never started
  and cannot be loaded.
- **`session.new`** inserts the session row, then spawns the agent.
- **Title and agent session ID.** When a session's title or
  `agent_session_id` changes, the session writes the new value to its row.
  `Session::update` already detects `SessionInfo` changes, so this happens
  there. A failed write goes to stderr (`oxd.log`) and the session carries on.
- **`session.close`** deletes the session's row. Shutdown on SIGTERM, SIGINT,
  or idle exit kills the agents and keeps every row.
- **`session.load`** is accepted only for a session in state `exited` that has
  an `agent_session_id`, and is refused with `INVALID_STATE` otherwise. It
  starts a new agent on the same `Session`, so the session ID, subscriptions,
  and sidebar selection stay the same, and the session goes through
  `starting` to `idle` as a new one does. The agent replays the conversation
  before it answers `session/load`:
  - A session with no events, such as one restored at startup, records the
    replay as its history.
  - A session that already has events, such as one whose agent crashed,
    keeps its history and drops the replay, so the conversation is not
    duplicated. Its history then shows a second `agent_started` after the
    `agent_exited`.
- **Idle exit.** Idle becomes "no clients and no running agents," where a
  running agent is any session not in state `exited`. Exited sessions are
  saved and no longer keep `oxd` alive. The daemon recomputes idle whenever a
  session's state changes as well as when the registry changes.

## `oxctl`

- `oxctl workspace add PATH` prints the new workspace's ID.
- `oxctl workspace ls` prints `id<TAB>path` per workspace.
- `oxctl workspace rm ID` removes a workspace.
- `oxctl new --workspace ID -- COMMAND` replaces `--cwd`.
- `oxctl load SESSION` takes `oxd`'s session ID and loads that session.
- `oxctl ls` adds the workspace ID column.

## App (`crates/ox`, `ui/`)

- **Tauri commands.** Add `add_workspace(path)`, `remove_workspace(workspace_id)`,
  and `load_session(session_id)`. `new_session` takes `workspace_id` instead of
  `cwd`. `watch_sessions` becomes `watch` and forwards both lists.
- **`useSessions`** becomes `useWorkspaces`, returning `workspaces` and
  `sessions`. `protocol.ts` gains `WorkspaceInfo` and adds `workspace_id`
  and `agent_session_id` to `SessionInfo`.
- **Sidebar.** An Add Workspace button at the top of the sidebar opens the
  directory dialog and calls `add_workspace`. Each workspace is a group
  headed by its directory's name, with the full path as a tooltip. The
  header's New Session menu starts a session in that workspace with no
  dialog, and its remove button calls `remove_workspace`. A refused removal
  shows its error where the sidebar shows other action errors. Sessions
  render under their workspace's group.
- **Opening a saved session.** Selecting a session in state `exited` that has
  an `agent_session_id` calls `load_session`, then views it as usual.
- **Styling.** The workspace group reuses the markup and classes from the
  sidebar styling branch. New elements, such as Add Workspace and the remove
  button, get only the CSS their layout needs.

## Documentation

`docs/architecture.md` describes the database, workspaces, saved sessions,
`session.load` on a saved session, and the new idle rule.

## Tests

Each item fails without the change it covers.

- **Store** (unit, `store.rs`): workspace insert and list round-trip,
  duplicate path refused, session insert, title and `agent_session_id`
  updates, delete, and ordering by `number`.
- **Daemon** (`crates/oxd/tests/daemon.rs`, through `Client`):
  - `workspace.add` refuses relative paths, non-directories, and duplicates.
  - `workspace.remove` refuses while the workspace has a session, then
    succeeds after `session.close`.
  - `session.new` sets `cwd` to the workspace path and `workspace_id`.
  - Restart: a daemon stopped with SIGTERM and started on the same socket
    lists the same workspaces and sessions, each session `exited` with its
    title. A session that never started is gone.
  - `session.load` on a restored session records the fake agent's replay
    and reaches `idle`. On a session whose agent exited, the history keeps
    its events with no duplicated replay.
  - `session.load` on a running session is refused.
  - `lifetime`: a daemon whose only session has exited reaches idle exit.
    The existing case, where a live session keeps the daemon alive, still
    passes.
- **Fake agent** (`oxd-fake-agent`): advertises `loadSession` and replays a
  fixed conversation on `session/load`, if the rewrite has not already added
  this.
- **e2e** (`ui/e2e`): the helper adds a workspace for its temporary folder
  with `oxctl workspace add` and starts sessions with
  `oxctl new --workspace`. A spec checks that sessions render under a group
  named after their workspace's folder.
- **Smoke** (`scripts/smoke-acp.py`): the load check stops `oxd` with SIGTERM,
  lists sessions through a restarted daemon, checks the session is `exited`
  with its title, loads it with `oxctl load SESSION`, and checks the replayed
  conversation and a follow-up prompt.

## Verification

`make check`, `make e2e`, and `make smoke-claude`, each reported with its
result.

## Unverified

- Whether `opencode acp` advertises `loadSession`. If it does not,
  `session.load` on a saved OpenCode session fails with "the agent cannot
  load sessions" and the session stays `exited`.
