# Deleting a worktree session removes its worktree and Ox's branch without losing work

- Task: `2026-11-12-005-remove-session-worktrees` (numbered 004 before the
  renumbering)
- Status: ready (revision 4; see **Revision history** at the end). Revision 3
  absorbed the former shared design's
  material on removal, delete, startup cleanup, the `session.delete` rename,
  their edge cases and their settled entries. The wording is unchanged, and the
  design was then deleted.
- Source HEAD: planned at `e7fdf22c675479f9f2fa328afe285c173ca99618`; executes
  on the commit of `2026-11-12-004-workspace-branches`
- Author: planner child of Hypercube session 2ea441ae, Claude Code, Opus 5.5
  (claude-opus-5-5); coordinator ID `planner`; native conversation ID
  `5ce61adf-7d6c-4b85-ab78-5167733b9e23`
- Dependencies: `2026-11-12-003-create-session-worktrees`. It runs after
  `2026-11-12-004-workspace-branches`, but uses nothing from it.

## Outcome and boundaries

This is correction A and the cleanup half of the session checkout request
recorded in `2026-11-12-003-create-session-worktrees`.

`session.close` becomes `session.delete { session_id, discard, env }`,
answering `Deleted { kept_branch }`. For a worktree session it refuses with
`UNSAVED_WORK` while work would be lost, unless the request sets `discard`.
Otherwise it stops the agent, removes the worktree, and applies the branch rule
to a branch Ox created. A delete during startup waits for the startup to
finish. After `oxd` reports `ready`, a background cleanup removes the
worktrees of sessions that never started, safely, or leaves them listed for
deletion. Clients never wait on that cleanup to connect. Removal and the
branch rule work from the repository the worktree belongs to, so they do not
depend on the workspace directory still existing. `oxctl close` becomes `oxctl
delete [--discard]`.

The user will later add parking (agent stopped, worktree and branch kept) and
archiving (conversation kept read-only, worktree removed, branch always kept).
Neither is part of this unit. The removal operations are split so that archive
can reuse the first one.

Not in this unit:

- the app's dialogs and the Tauri command rename (006).

`AGENTS.md` is in scope: its example of a stuck session "closed with
`session.close`" becomes "deleted with `session.delete`".

`crates/ox/src/lib.rs` changes only enough to compile: its `close_session`
command calls `Client::delete(.., false, utf8_env())`.

## Contract relied on from 003

- **The git runner**: `Git::for_client(env)` and `Git::own()` (`oxd`'s own
  environment, for startup cleanup). Both drop the repository-local `GIT_*`
  variables and set `GIT_TERMINAL_PROMPT=0`. `GitError` converts to `code::GIT`.
- **The `worktrees` row and `SessionInfo.worktree`**: `{ path, branch, base,
  created_branch }`.
  - `path` is the worktree's top level under `<socket dir>/worktrees/`, a
    directory Ox owns.
  - `base` is the commit the session started at.
  - `created_branch` is true only when Ox created the branch (**New** or
    **Track**).
  - The row cascades on session delete, and `Store::delete_worktree` deletes
    it alone.
  - This unit adds the repository's location to the row (see **Repository
    location**), changing 003's committed schema and `worktree::plan`. The
    change is carried in this unit's diff.
- **`Session.lifecycle`**: a `tokio::sync::Mutex<()>`. The startup task holds
  it from `session.new`'s answer until the worktree is added and the agent is
  launched, or until the rollback finishes.
- **A failed startup** leaves the session `exited`, with no agent and no
  `worktree`.
- **Requests that do Git work run in their own tasks** in `conn.rs`.
- **Protocol version stays 1**, and every caller is updated.

## Current system

As left by 003, plus:

- `Daemon::close_session` deletes the row under the registry lock. It then
  calls the synchronous `Session::close`, which records `Change::Closed` and
  calls `abort()` without awaiting. Dropping `AcpAgent`'s child guard SIGKILLs
  the process group, asynchronously to `abort()`. `close_all` (shutdown) uses
  `Session::close` and keeps rows.
- `Daemon::new` calls `Store::remove_unstarted_sessions`, and `main.rs` awaits
  it before binding the socket and printing `ready`.
  `Client::connect_or_start` gives up after `START_TIMEOUT` (5 s,
  `crates/ox-protocol/src/client.rs`). `Session::restore`
  returns `None` without an `agent_session_id`, and `State::load` refuses
  `Phase::Exited(None)`.
