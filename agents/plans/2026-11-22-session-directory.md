# Every session stores its directory

## Goal

Each session stores the directory its harness runs in, and the file tree and file viewer show that directory. A child created with `create_session`'s `dir` runs in that folder, but its file tree today shows the workspace's folder: in the live database, 141 of 205 children run outside their workspace's folder, nearly all in worktrees made with `make worktree`, so their trees show `~/src/ox` on `main` instead of the checkout they edit. After this change, the tree, the viewer, and the session header all show the session's own directory. An existing database is upgraded once at startup and keeps every session.

## Related code

- `internal/store/store.go`:
  - `children.directory` is the only stored directory, carried by `Child.Directory`.
  - `Open` runs `schema` with `CREATE TABLE IF NOT EXISTS` on one connection (`SetMaxOpenConns(1)`), under the store's file lock.
  - `AddSession` inserts the session row and its child row in one transaction. `SaveSession` updates the mutable fields: model, effort, permission mode, resume token.
  - `Workspaces` joins `children` to fill `Session.Child`.
- `internal/server/session.go`:
  - `session` holds `workspace` (the workspace's path) and `child *store.Child`, both fixed at creation. `directory()` derives where the harness runs: `child.Directory`, else `workspace`.
  - `newSession(record, workspace, firstLine)` builds it. `start` passes `sess.directory()` as `agent.Options.Workspace`.
- `internal/server/server.go` `New`: builds each stored session with `newSession(record, w.Path, first)`.
- `internal/server/methods.go`:
  - `createSession` builds the top-level `store.Session`. `addSession` stores and publishes it.
  - `listWorkspaces` (`sessionInfo`) and `publishLocked` (`session/created`) send session entries with `child`.
  - `listFolder` and `readFile` each parse `workspace_id` and `path` and resolve `w.path` under `s.mu` themselves.
- `internal/server/mcp.go`:
  - `createSession` resolves the child's directory from `caller.directory()` and `dir`, puts it in `store.Child.Directory`, and returns it as the result's `directory`.
  - `addChild` builds the child with `newSession(record, w.path, "")`.
  - `listSessions` reports `sess.workspace` as each entry's `workspace`. `messageInput` heads a message with `sender.workspace`. Both report the workspace, not the directory, and stay as they are.
- `internal/server/files.go`: `listFolder(dir, path)` and `readFile(dir, path)` read through `os.Root` at `dir`.
- `cmd/ox-mcp/main_test.go` `startOxd`: builds the server with `server.New` and creates its session through `session/create`, so the stored directory comes from `oxd` and the fixture needs no change.
- `cmd/ox/web/src/App.tsx`:
  - `Selection` is the selected session or draft. `files()` resolves the selected session's or draft's workspace and control.
  - The file viewer and file tree `<Show>`s are keyed by `files()?.workspace.id`.
- `cmd/ox/web/src/FileTree.tsx` and `FileView.tsx`: capture `workspaceId` with `untrack` when they mount and poll `ox.listFolder` and `ox.readFile` with it. The tree header shows `workspaceName(workspace.path)`.
- `cmd/ox/web/src/ox.tsx`:
  - `Session` holds `child?: Child`.
  - The reload path (`sessions: w.sessions.map(...)`) and the `session/created` handler build `Session` entries. The reload path reuses an existing entry and refreshes only `label` and `archived`.
  - `listFolder` and `readFile` take a workspace ID. `workspaceName` names a directory by its last segment.
- `cmd/ox/web/src/SessionView.tsx`: the header shows `child?.directory ?? workspaceOf(id)?.path`.
- `cmd/ox/web/src/protocol.ts`: `Child.directory`, session entries in `workspace/list` and `session/created`, and the file method params.

## Decisions

### Shape

- **One stored directory.** `sessions` gains `directory TEXT NOT NULL`, fixed at creation.
  - A top-level session stores its workspace's path, as `workspace/create` stored it.
  - A child stores the directory `create_session` resolved.
  - `children.directory` and `store.Child.Directory` go away. `store.Session.Directory` and the fixed field `session.directory` replace them, and the method `directory()` goes away.
  - `newSession` keeps its workspace argument, for `session.workspace`, and reads the directory from the record.
  - Every reader of `directory()` reads the field: `start`, and `create_session`'s resolution of the caller's directory.
- **The store refuses an unset directory.** `AddSession` returns `session directory must be an absolute path` unless `filepath.IsAbs(r.Directory)`, which also refuses `""`. `SaveSession` stays limited to its mutable fields.
- **One-time upgrade.** After `schema` runs, `Open` reads `SELECT count(*) FROM pragma_table_info('sessions') WHERE name = 'directory'` with `QueryRow(...).Scan`, which releases the single connection before the transaction begins. When the column is missing, one transaction runs:
  1. `ALTER TABLE sessions ADD COLUMN directory TEXT NOT NULL DEFAULT ''`.
  2. Sets each session's `directory` to its workspace's `path`.
  3. Overwrites it with `children.directory` for each child.
  4. `ALTER TABLE children DROP COLUMN directory`.

  Both copies keep the stored spelling as is, with no cleaning or symlink resolution. Every session row has a workspace, whose path `workspace/create` required to be absolute, and every child's directory was resolved from an absolute one, so no row is left empty. A fresh database gets the new tables from `schema`, and the check finds the column. A failed upgrade rolls back, `Open` closes the database and the lock, and returns `upgrade database: <err>`. A probe against `modernc.org/sqlite` v1.62.0 (SQLite 3.54.1) confirmed that both `ALTER` forms run in a transaction, that the backfill sets both kinds of row, and that a `DROP COLUMN` refused because of an index on `children (directory)` rolls back the added column.
- **File reads follow the session.** `workspace/list_folder` and `workspace/read_file` take `path` and exactly one of `session_id` or `workspace_id`.
  - Both methods parse one params struct and call one helper, `fileRoot`. It returns the directory the request reads beneath, or the error response:
    - both selectors, or neither: `-32602` `exactly one of session_id and workspace_id is required`;
    - an unknown session: `-32001` `session not found`, through `sessionNotFound`;
    - an unknown workspace: `-32001` `workspace not found`.
  - `fileRoot` takes `s.mu` only to look up the session or workspace and read its fixed `directory` or `path`. The read runs outside the lock as today.
  - The path errors read `path must be relative to the directory`.
  - The method names stay.
- **Protocol.**
  - Session entries in `workspace/list` and `session/created` carry `directory`.
  - `child` loses `directory`.
  - The `create_session` result keeps `directory`.
- **Page.**
  - `protocol.ts` defines `FileRoot = { session_id: string; workspace_id?: never } | { workspace_id: string; session_id?: never }`. The file methods take `FileRoot & { path: string }`. `ox.listFolder` and `ox.readFile` take a `FileRoot` and a path.
  - `Session` gains `directory: string`, set on both the reload path, for entries it adds, and the `session/created` handler. A reused entry keeps its directory, which never changes.
  - `files()` returns `{ root, directory, control }`:
    - for the selected session: `root` is `{ session_id }` and `directory` is `session.directory`;
    - for a draft: `root` is `{ workspace_id }` and `directory` is the workspace's path.
  - The file viewer and file tree `<Show>`s are keyed by `selected()`, the `Selection` the root is derived from, instead of by workspace ID. Every change of session or draft, including a draft becoming its session, mounts a new tree and viewer, whose captured `root` is the new selection's. Expanded folders, the open file, and its view live in the control, so they survive the remount.
  - `FileTree` and `FileView` take `root: FileRoot` in place of the workspace, captured with `untrack` as now. The tree header shows the name of `directory`.
  - `workspaceName` becomes `directoryName`, since it now names any directory.
  - `SessionView`'s header shows `session.directory`.

### Concerns

- **Requirements.**
  - **File tree** and **File viewer**: the root follows the selection. A request in flight when the selection changes belongs to the unmounted tree or viewer, whose poll stops and whose answer nothing reads. A reconnect resumes polling with the same selector. A second page resolves the same root from the same stored field. When the selected session is deleted, `files()` returns nothing for it and its tree and viewer unmount; a session selected earlier leaves no tree polling with its ID.
  - **File tree updates** and **File viewer updates**: unchanged clock, now per selection.
  - **Open file**, **Markdown preview**, and **File tree expansion**: unchanged. Paths stay relative and live in the session's control. A draft's open file carries to the session it creates, whose directory is the workspace's path, so the same relative path names the same file.
  - **Session header**: unchanged in what it shows, now read from `directory`.
  - **Working directory**, **Ur's shell commands**: unchanged. The harness gets the same directory as before, now stored instead of derived.
  - **Surviving restarts**: the upgrade keeps every session, its settings, resume token, archive state, title, and transcript.
- **Construction.**
  - Every stored session has an absolute directory: `AddSession` refuses any other, and the upgrade fills existing rows from absolute sources.
  - One field, fixed at creation, is the only source for the harness's directory and the file root, so no reader chooses between a child's directory and the workspace's.
  - A file request has exactly one selector, checked once in `fileRoot`, at the boundary.
  - The page's file selector is derived from `Selection` and keys the views that use it, so a mounted tree or viewer cannot hold another selection's selector.
- **Ownership.** `sessions.directory`:
  - Owner: the session row, fixed at creation; in memory, `session.directory`, fixed at creation.
  - Needed for: the harness's directory, the file tree and viewer root, and the session header.
  - Races: none, since it never changes.
  - Cleanup: deleted with the row.

  The upgrade runs once in `Open`, under the store's exclusive lock and on its one connection, before any session loads. It stays in the store so that existing databases still open.
- **Trust boundaries.** The file methods read only beneath a session's directory or a workspace's path, through `os.Root`. A child's `dir` can name any existing directory Claude chooses, and its file tree now shows that directory. Whoever reaches the page can already read that directory through the session's Claude.

## Check your assumptions

1. **The file tree and viewer follow the session's folder.**
   - **Plan:** A child created in another folder, such as a worktree, shows that folder's files in its file tree and file viewer, and the tree's heading names that folder. A top-level session and a new-session draft show the workspace as today.
   - **Alternative:** The tree keeps showing the workspace, which leaves you looking at files the agent is not changing.
   - **Requirement wording:** changes **File tree** and **File viewer**, as in Requirement changes below.
2. **An existing database is upgraded once and keeps every session.** This was your earlier choice over recreating the database.
   - **Plan:** The first start after this change records each session's folder, and every session, setting, and transcript stays.
   - **Alternative:** Delete the old database and lose its sessions, which avoids keeping upgrade code in the store.

## Test plan

- Store, new `TestUpgradeAddsSessionDirectory` in `internal/store/store_test.go`, since no existing test opens a database with an older schema:
  1. With `database/sql`, write a database with the pre-change `schema`, copied into the test, and rows:
     - a workspace whose path is spelled `/w/`;
     - a top-level session with a resume token, a title, and events;
     - an archived child with directory `/w/./sub`, so a cleaned spelling would show, and events.

     Add `CREATE INDEX children_directory ON children (directory)`, which makes the drop fail.
  2. `Open` returns an error starting `upgrade database:`. Reading the file directly, `sessions` has no `directory` column and `children` still has it.
  3. Drop the index and call `Open`. Check, through `Workspaces`, `Events`, and the archive and title fields:
     - the top-level session's `Directory` is `/w/`, and the child's is `/w/./sub`;
     - the resume token, title, archived child, and both sessions' events are kept;
     - `children` has no `directory` column.
  4. Close and reopen, and check that `Open` succeeds and `Workspaces` returns the same sessions.
- Store, `TestChildren`: `Directory` round-trips on top-level and child sessions, and `AddSession` refuses `""` and a relative directory.
- Server, `TestWorkspaceFiles` in `internal/server/methods_test.go`: store a session in the workspace before the server starts.
  - `list_folder` and `read_file` with its `session_id` read the workspace's directory.
  - Both IDs, and neither, return `-32602`.
  - An unknown `session_id` returns `-32001`.
- Server, `TestCreateSession` in `internal/server/mcp_test.go`:
  - The child created in `sub` carries `directory` `dir+"/sub"` in `session/created` and `workspace/list`, and its `child` has no `directory`.
  - `list_folder` with its `session_id` lists `sub`, not the workspace.
  - The top-level session is listed with the workspace's path.
- Server fixtures: every `AddSession` call in `history_test.go`, `live_test.go`, `mcp_test.go`, and `methods_test.go` sets an absolute `Directory`, the workspace's path. `live_test.go`'s child check reads the `session/created` entry's `directory` instead of `child.directory`.
- e2e, `session.spec.ts` "a child session shows its title, its directory, and its parent's message, also after a reload": write a file in the child's directory, open the file tree, and before and after the reload check that it lists that file and not a file only the workspace has, and that opening the file shows its text.
- e2e, `workspace.spec.ts` "each session keeps its own expanded folders": after switching from the first session to the second in the same workspace, delete the first from its menu, wait past a poll tick, and check that the second's tree still lists its entries and shows no error.
- The other file tree and viewer specs pass unchanged, with the page sending `session_id` for sessions and `workspace_id` for drafts. "each session and draft keeps its open file, and a draft's carries to its session" covers the draft becoming its session.

## Implementation plan

1. `internal/store/store.go`:
   - `schema` with `sessions.directory` and without `children.directory`;
   - `Session.Directory`, and `Child` without `Directory`;
   - `AddSession` checking and inserting the directory;
   - `Workspaces` reading it;
   - the upgrade in `Open`.

   Then the store tests.
2. `internal/server/session.go`, `server.go`, `methods.go`, `mcp.go`:
   - the fixed field `session.directory` set from the record, with `directory()` removed;
   - `createSession` setting `Directory: w.path`;
   - MCP `createSession` setting `record.Directory` and building `store.Child` without a directory;
   - `directory` on the `workspace/list` and `session/created` entries;
   - `fileRoot` and the shared params for `listFolder` and `readFile`.
3. Server tests and fixtures above.
4. Web client:
   - `protocol.ts`: `directory` on session entries, `Child` without `directory`, `FileRoot`, and the file method params.
   - `ox.tsx`: `Session.directory` on both paths, `listFolder` and `readFile` taking a root, and `directoryName`.
   - `App.tsx`: `files()` with `root` and `directory`, and both `<Show>`s keyed by `selected()`.
   - `FileTree.tsx` and `FileView.tsx`: take `root`, and the tree's header shows `directoryName(directory)`.
   - `SessionView.tsx`: the header shows `session.directory`.
   - The `workspaceName` callers use `directoryName`.
5. The e2e assertions above.
6. Docs, below. Run `make docs`.

## Requirement changes

- **Changed:** **File tree.**
  - Old: "Beside the transcript, a panel shows the selected session's workspace directory as a tree, folders first, whose folders expand and collapse. It hides version-control folders and operating-system metadata files at any depth, such as `.git` and `.DS_Store`, shows files Git ignores, lists nothing outside the workspace directory, and shows why a folder cannot be read."
  - New: "Beside the transcript, a panel shows the directory the selected session works in, or a new-session draft's workspace directory, as a tree, folders first, whose folders expand and collapse. It hides version-control folders and operating-system metadata files at any depth, such as `.git` and `.DS_Store`, shows files Git ignores, lists nothing outside that directory, and shows why a folder cannot be read."
- **Changed:** **File viewer.**
  - Old: the final clause "and reads nothing outside the workspace directory."
  - New: "and reads nothing outside the file tree's directory."

## Documentation updates

- `docs/protocol.md`:
  - "Workspaces and sessions": every session has `directory`, where its harness runs, fixed at creation; `child` loses `directory`.
  - `workspace/list` and `session/created`: entries carry `directory`.
  - `workspace/list_folder` and `workspace/read_file`: `path` with exactly one of `session_id` or `workspace_id`, and their errors.
- `docs/architecture.md`:
  - The child session paragraph: every session stores its directory at creation, replacing the derived child-else-workspace directory.
  - The file tree and viewer paragraphs: rooted at the selected session's directory or the draft's workspace, and keyed by the selection.
