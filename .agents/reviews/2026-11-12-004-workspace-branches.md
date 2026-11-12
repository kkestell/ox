# Review: 2026-11-12-004-workspace-branches

## Round 1

- Task: `2026-11-12-004-workspace-branches`
- Author: xreview reviewer, Claude Code, Opus 5.5 (`claude-opus-5-5`);
  coordinator ID `review-004` (child of Hypercube session `2ea441ae`);
  native conversation ID `578a67c9-ec46-4f62-b305-3cffeddf24dc`
- Independence: I did not write this code; the implementer was `impl-004`.
- Reviewed change: `/Users/kyle/projects/ox/.hypercube/2ea441ae` on
  `hypercube/session-2ea441ae`, `git diff HEAD` against `815240a` plus the
  untracked `crates/oxd/src/branches.rs`; plan
  `.agents/plans/2026-11-12-004-workspace-branches.md` (blob `5d7b58a`, as
  the log records) with its contract from
  `.agents/plans/2026-11-12-003-create-session-worktrees.md`; work log
  `.agents/work/2026-11-12-004-workspace-branches.md`

### Verdict

accept: the protocol, `SavedWorkspace` split, branch listing, coalesced fetch
with process-group timeout, working-tree flag and docs follow the plan; the
tests assert the behaviour through the client, and the required checks passed
on the final tree.

Invariants checked:

- **Coalescing** (`daemon.rs` `Daemon::fetch`): the std mutex guard lives only
  for the `let … = match` statement, with no `await` inside. The spawned task
  publishes the outcome before removing the entry, and `watch::Receiver::wait_for`
  checks the current value before awaiting, so a waiter that clones the
  receiver after the send, or after the sender drops, still gets the outcome.
  A request that arrives between the send and the removal gets that fresh
  outcome without fetching, which is harmless.
- **Fetch process** (`branches.rs` `fetch`): stdout is null, so only stderr is
  piped, and it is drained alongside `wait`, so git cannot block on a full
  pipe. The pid is read before any wait, so `killpg` targets an unreaped
  group leader. `oxd` is started under `setsid` (`client.rs:540`), so a fetch
  has no controlling terminal to stop on, and `GIT_TERMINAL_PROMPT=0` with
  stdin null leaves git nothing to prompt on.
- **Refusal**: `common_dir` adds `--show-toplevel` to `--git-common-dir`, so a
  bare repository and a path inside `.git` are refused even without `fetch`,
  as the 003 contract requires; the undo check covers it.
- **Flag**: `list` computes it after dropping the registry lock;
  `add_workspace` computes it under the lock, which costs only a few `stat`s.

### Plan findings

1. **The plan's edge-case row contradicts the 003 refusal contract the
   implementation follows.** The 004 plan's edge-case table says that for a
   "`.git` present but rejected by git", "`session.new` and
   `workspace.branches` refuse with git's reason". The 003 contract, which the
   004 plan also cites, maps any `rev-parse --show-toplevel` failure to
   `INVALID_REQUEST` "`<path>` is not in a Git working tree"
   (`worktree.rs` `plan`, `branches.rs` `common_dir`), discarding git's
   stderr. Consequence: for a repository git refuses, such as one with
   "detected dubious ownership" on an external or shared drive, the app shows
   the worktree items (flag true). Choosing one, or opening the branch list,
   then says the directory "is not in a Git working tree", which is false and
   gives no reason. The user should decide which wording holds. Keeping git's
   reason would mean refusing with git's stderr when `.git` exists, or always
   appending it to the message, in both `worktree::plan` and
   `branches::common_dir`. The work log does not list this as a deviation.

### Notes

- The coalescing test assumes the second request reaches the map before the
  released first fetch finishes (`worktrees.rs`
  `requests_for_one_repository_share_its_fetch`). Between the two points, the
  request has only a wrapper `exit`, `oxd` reading its output and a mutex.
  The fetch has to finish upload-pack negotiation, update its refs and exit.
  No product event lies between the map lookup and the wait, so enforcing the
  order would take a test-only seam, which AGENTS.md rules out. It was stable
  in 260 runs (see Verification); keep it as is.
- A fetch running when `oxd` exits (on a signal or at idle exit) is orphaned
  in its own process group and runs to completion. Without a terminal or a
  prompt it can only finish, fail, or hang on the network. Its ref updates
  are what a later fetch would have made, so this is acceptable as the log
  states it. Killing the group on shutdown would need `oxd` to track running
  fetches; nothing requires that now.
- A panicking fetch task would leave its map entry, and every later fetch of
  that repository would answer "the fetch stopped without an outcome"
  (`daemon.rs` `Daemon::fetch`). Nothing in the task can panic in practice: the
  `expect` on `child.id()` holds before any wait, and the mutex is never held
  across a panic. A drop guard that removes the entry would close this in a
  few lines, if it is ever wanted.
- A fetch that fails with empty stderr, such as one killed by an outside
  signal, reports `fetch_error: Some("")` (`branches.rs` `fetch`,
  `Ok(Ok(_))` arm). This is unlikely; falling back to the exit status would
  give the app something to show.