- Callers of close:
  - `oxctl` (the `close` subcommand, `started()`);
  - `scripts/smoke-acp.py` (`ctl("close", ..)` twice);
  - `ui/e2e/helpers.ts` (`resetDaemon`);
  - `crates/oxd/tests/daemon.rs` (`close_*` tests and helpers);
  - `crates/ox/src/lib.rs`;
  - `docs/architecture.md` and `docs/acp-v2.md`, and `README.md` if it names
    close;
  - `AGENTS.md:15`.

## Design

### Protocol

```rust
// session.close is renamed:
#[serde(rename = "session.delete")]
SessionDelete { session_id: String, #[serde(default)] discard: bool,
                #[serde(default)] env: BTreeMap<String, String> },
// answers:
pub struct Deleted { pub kept_branch: Option<String> }

pub const UNSAVED_WORK: i32 = 7; // delete refused; retry with discard
```

The rename covers the `Method` variant and its serde name, `Client::delete`,
`oxctl delete`, the Tauri `delete_session` command and the UI's **Delete
Session** button (006), plus docs and tests. The `SessionClosed` event and
`End::SessionClosed` keep their names, because shutdown also ends sessions'
subscriptions with that event without deleting them. `Session::close`, the
internal "stop and end subscriptions", also keeps its name.

### Repository location

Each `worktrees` row records `common_dir`, the repository's absolute common Git
directory. `worktree::plan` reads it at creation, with `rev-parse
--path-format=absolute --git-common-dir` added to its existing `rev-parse`. The
store keeps it in a new `common_dir TEXT NOT NULL` column. It stays inside
`oxd`: the store's worktree type carries it, and `WorktreeInfo` on the wire is
unchanged.

Repository-level commands (worktree removal, listing, and the branch rule) run
with `--git-dir=<common_dir>` from that directory. That turns off repository
discovery, so a directory that replaced the repository is never mistaken for an
enclosing one. Worktree-level commands (the unsaved checks) run with `git -C
<path>`. Neither depends on the workspace directory. Verified with git 2.56:
`worktree remove --force` of an existing or a missing worktree, `worktree list`,
`rev-list` and `update-ref -d` all work with `--git-dir=<common_dir>`.

Two probes decide what removal can do:

- **R**, the repository is found: `git --git-dir=<common_dir> rev-parse
  --git-dir` succeeds.
- **W**, the worktree is usable: the directory exists and `git -C <path>
  rev-parse --git-dir` succeeds.

The table is new on this branch and has no migration, by the store's rule that
a schema change means deleting the database. A database written by a 003 or 004
build has the table without `common_dir`, so worktree creation against it fails
with `STORAGE` until that database is deleted.

### Removal: two operations

`worktree.rs` exposes two operations, so a later archive can remove a worktree
while always keeping its branch. Neither knows about sessions or workspaces.

**`remove_worktree(git, worktree, discard) -> Result<()>`** removes the
checkout, unless that would lose uncommitted work the user has not agreed to
discard. It calls `unsaved` itself unless `discard`. `unsaved(git, worktree) ->
Result<Vec<String>>` lists:

- "uncommitted changes in `<path>`", when `git -C <path> status --porcelain
  --untracked-files=all --ignore-submodules=none`, run with
  `GIT_OPTIONAL_LOCKS=0`, prints anything. Ignored files do not count, and they
  are deleted.
- "commits on no branch in `<path>`", when `rev-list --count HEAD --not <base>
  --branches --remotes --tags` > 0 (detached-`HEAD` work).
- "submodule `<p>` has commits on no remote branch", for each initialized
  submodule when `.gitmodules` exists. The submodules come from `submodule
  foreach --quiet --recursive 'printf "%s\0" "$displaypath"'`; then
  `rev-list --count HEAD --branches --not --remotes` runs in each.
- When the directory exists but not W:
  - without R: "the repository of `<path>` no longer exists, so Ox cannot
    check it for unsaved work" (the repository was deleted or moved);
  - with R: "git no longer recognizes `<path>` as a worktree, so Ox cannot
    check it for unsaved work: <git's message>" (its administrative directory
    was removed outside Ox).

