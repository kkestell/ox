# Move a branch and compare a session's branch

## Goal

The local merge workflow ([git-workflows](../specs/git-workflows.md)) needs two `oxd` primitives that do not exist yet, usable from `oxctl` before the app has a UI for them:

- **Move a branch.** Set a branch to a commit, fast-forward only or forced, failing if the branch is no longer at the commit the caller expects. When a checkout has the branch, the move updates that checkout's files and index too. It refuses uncommitted changes to tracked files there, an untracked file it would overwrite, and a session's agent there that is starting, in a turn, or waiting for an answer.
- **Compare.** A worktree session has a comparison ref, such as `main`. `oxd` publishes, with the session, the branch's tip, the ref's commit, and the counts of commits ahead of and behind the ref. It recomputes them when the session's agent ends a turn, after a branch move, a session delete or a fetch in the repository, and when a client asks.

When this is done, the user can run local merge by hand: `oxctl new --worktree --compare main`, let the agent commit, see `1` ahead and `0` behind in `oxctl compare`, and `oxctl move-branch` `main` to the session's tip.

## Related code

- `crates/ox-protocol/src/messages.rs` — `Method` (line 25), `Checkout` (line 189), `WorktreeInfo` (line 223), `SessionInfo` (line 326), `code` (line 439), and the serialization tests at the end of the file.
- `crates/ox-protocol/src/client.rs` — one method per request, such as `branches` (line 314) and `new_session` (line 328).
- `crates/oxd/src/branches.rs` — `common_dir`, `list` and `fetch`: the repository-level operations on branches. Move and compare join them.
- `crates/oxd/src/worktree.rs` — `Git`, `Repository` (line 161, private), `checked_out_at` (line 496, private), `plan` (line 262), and `unsaved` (line 656), whose `git status` check is the uncommitted-changes test the move reuses.
- `crates/oxd/src/daemon.rs` — `handle` (line 202), `new_session` (line 381), `fetch` (line 497), `delete_session` (line 540), `clean_up` (line 112).
- `crates/oxd/src/session.rs` — `Session::new` (line 97) spawns the parker task; `park_when_idle` (line 134) is the pattern for a per-session task; `update` (line 531) sees every new event and signals `changed` when `SessionInfo` differs; `info_of` (line 329); `close` (line 505).
- `crates/oxd/src/store.rs` — schema (line 67), `sessions` (line 183), `add_session` (line 232).
- `crates/oxd/src/conn.rs` — the list of methods that run in their own task (line 134).
- `crates/oxctl/src/main.rs` — `Command::New` (line 41, mapping at line 201).
- `crates/oxd/tests/worktrees.rs` — helpers `repo`, `clone_with_remote`, `new_session_running`, `answers`, `commit_file`, and the fake agent's `wait` directive for a turn that stays open.

## Decisions

- **Protocol.**

  ```rust
  /// Sets local branch `branch` of the workspace's repository to commit `to`. Refused,
  /// changing nothing, when the branch is not at `expected`, or when `to` does not
  /// descend from `expected` unless `force` is set. When a checkout has the branch,
  /// its files and index move with it; the move is refused with `UNSAVED_WORK` while
  /// tracked files there have uncommitted changes, and refused while a session's
  /// agent there is starting, in a turn, or waiting for an answer.
  #[serde(rename = "branch.move")]
  BranchMove { workspace_id: String, branch: String, to: String, expected: String,
               #[serde(default)] force: bool, #[serde(default)] env: BTreeMap<String, String> },

  /// Sets or clears a worktree session's comparison ref and answers the session with
  /// its comparison recomputed. Refused for a session with no worktree and for a ref
  /// that names no commit.
  #[serde(rename = "session.set_compare")]
  SessionSetCompare { session_id: String, compare: Option<String> },

  /// Recomputes the session's comparison and answers the session.
  #[serde(rename = "session.refresh")]
  SessionRefresh { session_id: String },
  ```

  `Checkout::Worktree` gains `#[serde(default)] compare: Option<String>`, validated in `worktree::plan` like `start` (`{compare} is not a commit`). `WorktreeInfo` gains `compare: Option<String>`, the ref as the client gave it. `SessionInfo` gains `comparison: Option<Comparison>`, unset when the session has no worktree or no comparison ref:

  ```rust
  #[serde(tag = "type", rename_all = "snake_case")]
  pub enum Comparison {
      /// `tip` is the branch's commit, `compare_tip` the ref's.
      Compared { tip: String, compare_tip: String, ahead: u64, behind: u64 },
      /// Git could not compare them, such as when the ref or the branch is gone.
      Failed { error: String },
  }
  ```

  `compare_tip` is what a client passes as `expected` when it fast-forwards the ref's branch to `tip`. The new methods are added to `Client` and to the methods `conn.rs` runs in their own task.

