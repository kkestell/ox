# oxd starts a session in a new worktree on a new, existing or tracking branch

- Task: `2026-11-12-003-create-session-worktrees`
- Status: ready (revision 3). Revision 3 absorbed the former shared design,
  which was then deleted. Its request, Hypercube comparison, shared contracts,
  edge cases and settled entries moved, unchanged, into this plan and the later
  units' plans. Unit IDs were renumbered into execution order. This unit's
  decisions are the same as in revision 2, which the implementation follows.
- Source HEAD: planned at `e7fdf22c675479f9f2fa328afe285c173ca99618`; executes
  on `7c53b7e` (002 committed)
- Author: planner child of Hypercube session 2ea441ae, Claude Code, Opus 5.5
  (claude-opus-5-5); coordinator ID `planner`; native conversation ID
  `5ce61adf-7d6c-4b85-ab78-5167733b9e23`
- Dependencies: `2026-11-12-002-xdg-data-dir`, for the documented default
  path only (`data_dir()/oxd.sock`, so worktrees default to
  `$XDG_DATA_HOME/ox/worktrees/`)

## Outcome and boundaries

### The request

User's request: "Please review how ~/projects/hypercube allows the user to
choose existing checkout, new worktree, or new worktree on branch when creating
new sessions. I would like to add that to Ox. But Hypercube was a quick and
dirty vibe coded experiment. We hold Ox to a MUCH higher standard. So please
think very carefully about the architecture, clean boundaries, and likely edge
cases we need to support."

The user's walkthroughs (corrections A–I, relayed by 2ea441ae on 2026-11-12)
are built in. When every unit is done:

- Starting a session offers three choices in Git workspaces: run in the
  workspace's directory (today's behaviour), run in a new worktree on a new
  branch Ox names, or run in a new worktree on a branch the user names. A named
  branch that exists locally is checked out. A name found only on a remote
  creates a local branch tracking it. Any other name creates a new branch from
  Start From (default `HEAD`). Non-Git workspaces offer only the first choice.
  The app and `oxctl` both offer them.
- The session appears at once in `starting`. `oxd` adds the worktree, then
  launches the agent in it, as one startup.
- Worktrees live in Ox's data directory, not in the repository.
- Deleting a session (`session.delete`, renamed from `session.close`) removes
  its worktree when that loses nothing uncommitted, or when the user confirms
  discarding. It deletes a branch only if Ox created it and every commit on it
  is on some other branch, tag or remote branch. Otherwise the branch is kept
  and reported. A branch that existed before the session is never deleted.
- Opening the new-branch dialog fetches in the background and updates the
  branch suggestions when the fetch finishes.
- Ox's socket and database move to `$XDG_DATA_HOME/ox`, falling back to
  `~/.local/share/ox`, on every platform. The bridge's store moves to
  `$XDG_DATA_HOME/claude-code-acp`.
- Saved sessions keep their worktrees across reconnects, daemon restarts and
  idle exit, and load into them again.

Non-goals of the whole request:

- parking and archiving sessions (later; 005's removal operations are shaped
  for them);
- copying gitignored files (`.worktreeinclude`, a separate later task);
- initializing submodules;
- merge status or merge actions;
- child sessions;
- remembering the last choice, and a sidebar marker for worktree sessions;
- migrating the old data directory.

### Units of the request

Execution order: 002 → 003 → 004 → 005 → 006. Each unit is committed once its
review is accepted.

| Unit | Delivers |
| --- | --- |
| `2026-11-12-002-xdg-data-dir` (committed) | Data directory (C) |
| `2026-11-12-003-create-session-worktrees` (this plan) | The choices in `oxd` and `oxctl`; naming and location (B); existing and remote branches (D); startup in `starting` (G); the git runner with the client's environment (H); load and restore of worktree sessions |
| `2026-11-12-004-workspace-branches` | Background fetch (E) and the Git working tree flag (F), daemon side |
| `2026-11-12-005-remove-session-worktrees` | Delete rename and the two removal operations (A); startup cleanup; deletion after outside changes |
| `2026-11-12-006-app-session-checkout` | The choices, fetch, flag and delete confirmation in the app; in-window dialogs (I) |

### This unit