Then, depending on what is on disk:

1. **W.** Unless `discard`, `unsaved` must be empty, else `UNSAVED_WORK` with
   the list. Then `git --git-dir=<common_dir> worktree remove --force <path>`.
   One `--force` is enough: the check stands in for git's own, and without it
   git refuses any worktree with initialized submodules. A locked worktree still
   refuses, giving `GIT`.
2. **The directory exists, but not W.** `UNSAVED_WORK` unless `discard`. With
   `discard`, `oxd` deletes the directory itself (`remove_dir_all`), since it
   owns it, and continues as in case 3.
3. **The directory is missing.** With R, if `git --git-dir=<common_dir> worktree
   list --porcelain` lists the path, `worktree remove --force <path>`
   unregisters it. Otherwise there is nothing to do.

**`apply_branch_rule(git, worktree) -> Result<Option<String>>`** returns the
branch it kept, and runs entirely with `--git-dir=<common_dir>`:

- It returns `None` when `created_branch` is false.
- Without R, it returns `Some(branch)`. Ox can neither check nor delete the
  branch, so it reports the branch as left in place rather than saying nothing.
  The branch survives if the repository was moved.
- It returns `None` when the branch no longer exists.

Otherwise, with tip `T`, it deletes the branch when both of these hold:

- `rev-list --count T --not <base> --exclude=<branch> --branches --remotes
  --tags` is 0;
- no worktree has the branch checked out.

It deletes with `update-ref -d refs/heads/<branch> T` and returns `None`. In
every other case it keeps the branch and returns `Some(branch)`. For a
**Track** branch, the remote branch counts, so an unpushed commit keeps it.

### Delete

`session.delete { session_id, discard, env }` for a session with a worktree:

1. Take the session's lifecycle lock. A delete during startup waits for the
   worktree and agent launch to finish or fail. A load or a second delete waits
   for the delete.
2. Unless `discard`, if `unsaved` is not empty, answer `UNSAVED_WORK`. The
   session and its agent are untouched.
3. `Session::stop`: abort the agent task, **await** it, and record
   `Change::Exited(None)`.
4. `remove_worktree(discard)`, then `apply_branch_rule`. A refusal (work written
   between steps 2 and 3) or a `GIT` failure answers the error. The session
   stays `exited` and loadable.
5. Delete the rows, remove the session from the registry, record
   `SessionClosed`, and answer `Deleted { kept_branch }`.

A session without a worktree is deleted as `session.close` closed it, answering
`kept_branch: null`; `discard` and `env` go unused.

### Startup cleanup

`oxd` never makes a client wait on cleanup:

1. `Daemon::new(store, worktrees_dir)` reads the rows, and runs no git. It is
   synchronous again.
   - Unstarted rows without a worktree are deleted, as today.
   - Unstarted rows with a worktree (a crash mid-startup, or a launch that
     failed) are restored as `exited` with no agent (`State::unstarted`).
     `Daemon::new` takes each one's lifecycle guard (`try_lock_owned` on its
     fresh mutex, which cannot block) and keeps the sessions with their
     guards for cleanup.
2. `main.rs` binds the socket, prints `ready`, and then spawns
   `Daemon::clean_up`.
3. `clean_up` takes the sessions one at a time and runs `remove_worktree(false)`
   and `apply_branch_rule` with `Git::own()`, `oxd`'s own environment.
   - On success, it deletes the session as a delete's last step does: the row,
     the registry entry, and `SessionClosed`.
   - On refusal or failure, it logs the error, and the session stays `exited`
     with no agent, so it can only be deleted.
   - Either way, it then drops that session's guard.
4. While cleanup runs, clients list those sessions as `exited`. A delete or
   load of one waits on its lifecycle lock. After a successful cleanup it
   answers `NOT_FOUND`. Otherwise the delete proceeds as usual, and the load
   answers `INVALID_STATE`.
5. The daemon does not idle-exit while cleanup runs: `is_idle` is false until
   `clean_up` ends, and the end signals `changed`. A shutdown by signal does
   not wait for cleanup. Sessions not yet cleaned stay saved and are cleaned at
   the next start.

### Edge cases