- **Computing a comparison.** `branches::compare(git, worktree) -> Comparison` runs in the recorded common Git directory through `Repository`: `rev-parse --verify` of `refs/heads/<branch>^{commit}` and `<compare>^{commit}`, then `rev-list --left-right --count <compare_tip>...<tip>`, whose two numbers are behind and ahead. It always runs with `Git::own()`: it is local, read-only git, and several of its triggers have no client.

- **Recomputing.** `Session` gets `comparison: Mutex<Option<Comparison>>`, read by `info_of`; `comparing: tokio::sync::Mutex<()>`; and `recompare: tokio::sync::Notify`.
  - `Session::compare(&self)` holds `comparing`, computes, and stores the result through `update`, so a changed comparison reaches every watch. Holding the lock means a comparison that read git later is also published later.
  - `Session::recompare(&self)` calls `recompare.notify_one()`, which coalesces triggers that arrive while a comparison runs.
  - A task spawned in `Session::new` for a session with a worktree, beside the parker and aborted with it in `close`, runs `compare` once and then once per notification. A new session and every session restored at `oxd` start therefore get a comparison without a client asking.
  - `update` calls `recompare` when the events it takes include `TurnEnded`.
  - `Daemon::recompare(common_dir)` calls `recompare` on every session whose worktree has that common Git directory. `branch.move` calls it after a move, `delete_session` and `clean_up` after the branch rule, and `Daemon::fetch` after the outcome arrives, whether or not the fetch succeeded.
  - `session.set_compare` and `session.refresh` await `compare` directly so their answer carries the new comparison.

- **Storing the ref.** A new table `compare_refs (session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE, ref TEXT NOT NULL)`, created with `IF NOT EXISTS`, so an existing database opens unchanged. `sessions` reads it with a `LEFT JOIN` into `WorktreeInfo.compare`; `add_session` writes it in the same transaction as the worktree row; `Store::set_compare(session_id, Option<&str>)` replaces or deletes the row. `session.set_compare` writes the row, then updates `Session.worktree`.

- **Moving.** `branches::move_branch` in the workspace's repository:
  1. Resolve `to` and `expected` to commits (`… is not a commit`), and refuse with `INVALID_STATE` when `refs/heads/<branch>` is not at `expected` (`main is at <x>, not <expected>`).
  2. Unless `force`, refuse with `INVALID_REQUEST` when `merge-base --is-ancestor expected to` fails (`<to> is not a fast-forward of main`).
  3. Find the checkout with the branch with `checked_out_at`, made `pub(crate)` with `Repository`.
  4. With a checkout: the daemon refuses with `INVALID_STATE` (`session <id> is busy in <path>`) when a session whose canonical `cwd` is inside the checkout's canonical path is `starting`, `running`, `awaiting_permission` or `awaiting_input`. `move_branch` then refuses with `UNSAVED_WORK` when `git status --porcelain --untracked-files=no --ignore-submodules=none` in the checkout prints anything, and runs `update-index -q --refresh` there.
  5. `update-ref -m "ox: move <branch>" refs/heads/<branch> <to> <expected>`, which fails if the branch moved since step 1.
  6. With a checkout: `read-tree -u -m <expected> <to>` in it, which updates the index and files and refuses to overwrite an untracked file. If it fails, `update-ref refs/heads/<branch> <expected> <to>` puts the branch back and the move answers git's error with `code::GIT`.

  The busy check is in the daemon because it reads sessions; the git steps are in `branches.rs`. A prompt that arrives between the busy check and step 6 is not held back; the window is the few milliseconds the git commands take. Submodule checkouts are not updated, as with `git merge` without `--recurse-submodules`.

- **`oxctl`.**
  - `oxctl new … --compare <ref>`, which requires one of `--worktree`, `--branch` and `--new-branch`.
  - `oxctl compare <session> [<ref>] [--clear]`: with a ref it sets it, with `--clear` it clears it, with neither it refreshes. It prints `<ref>\t<tip>\t<compare tip>\t<ahead>\t<behind>`, prints nothing when there is no comparison ref, and fails with the error of a `failed` comparison.
  - `oxctl move-branch --workspace <id> <branch> <to> --expected <commit> [--force]`.

## Check your assumptions

1. **Comparisons run with `oxd`'s own environment.** Kept.
   - **Decided:** Every comparison uses `Git::own()`, so `session.set_compare` and `session.refresh` take no `env`. Turn ends, startup and other sessions' triggers have no client environment to use, and comparing runs only local `rev-parse` and `rev-list`.
   - **Consequences:** A client whose `PATH` finds a different `git` than `oxd`'s gets comparisons from `oxd`'s `git`.
   - **Alternative:** Store each session's launch environment and compare with it.
   - **Trade-offs:** The decision keeps one code path and no stored environment. The alternative honours per-client git configuration that read-only commands barely use.