- The 60-second timeout and its `SIGTERM` are untested, as the plan settled
  and the log names.

### Verification

- Run: `target/debug/deps/worktrees-… --exact
  requests_for_one_repository_share_its_fetch`, 100 times serially: 0
  failures. Run because the log says the test assumes an ordering it does
  not enforce. Evidence: this conversation.
- Run: the same test as 10 rounds of 16 concurrent copies, with 14 `yes`
  processes holding all 14 CPUs: 0 failures in 160. Run to stress the
  assumed ordering under scheduling delay. Evidence: this conversation.
- Reused: `make check`: passed (fmt, clippy `-D warnings`, workspace tests
  including `worktrees` 25 passed, Biome, `tsc`, ruff), from
  `/tmp/ox-004-check.log` (10:59:24, after the last source edit at 10:58:44).
- Reused: `make smoke-claude`: passed through `close_and_idle_exit`, then
  `{"check": "passed"}`, from `/tmp/ox-004-smoke.log` (11:00:17).
- Reused: the log's seven undo checks, which I did not repeat. Each targets
  a behaviour whose test I read, and each failure it records matches that
  test's assertions.
- Not run: `make e2e`. Neither `crates/ox` nor the UI's TypeScript changed,
  and the app passes `WorkspaceInfo` through untouched.

## Round 2

- Task: `2026-11-12-004-workspace-branches`
- Author: xreview reviewer, Claude Code, Opus 5.5 (`claude-opus-5-5`);
  coordinator ID `review-004` (child of Hypercube session `2ea441ae`);
  native conversation ID `578a67c9-ec46-4f62-b305-3cffeddf24dc`
- Independence: I did not write this code; the implementer was `impl-004`.
- Reviewed change: the fix under "Repair round 1" in
  `.agents/work/2026-11-12-004-workspace-branches.md`. It touches
  `worktree::not_a_working_tree`, `GitError::stderr`, `worktree::plan`,
  `branches::common_dir`, the test
  `a_git_directory_git_rejects_is_refused_with_gits_reason`, and
  `docs/architecture.md`. Plan `.agents/plans/2026-11-12-004-workspace-branches.md`
  revision 3 (blob `79e61a6`, as the log records), with the revision 4 note in
  `.agents/plans/2026-11-12-003-create-session-worktrees.md`.

### Verdict

accept: both refusal sites now keep `INVALID_REQUEST` and the message, and
append git's trimmed stderr. That is the user's decision, as both plans now
state it. A new test drives both requests through the client against a `.git`
that git rejects.

The fix is correct in these respects:

- **Both `rev-parse` failure paths carry the reason**: `worktree::plan`
  (`session.new` with a worktree) and `branches::common_dir`
  (`workspace.branches`). The undo check in the log fails the test separately
  for each.
- **The fallback refusals give the bare message**: these are an output
  without a top-level name and an empty common-dir line. Git reported no
  error in either case, so it has no reason to append.
- **`GitError::stderr` is already trimmed**: `Git::run` trims stderr on a
  non-zero exit.
- **No caller compares the message exactly**: the earlier assertions in
  `worktrees.rs` use `contains`, and no UI or script code matches the text.

### Resolution of earlier findings

| Finding | Status | Evidence in current code |
| --- | --- | --- |
| Round 1 plan finding 1 (rejected `.git` refused without git's reason) | resolved as the user decided | `crates/oxd/src/worktree.rs` `not_a_working_tree(workspace, reason)`, called with `error.stderr()` from `plan` and from `branches.rs` `common_dir`. `a_git_directory_git_rejects_is_refused_with_gits_reason` asserts "is not in a Git working tree: fatal: gitfile does not point to a valid repository" from both requests. Both plans and `docs/architecture.md` state the rule. |

The round 1 notes stand unchanged; this fix does not touch them.

### Notes

- A plain directory or bare repository now also gets git's message appended.
  For example: "`<path>` is not in a Git working tree: fatal: not a git
  repository (or any of the parent directories): .git". The two halves say
  the same thing, as the decision implies.
- When git cannot be started, for example because it is not on the client's
  `PATH`, the spawn error becomes the reason: "`<path>` is not in a Git
  working tree: No such file or directory (os error 2)". The workspace
  framing is wrong for that case, but the message now names the cause, where
  before it named nothing. This is unlikely with a client's real `PATH`.
- The dubious-ownership case is not tested, as the log states. Producing it
  needs root, and the rejected-`gitfile` case exercises the same path.

### Verification

- Reused: `make check`: passed, `worktrees` 26 passed, including
  `a_git_directory_git_rejects_is_refused_with_gits_reason ... ok`, from
  `/tmp/ox-004-check.log` (11:10:23, after the last code edit at 11:09:35).
- Reused: `make smoke-claude`: `{"check": "passed"}`, from
  `/tmp/ox-004-smoke.log` (11:11:43).
- Reused: the log's two undo checks, one per refusal site. I did not repeat
  them; each matches the test's assertion on the request it names.
- Not run: `make e2e`. `crates/ox` and the UI's TypeScript are unchanged.