| Case | Behaviour |
| --- | --- |
| Uncommitted changes at delete | `UNSAVED_WORK`; the app asks; `discard` removes. |
| Commits only on the Ox branch | Worktree removed, branch kept, reported. |
| Existing branch the session checked out | Never deleted (`created_branch` false). |
| Tracking branch Ox created | Branch rule applies; an unpushed commit keeps it. |
| Submodules | Initialized ones are checked for unpushed commits. |
| Crash mid-startup | Startup cleanup. |
| Worktree deleted outside Ox | Delete unregisters it (load is refused by 003). |
| Workspace directory gone (for example a subdirectory workspace whose directory was removed from the main checkout) | Delete and cleanup work as usual, from `common_dir`. |
| Worktree's registration removed outside Ox | Delete asks for discard, then removes the directory itself. |
| Repository deleted or moved | Delete asks for discard, then removes the directory itself; the Ox branch is reported as kept. |
| Many or slow cleanups at startup | `ready` comes first; those sessions show `exited` until cleaned. |
| Locked worktree | `GIT`; session stays `exited`. |
| Agent processes outside its process group | May write into a removed directory; out of scope. |

### Structure

- **`Session::stop`** (async): take the task handle, `abort()`, await it
  (ignoring cancellation), then record `Change::Exited(None)`. The state
  machine ignores that for a session already exited. `Session::close` keeps
  its synchronous form for `close_all` and for the final step of a delete.
- **`Daemon::delete_session(id, discard, env)`** (async) follows **Delete**. It
  looks up the session and takes `lifecycle.lock()`. For a worktree session it
  runs the pre-check, `stop`, `remove_worktree` and `apply_branch_rule` with
  `Git::for_client(env)`. It no longer looks up the workspace's path. It then deletes the row, removes the registry entry
  under the lock, and calls `Session::close`. A session without a worktree
  runs only that last step.
- The `session.load` handler holds `lifecycle` across `Session::load`, so a
  load waiting on a successful delete then answers `NOT_FOUND`.
- **Startup**: as in **Startup cleanup**. Unstarted worktree rows are restored
  with a new `State::unstarted(session_id, title)` (`Phase::Exited(None)`).
  `Daemon` holds the pending sessions and their guards until `clean_up` takes
  them. The registry carries a flag that keeps `is_idle` false while cleanup
  runs. The store method `remove_unstarted_sessions` and its test go.
- **`conn.rs`**: `SessionDelete` joins the spawned arm.
- **oxctl**: the `delete` subcommand with `--discard`, which prints `kept branch
  <name>` when one is reported. `started()` deletes with `discard: true`,
  since the agent never ran there.

## Check your assumptions

Entries 1–5 are settled. Entries 1–3 were numbered 1, 8 and 12 among the
entries for the whole request; entries 4–5 are this unit's own, numbered
004-1 and 004-2 before the renumbering. Entry 5 now reflects the user's
revision-4 decision that cleanup runs after `ready`. Entries 6–8 were added
in revision 4 and confirmed by the user, entry 6 with a change.

1. **Confirmed.** **Deleting a worktree session removes the whole worktree,
   including ignored files**, after the unsaved check or a confirmed discard.
2. **Confirmed.** **Deleting a worktree whose repository was deleted or moved
   asks for discard first**, because git can no longer tell whether anything
   in it is unsaved. On confirmation, Ox deletes the directory itself.
3. **Confirmed.** **Only the request is renamed.** The `session_closed` event
   keeps its name, because shutdown also ends subscriptions with it.
4. **Confirmed.** **There are two checks.** If the second one refuses after the
   agent was stopped, the session is left `exited`.
5. **Confirmed, and changed in revision 4.** **Restart cleanup keeps dirty
   unstarted worktree sessions as `exited`**, and they can only be deleted.
   Cleanup now runs in the background after `ready`. Until it reaches a
   session, that session shows `exited`, and a delete or load of it waits.
6. **Confirmed, as changed by the user.** **Deleting a session whose
   repository was deleted or moved reports its Ox branch as kept.** Ox cannot
   tell whether the branch still exists: it survives in a moved repository and
   is gone with a deleted one. 006's kept-branch notice is neutral ("Ox kept
   the branch `<name>`."), so it is true here and for an unmerged branch
   alike.