`session.new` takes a `checkout`. For a worktree, it validates and resolves the
branch (new, existing local, or tracking a remote), creates a new branch,
saves the rows, and answers at once with the session `starting`. The session's
startup then adds the worktree beside `oxd`'s socket and launches the agent in
it. A failure at that point rolls back and leaves the session `exited` with
git's error. Git runs with the client's environment minus the
repository-local variables. `session.load` refuses a session whose directory is
gone. `oxctl new` gains `--worktree`, `--branch` and `--start`.

Not in this unit:

- `workspace.branches` and the Git flag (004);
- removal, the `session.delete` rename and startup cleanup (005);
- the app (006).

After this unit, `session.close` still deletes rows (the `worktrees` row by
cascade) and leaves worktrees and branches on disk. Startup still deletes
unstarted rows without removing their worktrees.

## What Hypercube does, and what Ox does differently

Read in `~/projects/hypercube` at `c094fa6`: `internal/workspace/workspace.go`,
`internal/workspace/branches.go`, `internal/session/sessions.go`,
`internal/session/removal.go`, `internal/ui/web/agent/worktree-select.tsx`,
`internal/ui/web/agent/new-branch-dialog.tsx`, `internal/ui/actions.go`.

User-facing behaviour: a "Location" dropdown under the composer of an unsent
session offers *Existing checkout*, *New worktree* and *New worktree on
branch…*. The last opens a dialog with a new branch name and a "Source" picker
listing local and remote branches or accepting any commit. A worktree lives at
`<checkout>/.hypercube/<session id>` on branch `hypercube/session-<id>` or the
named new branch. The directory is hidden by appending `/.hypercube/` to
`.git/info/exclude`. `.worktreeinclude` paths are copied in and submodules are
initialized. Deleting a session refuses while the worktree has uncommitted
changes, its branch has commits not on the checkout's `HEAD`, the starting
commit or a remote branch, or a submodule has unpushed commits. Otherwise it
force-removes the worktree and deletes the branch.

Defects and shortcuts Ox avoids, with the unit that avoids each:

1. **Deletes another session's branch** (003, 005). It checks that the branch
   is new, then runs `worktree add -b`, and on failure runs `branch -D` if the
   branch exists. With two concurrent creations under one name, the loser
   deletes the winner's branch. Ox creates the branch itself with `git
   branch`, which is atomic and fails if the branch exists. It records that it
   created the branch, and deletes only with a compare-and-swap `update-ref -d
   <ref> <expected>`.
2. **"Merged" is relative to whatever `HEAD` is now** (005). Ox asks whether the
   commits are on *any* other branch, tag or remote-tracking branch.
3. **A session cannot be deleted while its branch is unmerged** (005). Ox keeps
   the branch and removes the worktree.
4. **Writes into the repository** (003): worktrees inside the checkout and a
   rule in `info/exclude`. Ox keeps worktrees in its own data directory and
   writes nothing into the repository.
5. **Network during creation** (003, 004): `submodule update --init
   --recursive`. Ox does not initialize submodules. Its only network use is the
   dialog's background fetch, which never blocks creation.
6. **Git on the request path with no crash recovery** (003, 005). Ox answers at
   once, adds the worktree during the session's startup, and cleans unstarted
   sessions at daemon startup.
7. **Subdirectory workspaces move to the repository root** (003). Ox runs the
   session in the same subdirectory of the worktree.
8. **Git inherits the daemon's environment, `GIT_DIR` included** (003). Ox runs
   git with the client's environment minus the repository-local variables.

## Current system

- Protocol: `crates/ox-protocol/src/messages.rs` — `Method::SessionNew
  { workspace_id, command, env }`, `Method::SessionClose { session_id }`,
  `WorkspaceInfo { id, path }`, `SessionInfo { cwd, … }`, `code::*`. The
  client is in `crates/ox-protocol/src/client.rs`.
- Daemon: `crates/oxd/src/daemon.rs`. `Daemon::new_session` is synchronous
  inside `self.update`, holding the registry's std mutex; Git work must not run
  there. `close_session` deletes the row, then `Session::close` records
  `SessionClosed` and aborts the agent task. `Daemon::new` calls
  `Store::remove_unstarted_sessions`. `remove_workspace` consults the registry.
- `main.rs` knows the socket path (`args.socket`), keeps the database at
  `<socket>.db`, and creates the socket's parent directory. `Daemon::new(store)`
  does not know the socket path.
- Session: `crates/oxd/src/session.rs`. `Session::spawn` builds the state and
  calls `launch`, which spawns `run`. `run` records `Change::Exited(error)` when
  the agent cannot start (line 466), leaving `Phase::Exited(None)`, which
  `load` refuses.
- Connection: `crates/oxd/src/conn.rs` handles requests one at a time, except
  `session.set_config_option`, which runs in its own task.
- Store: `crates/oxd/src/store.rs`. Tables are created with `IF NOT EXISTS`,
  and there are no migrations.
- Clients: `oxctl` (`new`, `close`, `ls`) and `crates/ox/src/lib.rs` (Tauri
  `new_session`).
- Tests: `crates/oxd/tests/daemon.rs` has the `Daemon` harness (`start`, and
  `start_in` for a restart on the same directory) and `fake_agent`;
  `agent_that_cannot_start` shows waiting for `agent_exited`.

## Design

### Ownership and the git runner

`oxd` owns every Git operation. Creation, removal and the branch rule live in
`crates/oxd/src/worktree.rs`; listing and fetch live in
`crates/oxd/src/branches.rs` (004). Clients say which checkout they want,
confirm discards, and pass their environment.

Git runs as the `git` CLI through `tokio::process::Command`. A `Git` value holds
the environment for one operation:

- for a client request, `oxd`'s environment overlaid with the request's `env`,
  as `AcpAgentConfig::envs` does for the agent (the same environment the
  session's agent gets);
- for startup cleanup, which has no client, `oxd`'s own environment;
- in both cases, without the repository-local variables `git rev-parse
  --local-env-vars` lists (`GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_CONFIG
  GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT GIT_OBJECT_DIRECTORY GIT_DIR
  GIT_WORK_TREE GIT_IMPLICIT_WORK_TREE GIT_GRAFT_FILE GIT_INDEX_FILE
  GIT_NO_REPLACE_OBJECTS GIT_REPLACE_REF_BASE GIT_PREFIX GIT_SHALLOW_FILE
  GIT_COMMON_DIR`), and with `GIT_TERMINAL_PROMPT=0`.

Every invocation runs `git -C <dir> …` with stdin null, puts `--end-of-options`
before user-supplied names and refs, and returns stdout, or an error carrying
the arguments and git's trimmed stderr. `git` is found on the `PATH` of that
environment. No Ox-level lock serializes creation or removal: git's ref locks
make branch creation atomic, and `worktree add` locks a new worktree while it
initializes.

Code structure: `Git { env }` with `Git::for_client(env)` and `Git::own()`, and
a private `run(dir, args)`. `GitError` keeps the arguments and stderr and
converts to `code::GIT`; validation maps git failures to `INVALID_REQUEST`.

### Protocol (version stays 1; every caller updated)

```rust
// session.new gains:
#[serde(default)] checkout: Checkout,

#[serde(tag = "type", rename_all = "snake_case")]
pub enum Checkout {
    #[default] Workspace,
    /// `branch: None` → new branch `ox/<id8>` from `start` (default HEAD).
    /// `branch: Some` → see "Branch resolution"; `start` only for a new branch.
    Worktree { branch: Option<String>, start: Option<String> },
}

// SessionInfo gains:
pub worktree: Option<WorktreeInfo>,
pub struct WorktreeInfo { pub path: PathBuf, pub branch: String,
                          pub base: String, pub created_branch: bool }