2. **A comparison ref must name a commit when it is set.** Kept.
   - **Decided:** `session.new` and `session.set_compare` refuse a ref that names no commit, as `start` is refused. After that, a ref that stops resolving shows as `Comparison::Failed` with git's error.
   - **Consequences:** `--compare origin/main` needs a fetched `origin/main` at creation.
   - **Alternative:** Accept any string and let the first comparison report the failure.
   - **Trade-offs:** The decision catches typos when they are made. The alternative lets a client set a ref it is about to fetch.

3. **`oxctl move-branch` requires `--expected`.** Kept.
   - **Decided:** The flag is required, as the protocol's `expected` is.
   - **Consequences:** Moving a branch by hand takes the branch's current commit, from `git rev-parse` or `oxctl compare`'s `compare_tip`.
   - **Alternative:** Default `--expected` to the branch's current commit, read by `oxctl`.
   - **Trade-offs:** The decision keeps the check meaningful from the CLI. The alternative is shorter to type, and races only with another move made at the same moment.

## Test plan

In `crates/oxd/tests/worktrees.rs`, against real repositories:

- **Move, no checkout:** a branch no checkout has fast-forwards to a descendant; with a wrong `expected` it is refused with `INVALID_STATE` and stays put; to a commit that is not a descendant it is refused with `INVALID_REQUEST`, and with `force` it moves.
- **Move, checkout:** moving the workspace checkout's `main` to a session branch's tip updates the checkout's files and index, and `git status` is clean afterwards.
- **Move, unsaved work:** a modified tracked file in the checkout refuses the move with `UNSAVED_WORK` and changes nothing. An untracked file the move does not touch lets it through. An untracked file at a path the move adds fails it with `GIT`, and the branch is back at `expected`.
- **Move, busy agent:** a Session in Checkout whose turn is held open with the fake agent's `wait` refuses the move with `INVALID_STATE`. Once the turn is cancelled and its `turn_ended` arrives, the same move succeeds. A worktree session moved to a forced commit has its worktree updated.
- **Compare at creation:** `session.new` with `compare: Some("main")` gives a `Compared` comparison with `ahead` and `behind` 0 and both tips at `HEAD`, delivered on a watch. A ref that names no commit is refused and nothing is created.
- **Compare at turn end:** a commit made in the worktree, then a prompt, gives `ahead` 1 once the watch delivers the session after `turn_ended`.
- **Compare after a move:** a session comparing with `main` sees `behind` change on the watch after `branch.move` moves `main`, with no other request.
- **Compare after a fetch:** in a clone, a session comparing with `origin/main` sees `behind` 1 after a commit is pushed to the remote and `workspace.branches` fetches.
- **Compare after a delete:** a session comparing with another session's `ox/<id8>` branch shows `Failed` after that session is deleted and its branch goes.
- **Refresh and set:** a commit on `main` outside Ox shows after `session.refresh`; `session.set_compare` changes the ref and the comparison, `None` clears both, and a session with no worktree is refused.
- **Restart:** a session's comparison ref survives an `oxd` restart, and its comparison is published without a request.
- `crates/oxd/src/store.rs`: a session round-trips with its comparison ref, `set_compare` replaces and clears it, and deleting the session deletes the row.
- `crates/ox-protocol/src/messages.rs`: `branch.move`, `session.set_compare`, `session.refresh`, `Checkout::Worktree` with `compare`, and `Comparison` serialize as documented; `Checkout::Worktree` without `compare` still deserializes.

## Implementation plan

1. `ox-protocol`: add `Comparison`, the three methods, `Checkout::Worktree.compare`, `WorktreeInfo.compare`, `SessionInfo.comparison`, their serialization tests, and the `Client` methods `move_branch`, `set_compare` and `refresh`. Update every `Checkout::Worktree` constructor.
2. `store.rs`: the `compare_refs` table, the join in `sessions`, the insert in `add_session`, and `set_compare`, with tests.
3. `worktree.rs`: make `Repository` and `checked_out_at` `pub(crate)`; validate `compare` in `plan` and carry it into `WorktreePlan::saved`.
4. `branches.rs`: `compare` and `move_branch`. Update the module comment.
5. `session.rs`: the comparison fields, `compare`, `recompare`, the comparing task, the `TurnEnded` trigger in `update`, `comparison` in `info_of`, and `set_compare`.
6. `daemon.rs`: `handle` arms for the three methods; `move_branch` with the busy check; `recompare(common_dir)` and its calls in `move_branch`, `delete_session`, `clean_up` and `fetch`. Add the methods to the task list in `conn.rs`.
7. `oxctl`: `new --compare`, `compare`, `move-branch`.
8. Tests in `crates/oxd/tests/worktrees.rs`.

## Documentation updates

- `docs/architecture.md`: the oxctl paragraph gains `--compare`, `compare` and `move-branch`; the Worktrees section gains the comparison (what is published, when it is recomputed, `compare_refs`) and `branch.move` (the expected-commit check, fast-forward or forced, updating a checkout, its refusals).
- `docs/worktrees.md`: sections on comparing a session's branch and on moving a branch, with the `oxctl` commands.
- `.agents/todo.md`: check off **Daemon and CLI**.