7. **Confirmed.** **Deleting a worktree whose registration was removed outside Ox asks for
   discard**, as a missing repository does, because git can no longer check it.
   Alternative: delete it without asking, which could lose uncommitted files.
8. **Confirmed.** **A database written by a build of 003 or 004 must be
   deleted**
   (`<socket>.db`; for example `~/.local/share/ox/oxd.sock.db`, or
   `target/dev/oxd.sock.db` under `make dev`). Until it is, creating a worktree
   session fails with `STORAGE`, because its `worktrees` table lacks
   `common_dir`. The user confirmed that all Ox state is disposable: there are
   no migrations, and any database may be deleted.

## Implementation sequence

1. **Protocol and every caller**: rename to `SessionDelete` (`session.delete`)
   with `discard` and `env`; add `Deleted` and `code::UNSAVED_WORK`;
   `Client::delete(session_id, discard, env) -> Deleted`. Update the callers:
   - `oxctl`;
   - `crates/ox/src/lib.rs` (compile-level change only);
   - the `daemon.rs` tests, renaming the `close_*` tests to `delete_*`;
   - `scripts/smoke-acp.py`, which runs `delete`, with the report key
     `close_and_idle_exit` becoming `delete_and_idle_exit`;
   - `ui/e2e/helpers.ts`, where `resetDaemon` runs `oxctl delete --discard`.