pub const GIT: i32 = 8; // a git command failed; message has git's stderr
```

`<id8>` is the first 8 hex digits of the session's UUID. `WorktreeInfo.path` is
the worktree's top level; `SessionInfo.cwd` may be a subdirectory of it.
`base` is the commit the session started at (see **Branch resolution**).

Requests that do Git work run in their own tasks in `conn.rs`, like
`session.set_config_option`, so a client disconnecting never cancels Git work.
This unit spawns `session.new` that way; 004 adds `workspace.branches` and 005
adds `session.delete`.

### Worktree location

`oxd` keeps everything it stores beside its socket: the database at
`<socket>.db`, as today, and worktrees in `<socket dir>/worktrees/`. With the
default socket (002) that is `$XDG_DATA_HOME/ox/worktrees/`. `make dev`, e2e
and tests point `OX_SOCKET` or `--socket` elsewhere, and their worktrees
follow. `Daemon` gains `worktrees_dir: PathBuf`, which `main.rs` passes as
`<socket dir>/worktrees`.

The worktree directory is `<socket dir>/worktrees/<name>-<id8>`, where `<name>`
is the final component of the workspace checkout's top level. The session's
`cwd` is that directory joined with the workspace's prefix within its
checkout.

If the directory already exists (a leftover `oxd` no longer knows about),
`session.new` refuses in the request phase with `INVALID_REQUEST` "`<path>`
already exists". It picks no other session ID, creates nothing, and leaves the
directory alone. An existing `ox/<id8>` branch is refused the same way, by
`git branch` itself.

`oxd` writes nothing into the repository except what git itself writes for a
worktree: its administrative directory in the common Git directory, and the
branch ref.

### Branch resolution

Run in the workspace's checkout. `top` and `prefix` come from `rev-parse
--show-toplevel --show-prefix`. A failure (not a repository, a bare repository,
inside `.git`, or a `.git` that git rejects) answers `INVALID_REQUEST`
"`<path>` is not in a Git working tree: <git's trimmed stderr>" (amended in
revision 4; see **Revision history**).

For `branch: None`: kind **New**, name `ox/<id8>`.

For `branch: Some(name)`: `git check-ref-format --branch <name>` must succeed
and print exactly `name`, else `INVALID_REQUEST` "`<name>` is not a valid branch
name". Then, in order:

1. `refs/heads/<name>` exists → **Existing**. If `worktree list --porcelain`
   shows it checked out (`branch refs/heads/<name>`), refuse with
   `INVALID_REQUEST` "`<name>` is checked out at `<path>`".
2. `name` is `<remote>/<rest>` for a remote listed by `git remote`, and
   `refs/remotes/<remote>/<rest>` exists. This is the user picking a remote
   branch from the suggestions. Result: **Track** local `<rest>` from
   `<remote>/<rest>`. If `refs/heads/<rest>` exists, refuse with "a local
   branch `<rest>` exists; name it instead".
3. Remotes `r` with `refs/remotes/<r>/<name>`. With exactly one, the result is
   **Track** local `<name>` from it. With several, use the one
   `checkout.defaultRemote` names if it is among them, as `git worktree add`
   does; otherwise refuse with "`<name>` exists on remotes `a`, `b`; set
   `checkout.defaultRemote` or create the local branch first".
4. Otherwise → **New** `<name>`.

`start` is allowed only for **New**; with any other kind, refuse with
`INVALID_REQUEST` "Start From applies only to a new branch". `base` is:

- for **New**, the resolved `start` (`rev-parse --verify <start or
  HEAD>^{commit}`);
- for **Existing**, the local tip;
- for **Track**, the remote tip.

With no `start` and an unborn `HEAD`, refuse with "the repository has no
commits". An unknown `start` gives "`<start>` is not a commit". When `prefix` is
not empty, `git cat-file -e <base>:<prefix>` must succeed, else refuse with
"`<prefix>` does not exist at `<base>`".

Branch creation: **New** runs `git branch --no-track --end-of-options <name>
<base>`, with no upstream even from a remote ref. **Track** runs `git branch
--track --end-of-options <local> <remote>/<local>`. Both are atomic; a branch
created meanwhile gives git's "already exists" as `INVALID_REQUEST`.
`created_branch` is true for **New** and **Track**, false for **Existing**.

### Creation

`session.new` with `Checkout::Worktree`. The **request phase** runs before the
answer, and every failure there refuses at once:

1. Branch resolution, validation, and the check that the worktree directory
   does not exist yet, as above.
2. Create the branch (**New**, **Track**).
3. Save the session row and the `worktrees` row in one transaction. On failure,
   delete a created branch (CAS at its tip) and answer `STORAGE`.
4. Under the registry lock, check the workspace still exists (else roll back as
   in 3 and answer `NOT_FOUND`), insert the session in `starting` with its
   `worktree`, and take the session's lifecycle guard (an owned guard on a
   fresh mutex, so it cannot block). Answer.

The **startup phase** runs in the session's startup task while it holds that
guard:

5. Create `<socket dir>/worktrees/`, then `git -C <top> worktree add <path>
   <branch>`.
6. Launch the agent with `cwd`, as `Session::spawn` does today, and release the
   guard.

If step 5 fails, roll back:

- `git worktree remove --force <path>`, if the path is registered;
- delete the branch if Ox created it (`update-ref -d refs/heads/<branch>
  <base>`, which leaves a moved branch alone);
- delete the `worktrees` row.

Then record `Change::Exited(Some(message))`, exactly as a failed agent launch
does. The session is `exited`, has no `worktree`, cannot load, and is deleted
by the user or at the next daemon startup. Rollback failures are added to the
message and logged.

`remove_workspace` refuses while the *store* has a session row for the
workspace, which covers a creation in flight.

Code structure:

- `worktree.rs` exposes async functions. `plan(git, workspace, checkout,
  worktrees_dir, id) -> Result<WorktreePlan, ErrorBody>` covers resolution,
  validation and the check that the path is free. It returns the path, cwd,
  branch, kind, base, and the remote for **Track**. The others are
  `create_branch(git, &plan)`, `add(git, &plan)` and `roll_back(git, &plan,
  added: bool)`.
- `Daemon::new_session` becomes async. It generates the session ID once and
  calls `worktree::plan`; an existing path is refused there, before anything is
  created. It then creates the branch, saves the rows, and inserts into the
  registry under the lock. The `Workspace` checkout keeps today's order.
- `Session` gains `lifecycle: Arc<tokio::sync::Mutex<()>>` and
  `worktree: Mutex<Option<SavedWorktree>>`. The second is cleared on rollback,
  so `SessionInfo.worktree` changes and the listing updates.
  `Session::spawn(.., prepare: Option<(Git, WorktreePlan)>)` takes
  `lifecycle.clone().try_lock_owned()`, which always succeeds on a new session.
  It spawns a startup task that holds the guard, runs `worktree::add`, and then
  either calls `launch` or rolls back and records `Change::Exited(Some(..))`.
  The guard drops when the task finishes either way. 005's delete and load take
  the same lock.
- `remove_workspace` asks `Store::workspace_has_sessions`.
- `oxctl new`: `--branch` and `--start` each imply `--worktree`. `started()`
  reports "the session did not start: <error>", since the error may now come
  from git.

### Persistence

```sql
CREATE TABLE IF NOT EXISTS worktrees (
    session_id     TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    path           TEXT NOT NULL,
    branch         TEXT NOT NULL,
    base           TEXT NOT NULL,
    created_branch INTEGER NOT NULL
);
```

`SavedSession.worktree: Option<SavedWorktree>` is loaded with a `LEFT JOIN`.
Because the database moved with the data directory (002), every user starts
with a new database. The table still uses `IF NOT EXISTS`, like the others.

### Load, restart, reconnect

- `session.load` refuses with `INVALID_STATE` "the session's directory `<cwd>`
  no longer exists" when `cwd` is not a directory.
- Shutdown and idle exit keep rows and worktrees. Startup restores worktree
  sessions as `exited` with their `worktree`.
- Reconnecting clients get `worktree` in every `SessionInfo`.
- Startup cleanup of unstarted worktree sessions is 005's.

### Edge cases (creation)

| Case | Behaviour |
| --- | --- |
| Not a Git repo, bare repo, inside `.git` | `session.new` refuses (004 adds the flag that lets the app hide the items). |
| Workspace is a linked worktree | `top` is its top level; worktrees share its common dir. |
| Workspace is a subdirectory | Same subdirectory in the worktree; refused at once if absent at `base`. |
| Existing local branch | Checked out, `created_branch` false, so 005 never deletes it. |
| Existing branch checked out elsewhere | Refused at once, naming where. |
| Name only on a remote | Local tracking branch created; 005's branch rule applies to it. |
| Name on several remotes | `checkout.defaultRemote`, else refused. |
| `remote/name` typed with local `name` present | Refused, naming the local branch. |
| Invalid name, unknown start, unborn `HEAD`, start with existing branch | Refused at once, nothing created. |
| Dirty or detached source | Worktree starts at the commit; uncommitted changes stay behind. |
| Submodules | Not initialized (005's removal checks initialized ones). |
| Concurrent creation, one new name | One succeeds; the other is refused; the winner's branch is untouched. |
| Worktree directory exists | Refused at once, naming it; nothing created; directory untouched. |
| `worktree add` fails | Rollback; session `exited` with git's error. |
| Worktree deleted outside Ox | Load refused (005: delete unregisters it). |

## Check your assumptions

All entries are settled. Entries 1–5 were settled as entries for the whole
request and belong to this unit; entries 6–7 are this unit's own. Their
numbers changed when the former shared design was split.

1. **Confirmed.** **The worktree has committed files only.** New branches have
   no upstream; a branch created from a remote branch tracks it.
2. **Confirmed.** **There is no `.worktreeinclude` copying and no submodule
   initialization.** `.worktreeinclude` is a separate later task.
3. **Confirmed.** **A subdirectory workspace runs in the same subdirectory of
   the worktree**, and is refused when that subdirectory is absent.
4. **Confirmed.** **Worktrees follow `oxd`'s socket: `<socket
   dir>/worktrees/`.** With the default socket that is
   `$XDG_DATA_HOME/ox/worktrees/`, as asked. Under `make dev`
   (`target/dev/oxd.sock`), e2e and tests, worktrees land beside those
   sockets, which keeps test runs out of the user's data directory.
   Alternative: always `data_dir()/worktrees`, which would put every test
   run's worktrees in the user's real data directory.
5. **Confirmed.** **Typing `origin/feature` checks out a local `feature`
   tracking it.** Typing `feature` that exists only on several remotes follows
   git's `checkout.defaultRemote` and is otherwise refused. Typing
   `origin/feature` when a local `feature` exists is refused, naming `feature`.
   Start From with an existing or remote branch is refused rather than
   ignored.
6. **Confirmed.** **A failed `git worktree add` leaves the session in the list
   as `exited`, showing git's error.** The user deletes it, or the next daemon
   start removes it. This matches how a failed agent launch looks today.
   Alternative: remove the session automatically, which hides why it failed.
7. **Confirmed, as changed by the user.** **When the worktree directory for a
   new session already exists, `session.new` refuses at once with
   `INVALID_REQUEST` naming the directory.** No session appears, no other
   session ID is tried, nothing is created, and the directory is left alone.

## Implementation sequence

1. **Protocol**: `Checkout`, `WorktreeInfo`, `SessionInfo.worktree`,
   `code::GIT`, and `checkout` on `SessionNew`. Round-trip tests check that an
   absent `checkout` decodes as `Workspace`. `Client::new_session(workspace_id,
   command, env, checkout)`. Update the callers: `oxctl`,
   `crates/ox/src/lib.rs` (which passes `Workspace` until 006), and the
   `daemon.rs` tests.
2. **Store**: the `worktrees` table; `SavedWorktree { path, branch, base,
   created_branch }`; `add_session(.., worktree)` in one transaction; the `LEFT
   JOIN` in `sessions()`; `delete_worktree(session_id)`;
   `workspace_has_sessions`. Unit tests: round trip with and without a
   worktree, cascade on session delete, `delete_worktree`, and
   `workspace_has_sessions`.
3. **`worktree.rs`**: the runner, and the functions of **Branch resolution**
   and **Creation**, as above.
4. **Daemon and session**: `worktrees_dir` from `main.rs`; the async
   `new_session`; `Session::spawn` with `prepare`; the new `Session` fields and
   `info_of`; the `load` directory check (`INVALID_STATE`); the spawned
   `conn.rs` arm.
5. **oxctl**: the flags and the `started()` wording.
6. **Docs**: in `docs/architecture.md` "Workspaces and saved sessions", the
   checkout choices, branch kinds, worktree location, two creation phases, the
   row, and the `load` check. Close behaviour stays as is until 005. Add doc
   comments on the new protocol items.

## Verification

New `crates/oxd/tests/worktrees.rs`. Copy the harness pieces it needs from
`daemon.rs`, since test files share no module. Fixtures live in the file:

- a `git` helper;
- `repo()`, a tempdir with `git init -b main`, an identity in its config and
  one commit;
- `clone_with_remote()`, a clone of a bare remote that has branch `feature`.

Each test that checks a started worktree session first waits for
`agent_started`. Each failure in the startup phase waits for `agent_exited`.

- **Default**: the answer is `starting` with `worktree` set, then
  `agent_started`. `cwd` is `<socket dir>/worktrees/<name>-<id8>`, the branch
  is `ox/<id8>` at the checkout's `HEAD`, and `created_branch` is true. The
  repository has no new files (`git status --porcelain --ignored` is
  unchanged), and `git worktree list` shows the path.
- **New named branch from `origin/feature` via `start`**: created at that
  commit, with no `branch.<name>.remote`.
- **Existing local branch**: checked out, `created_branch` false, `base` its
  tip. **`main`, checked out in the workspace**: refused at once, naming the
  workspace path.
- **Remote only**:
  - `feature` gives local `feature` tracking `origin/feature`
    (`branch.feature.remote` is `origin`), and typing `origin/feature` gives
    the same;
  - `origin/feature` with a local `feature` is refused;
  - with a second remote that also has `feature`, the request is refused,
    naming both;
  - the same request tracks `origin` when `checkout.defaultRemote=origin` is
    set only in a config file named by the client env's `GIT_CONFIG_GLOBAL`.
    This also proves git uses the client's environment.
- **`start` with an existing or remote branch**: refused.
- **Subdirectory workspace**: `cwd` is the same subdirectory. With `start` at a
  commit lacking it, refused at once, with no branch created.
- **Refused with nothing created**: not a repository; a bare repository; names
  `-x`, `HEAD` and `a..b`; an unknown `start`; an unborn `HEAD`. Assert the
  code, that `git branch --list` and `git worktree list` are unchanged, and
  that no session is listed.
- **Concurrent creations of one new name** (`tokio::join!`): one answers
  `starting` and the other is refused. After `agent_started`, the winner's
  branch points at its base and its worktree exists.
- **Startup failure**: make `<socket dir>/worktrees` read-only (`0555`) before
  the request. The answer is `starting`, then `agent_exited` with git's
  message. `worktree` is `None`, the branch is gone, and after a restart the
  session is not listed.
- **Linked-worktree workspace**: the directory name uses the linked worktree's
  top-level name.
- **Dirty source**: an uncommitted file in the workspace is absent from the
  worktree.
- **Repository-local variables removed**: with `GIT_DIR=/nonexistent` in the
  client env, creation still succeeds.
- **Load after the directory is deleted** (restart first, so the session is
  `exited`): `INVALID_STATE` naming the directory.
- **Restart**: `SessionInfo.worktree` is unchanged.
- **Not tested** (the work log names them):
  - the existing-directory refusal, because the client cannot know the session
    ID, and so the directory name, in advance;
  - a store failure after branch creation;
  - `remove_workspace` during a creation, which the store test covers.

Commands: `make check-rust`, then `make smoke-claude`.

## Handoff

Ready. Size: one normal assignment, made of a creation mechanism with branch
resolution, a two-phase startup and a substantial Git integration suite.
Revision 3 does not change the assignment.

Detail: `plan_metrics.py` measures 4,096 words (121 fenced), above the
600–1,500 band. The extra length is the request, the scope-to-unit map, the
Hypercube comparison and the contracts later units rely on, which the former
shared design held and which had to move here unchanged. The work assigned did
not grow, and the implementation already follows this text, so it is not
trimmed.

## Revision history

- **Revision 4 (2026-11-12, after 003 was committed).** The user decided on a
  finding from review-004 (`.agents/reviews/2026-11-12-004-workspace-branches.md`).
  When git's repository check fails (`rev-parse --show-toplevel`, or
  `--git-common-dir` in 004), the `INVALID_REQUEST` refusal keeps its code and
  its message, and appends git's trimmed stderr: "`<path>` is not in a Git
  working tree: <git's message>". An example is git's dubious-ownership
  refusal. Committed 003 dropped git's message, which contradicted the
  confirmed behaviour that a `.git` git rejects fails with git's reason.

  The fix to `worktree::plan` (`not_a_working_tree`) is carried in the diff of
  `2026-11-12-004-workspace-branches`, not in 003's commit. Only the
  **Branch resolution** wording above changed; the other decisions are as
  committed.
- **Revision 5 (2026-11-12).** This revision records a change made by
  `2026-11-12-005-remove-session-worktrees` (its revision 4, at the user's
  decision on a review-005 plan finding).
  - The `worktrees` row gains `common_dir TEXT NOT NULL`, the repository's
    absolute common Git directory.
  - `worktree::plan` records `common_dir` at creation, with `rev-parse
    --path-format=absolute --git-common-dir`.
  - It stays inside `oxd`; `WorktreeInfo` on the wire is unchanged.
  - Removal and the branch rule use it, so they no longer depend on the
    workspace directory.

  The change is carried in 005's diff. The **Persistence** schema above shows
  the table as 003 committed it.