2. **Repository location** (003's code): the `common_dir` column and store
   type, and `worktree::plan` recording it; the store tests follow.
3. **`worktree.rs`**: `unsaved`, `remove_worktree` and `apply_branch_rule`, with
   the R and W probes and `--git-dir=<common_dir>`.
4. **Session and state**: `stop`, `State::unstarted`, and `restore` for
   unstarted rows.
5. **Daemon, conn, main**: `delete_session`, the `lifecycle` lock in load,
   `Daemon::new` with the pending cleanups, `clean_up` spawned after `ready`,
   the idle flag, and the spawned arm.
6. **Docs**:
   - `docs/architecture.md` uses delete wording throughout. It states the
     delete rules, `discard`, kept branches, waiting on startup, and cleanup
     after `ready`. It replaces 003's interim close sentence.
   - In `docs/acp-v2.md`, the `session/close` row's oxd note says deleting a
     session kills the agent's process group. The bridge's own ACP
     `session/close` is unrelated and keeps its name.
   - `AGENTS.md:15`: "A session stuck in startup is deleted with
     `session.delete`, the method a UI uses, …".
   - Add doc comments on the protocol items.

## Verification

Add to `crates/oxd/tests/worktrees.rs`, using 003's harness and fixtures. A
test that checks the agent stopped first confirms it was running (the fake
agent's `pid` directive), then that the process is gone.

- **Clean delete**: the directory and its registration are gone, `ox/<id8>` is
  deleted, `kept_branch` is `None`, and the repository is unchanged.
- **Ox branch with a commit merged into `main`**: deleted.
- **Ox branch with a commit on no other ref**: the worktree is removed, the
  branch is kept, and `kept_branch` names it, also with `discard`.
- **Existing-branch session with a new commit**: the branch remains, and
  `kept_branch` is `None`.
- **Tracking branch** with an unpushed commit: kept. After `git push` from the
  worktree: deleted.
- **Untracked file** written by the test: `UNSAVED_WORK` naming the worktree,
  with the agent still alive and the session still listed. Then `discard`
  removes the worktree, and the agent is gone.
- **Detached `HEAD` with a new commit**: `UNSAVED_WORK`.
- **Submodule** (fixture with `protocol.file.allow=always`), initialized in the
  worktree, with a commit not on its remote: `UNSAVED_WORK`. Initialized and
  clean: removed.
- **Worktree directory deleted outside Ox**: delete succeeds, and `git worktree
  list` no longer shows it.
- **Repository moved away**: `UNSAVED_WORK` mentioning the repository. With
  `discard`, the directory is gone, the session is deleted, and `kept_branch`
  names the Ox branch, which still exists in the moved repository.
- **Workspace subdirectory gone**: a session in a subdirectory workspace. The
  test removes the subdirectory from the main checkout (`rm -rf`).
  - With the worktree untouched, delete succeeds, the worktree directory and
    its registration are gone, the Ox branch is deleted, and `kept_branch` is
    `None`.
  - With a commit in the worktree, delete succeeds and `kept_branch` names the
    branch, which still exists.
- **Registration removed outside Ox**: the test deletes
  `<common_dir>/worktrees/<name>`. Delete answers `UNSAVED_WORK` with "no longer
  recognizes". With `discard`, the directory is gone and the session deleted.
- **Locked worktree** (`git worktree lock`): `GIT`, and the session is
  `exited`. After `git worktree unlock`, it loads.
- **Ox branch checked out in another worktree** the test adds: kept.
- **Delete during startup**: a delete sent as soon as `session.new` answers
  succeeds. Afterwards the worktree directory, its registration and the branch
  are all gone.
- **Startup cleanup**: the command is a missing executable, as in
  `agent_that_cannot_start`. Wait for `agent_exited`, then restart.
  - A clean worktree session disappears from the listing (watch until it is
    gone), with its worktree and branch.
  - One with an untracked file stays listed `exited`, `load` answers
    `INVALID_STATE`, and `delete` with `discard` removes it.
- **Cleanup after `ready`**: startup cleanup uses `oxd`'s own environment, so
  the harness gains a start that sets `oxd`'s `PATH` with `BlockedGit`'s
  directory first (`BlockedGit::new(" worktree remove ")`). Make one clean
  unstarted worktree session, stop `oxd`, and start it with that `PATH`.
  - The start returns (`ready` arrived), `blocked()` resolves (cleanup is
    held), and a client connects and lists the session `exited`.
  - The client sends `session.load` for it, and the test then releases git.
    The load answers `NOT_FOUND`, the session leaves the listing, and the
    worktree and branch are gone.
  - With no client connected, `oxd` is still running two idle periods after
    `blocked()`, and exits once idle after the release.
- **Contract change**: a store test round-trips `common_dir`.
- The renamed `daemon.rs` tests pass.
- **Undo checks** (AGENTS.md):
  - removing the pre-check fails the untracked-file test's "agent still alive"
    assertion;
  - removing the startup removal fails the clean startup test;
  - dropping the lifecycle wait in delete makes the delete-during-startup test
    leave a worktree;
  - running cleanup before `ready` makes the cleanup-after-`ready` start time
    out;
  - running removal from the workspace instead of `common_dir` fails the
    workspace-subdirectory-gone test.
- **Untested races**, named in the work log: the second check after `stop`,
  awaiting the aborted task before git runs, the branch CAS, and load waiting
  on delete. The load in the cleanup-after-`ready` test is sent before the
  release, but a load that did not wait would usually be handled before the
  release too. The test passes reliably with the lock; without the lock it
  detects the bug almost always, but not provably always.

Commands: `make check-rust`, then `make smoke-claude`.

## Handoff

Ready once 004 is committed. Size: one normal assignment, made of one removal
mechanism, the delete path, a startup change and a substantial Git test set.
The rename is mechanical caller updates. Detail: the plan now carries its own
contract, which was formerly in the shared design. `plan_metrics.py`: 2,324
words (41 fenced), above the band. The rise from about 1,120 words is the
removal, delete and startup-cleanup contracts and edge cases moved in
unchanged. The work assigned did not grow. At revision 4 it is 3,677 words. The growth is the
repository-location mechanism, the background cleanup and their tests, which
add real work: a schema column, two probes, and a startup task.

## Revision history

- **Revision 4 (2026-11-12, after 005 was implemented to revision 3).** This
  revision follows the user's decisions on the two plan findings in
  `.agents/reviews/2026-11-12-005-remove-session-worktrees.md` (round 1), and
  its note on `AGENTS.md`.
  - Removal and the branch rule run from the repository's recorded common Git
    directory instead of the workspace directory. This adds the `common_dir`
    column, a change to 003's committed schema and `worktree::plan` that is
    carried in this unit's diff.
  - The branch rule reports the branch as kept when the repository cannot be
    found.
  - A worktree whose registration was removed outside Ox needs discard.
  - Startup cleanup moved from before `ready` to a background task that holds
    each session's lifecycle lock.
  - `AGENTS.md` joins the scope.

  The implementation of revision 3 changes accordingly.
