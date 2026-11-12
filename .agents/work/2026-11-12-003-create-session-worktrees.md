# oxd starts a session in a new worktree on a new, existing or tracking branch

- Task: `2026-11-12-003-create-session-worktrees`
- Status: complete
- Plan: `.agents/plans/2026-11-12-003-create-session-worktrees.md` (repository-relative), blob `da01692bd4a9c05760d5aa4b284285cd12578937`
- Guidance blobs: `.agents/designs/2026-11-12-002-session-checkout-choice.md` `027dd0704686dfb9732c6ac9dbf13a9a1a431c05`
- Source HEAD: `7c53b7e3e37579bd8d8068205569243011be818a` (unit 002 committed)
- Review: `.agents/reviews/2026-11-12-003-create-session-worktrees.md` (rounds 1–2)

## Changes

The design and structure are the plan's; this lists where they landed.

- Protocol (`crates/ox-protocol`): `Checkout` (`workspace` default,
  `worktree { branch, start }`), `checkout` on `session.new`, `WorktreeInfo`,
  `SessionInfo.worktree`, `code::GIT`; `Client::new_session` takes a
  `Checkout`. Round-trip tests cover an absent `checkout` (decodes as
  `Workspace`), an absent `branch`/`start`, and a listing with a worktree.
- Store (`crates/oxd/src/store.rs`): the `worktrees` table; `add_session`
  writes the session row and the worktree row in one transaction;
  `sessions()` reads them with a `LEFT JOIN`; `remove_worktree`;
  `workspace_has_sessions`. Unit tests for the round trip with and without a
  worktree, the transaction, the cascade on session delete, `remove_worktree`
  and `workspace_has_sessions`.
- Git runner and branch rule (`crates/oxd/src/worktree.rs`, new): `Git`
  (client env over oxd's, the 15 repository-local variables removed,
  `GIT_TERMINAL_PROMPT=0`, stdin null, `git -C`), `GitError` (arguments and
  trimmed stderr, converts to `code::GIT`), `plan` (resolution, validation,
  free-path check), `create_branch`, `add`, `roll_back`.
- Daemon (`crates/oxd/src/daemon.rs`, `main.rs`, `conn.rs`): `Daemon::new`
  takes the worktrees directory, `main.rs` passes the absolute
  `<socket dir>/worktrees`; `new_session` is async and, for a worktree, plans
  and creates the branch outside the registry lock, then saves the rows and
  inserts the session under it, deleting a created branch if that fails;
  `remove_workspace` asks the store; `session.new` runs in its own task in
  `conn.rs`.
- Session (`crates/oxd/src/session.rs`): `worktree` and `lifecycle` fields;
  `spawn` takes the worktree to add and runs a startup task holding the
  lifecycle guard, which adds the worktree and launches the agent, or rolls
  back, forgets the worktree row and records `AgentExited` with git's error
  and `worktree` cleared; `load` refuses a missing directory with
  `INVALID_STATE`.
- `oxctl new`: `--worktree`, `--branch`, `--start` (either of the last two
  implies a worktree); a failed start reads "the session did not start: …".
- `crates/ox/src/lib.rs` passes `Checkout::Workspace` (unit 006 adds the
  choice).
- Tests: `crates/oxd/tests/worktrees.rs` (new, 14 tests, real git repos and
  `oxd`); `crates/oxd/tests/daemon.rs` callers pass `Checkout::Workspace`.
- Docs: `docs/architecture.md` ("Workspaces and saved sessions" and a new
  "Worktrees" paragraph; `oxctl` flags).

Changed paths:

- `crates/ox-protocol/src/client.rs`
- `crates/ox-protocol/src/messages.rs`
- `crates/ox/src/lib.rs`
- `crates/oxctl/src/main.rs`
- `crates/oxd/Cargo.toml` (tokio `process` feature)
- `crates/oxd/src/conn.rs`
- `crates/oxd/src/daemon.rs`
- `crates/oxd/src/main.rs`
- `crates/oxd/src/session.rs`
- `crates/oxd/src/store.rs`
- `crates/oxd/src/worktree.rs` (new)
- `crates/oxd/tests/daemon.rs`
- `crates/oxd/tests/worktrees.rs` (new)
- `docs/architecture.md`
- `.agents/work/2026-11-12-003-create-session-worktrees.md` (this log)

## Deviations

- **No `SavedWorktree` type.** The store saves and returns the protocol's
  `WorktreeInfo`, which has exactly the planned fields, as it already does
  `WorkspaceInfo` for workspaces. This removes a duplicate struct and its
  conversion.
- **No `Git::own()`.** Nothing in this unit runs git without a client, and an
  unused constructor fails clippy's dead-code check; 005's startup cleanup
  adds it.
- **Rows and registry under one lock.** The design lists saving the rows
  (step 3) and the registry insert with the workspace check (step 4)
  separately; `add_session` does both inside the registry lock, as the
  `Workspace` checkout already did. Any failure there (workspace gone,
  `STORAGE`) deletes a created branch with the compare-and-swap. Behaviour is
  as designed.
- **Start-point refusal wording.** "a start point applies only to a new
  branch, and `<branch>` exists" rather than the design's "Start From applies
  only to a new branch", since "Start From" is the app's label and `oxctl`
  calls it `--start`. Error messages carry names and paths without literal
  backticks, like the daemon's existing messages.
- **Rollback removes the worktree only when its directory exists.** The plan's
  `roll_back(git, plan, added)` is kept; with `added`, it runs `git worktree
  remove --force <path>` when the path exists (git refuses a directory it did
  not register), rather than parsing `worktree list` and comparing paths that
  git reports resolved (`/private/var` against `/var` on macOS).
- **A `session.close` during startup does not interrupt git.** The startup
  task is not stored in the session's task slot, so `close` does not abort it
  mid-`git worktree add`. After the worktree is added, the task takes the task
  slot and launches the agent only if the session is still `starting`; a
  closed session stays closed with its worktree on disk, the plan's stated
  pre-005 behaviour.

## Verification

| Command | Result | Environment | Evidence |
| --- | --- | --- | --- |
| `cargo test -p oxd --test worktrees` | passed: 14 passed, run twice in parallel | macOS 26.7.2, git 2.56.0, rustc 1.96.0 | conversation `a2d2b02c-…` tool results |
| Mutation: `env_remove` loop disabled | `git_ignores_the_clients_repository_variables` failed ("… is not in a Git working tree"); restored | same | same |
| Mutation: client `envs` overlay removed | `a_branch_on_several_remotes_follows_the_default_remote` failed ("feature exists on remotes origin, upstream …"); restored | same | same |
| `make check` | passed (exit 0): fmt, clippy `-D warnings`, workspace tests (oxd unit 47, daemon 44, worktrees 14, ox-protocol 30, …), Biome, `tsc`, ruff | same | `/tmp/003-check.log` (local) |
| `make smoke-claude` | passed (exit 0): all 14 checks and `passed`, Haiku (`claude-haiku-5-5`) | same | `/tmp/003-smoke.log` (local) |
| `make check-rust` (rerun on the final tree) | passed (exit 0): unchanged counts | same | `/tmp/003-check-rust.log` (local) |

`make check` and `make smoke-claude` ran before a last change that only made
`WorktreePlan`'s fields other than `cwd`, and `Kind`, private. That change
alters no behaviour, and `make check-rust` was rerun after it.
| `make e2e` | not run | — | see Limitations |

## Session provenance

| Stage | Role/name | Coordinator ID | Harness | Model/effort | Native conversation ID |
| --- | --- | --- | --- | --- | --- |
| parent | xtask coordinator | `2ea441ae` | Claude Code (Hypercube) | claude-opus-5-5 | `57fda6b3-c0d1-4d32-a25b-993c5391c607` |
| plan | planner | `planner` | Claude Code (Hypercube child) | opus / high (Hypercube setting) | `5ce61adf-7d6c-4b85-ab78-5167733b9e23` |
| work, repairs 1–2 | xwork worker, child of Hypercube session 2ea441ae | `impl-003` | Claude Code (Hypercube child) | claude-opus-5-5, opus / high (Hypercube setting) | `a2d2b02c-9e8a-4e20-95aa-e1c8875b987c` |
| review rounds 1–2 | review-003 | `review-003` | Claude Code (Hypercube child) | claude-opus-5-5, opus / high (Hypercube setting) | `f980b822-6be1-425e-8390-4dee8289cecd` |

## Limitations

- **Not tested, as the plan names:** the refusal of an existing worktree
  directory (a client cannot know the session ID, so the directory name, in
  advance); a store failure after the branch is created (the branch
  compare-and-swap deletion on that path has no test); `workspace.remove`
  during a creation (covered only by the store's `workspace_has_sessions`
  test).
- **Untested: `session.close` while a worktree is being added.** No test
  closes a session mid-startup and checks that no agent launches afterwards.
- **The lifecycle lock has no other user yet.** The startup task holds it, as
  the plan says; nothing waits on it until 005's delete and load take it.
- **Pre-005 state:** `session.close` deletes a worktree session's rows and
  leaves its worktree and branch on disk, and the next daemon start deletes an
  unstarted worktree session's rows without removing its worktree.
- **`make e2e` not run.** The app's behaviour is unchanged: `crates/ox` passes
  `Checkout::Workspace`, which is today's `session.new`, and `oxctl new` as
  `ui/e2e/helpers.ts` calls it (`--workspace … -- <agent>`) behaves as before;
  only its failure message changed wording, which the helpers do not read.
- Test fixtures run git with `GIT_CONFIG_GLOBAL=/dev/null` and
  `GIT_CONFIG_NOSYSTEM=1`, and start `oxd` with the same, so the user's git
  configuration does not affect them. Every fixture repository has a
  `user.email`: without one, git looks up the host name for reflog entries,
  which took over 10 seconds per `worktree add` under parallel tests here.

## Records restructured

After this log was written, the shared design
`.agents/designs/2026-11-12-002-session-checkout-choice.md` was folded into
per-unit plans and deleted; its decisions for this unit now sit in
`.agents/plans/2026-11-12-003-create-session-worktrees.md` unchanged. The
design blob recorded above is the one this work read. Later units were
renumbered: branches 004, delete 005, app 006; the XDG unit is 002.

## Repair round 1

Review: `.agents/reviews/2026-11-12-003-create-session-worktrees.md`, round 1
(changes-required). The user approved fixing all of it; the reviewer's
`add_session` out-parameter remark is a non-blocking opinion and is left as
is.

- Findings addressed:
  1. **Rollback deleted a branch another worktree had checked out.**
     `roll_back` now deletes a created branch only when `git worktree list
     --porcelain` shows no `branch refs/heads/<branch>`, and still with the
     compare-and-swap at `base`. The worktree-list parse that `resolve` used is
     now `checked_out_at`, shared by both. Test
     `a_failed_worktree_leaves_a_branch_another_worktree_checked_out`: a `git`
     wrapper on the client env's `PATH` (`BlockedGit`, in the test file) stops
     session A's `worktree add` on a FIFO; the test checks `topic` out in
     another worktree, releases A, waits for A's `agent_exited`, and asserts
     that `topic` still exists and is still checked out there.
  2. **Track branch could be created at a commit other than its `base`.**
     `create_branch` now creates every new branch with `git branch --no-track
     --end-of-options <branch> <base>`, and for **Track** then runs `git branch
     --set-upstream-to=refs/remotes/<remote>/<branch> --end-of-options
     <branch>`. If setting the upstream fails, the branch is deleted
     (compare-and-swap at `base`) and the request answers `GIT`. Test
     `a_tracking_branch_starts_at_the_commit_it_records`: the wrapper stops
     the first `git branch`; meanwhile the test moves
     `refs/remotes/origin/feature` to a newer commit, as a fetch would; the
     session's recorded `base`, the branch's tip, and the old remote tip are
     equal, and `branch.feature.remote`/`merge` are `origin` and
     `refs/heads/feature`.
  3. **The failure path's `store.remove_worktree` is untested.** Added to
     Limitations below.
  4. **`workspace.remove` during a creation** is now tested, so it leaves the
     untested list. Test `a_workspace_removed_during_a_creation`, with the
     same wrapper: removed while `git branch --no-track` is stopped (before the
     rows are saved), the removal succeeds, the creation answers `NOT_FOUND`,
     the created branch is deleted, and no session or workspace is listed;
     removed while `worktree add` is stopped (rows saved), the removal is
     refused with `INVALID_STATE` and the session then starts. The first part
     also exercises the request-phase compare-and-swap deletion that a store
     failure after branch creation would use.
- Changed paths: `crates/oxd/src/worktree.rs`, `crates/oxd/tests/worktrees.rs`,
  this log.

Limitations, superseding the list above where they differ:

- **Not tested:** the refusal of an existing worktree directory, because a
  client cannot know the session ID, and so the directory name, in advance; a
  store failure after the branch is created, because a client cannot make the
  database fail (its branch deletion is the one the `NOT_FOUND` path above
  runs).
- **Not tested: the startup failure path's `store.remove_worktree`.** No
  client-visible behaviour depends on it in this unit: the listing reads the
  in-memory worktree, and a restart deletes the unstarted row. Unit 005's
  startup-cleanup tests must cover a session whose worktree could not be
  added, which is where a leftover row would matter.
- **Not tested: `session.close` while a worktree is being added.** With the
  check in place, no agent launches after the close, and that absence
  produces no event or state a test could wait on before asserting it.

### Verification, repair round 1

| Command | Result | Environment | Evidence |
| --- | --- | --- | --- |
| `cargo test -p oxd --test worktrees` | passed: 17 passed | macOS 26.7.2, git 2.56.0, rustc 1.96.0 | conversation `a2d2b02c-…` tool results |
| Mutation: `roll_back` deletes the branch whatever `checked_out_at` returns | `a_failed_worktree_leaves_a_branch_another_worktree_checked_out` failed at the `refs/heads/topic` assertion; restored (`cmp` identical) | same | same |
| Mutation: a Track branch created from `refs/remotes/<remote>/<branch>` by name | `a_tracking_branch_starts_at_the_commit_it_records` failed: branch tip `44f4222…` ≠ recorded base `fecf312…`; restored | same | same |
| `make check` | passed (exit 0): fmt, clippy `-D warnings`, workspace tests (worktrees 17, daemon 44, oxd unit 47, ox-protocol 30, …), Biome, `tsc`, ruff | same | `/tmp/003-r1-check.log` (local) |
| `make smoke-claude` | passed (exit 0): all checks and `passed`, Haiku | same | `/tmp/003-r1-smoke.log` (local) |
| `make e2e` | not run: the repair changes only `oxd`'s git handling and its tests, which the app and the e2e `oxctl` helpers do not reach | — | — |

## Repair round 2

Review: `.agents/reviews/2026-11-12-003-create-session-worktrees.md`, round 2,
finding 2.1 (Low).

- Finding addressed: **2.1, the undo in `create_branch` when
  `--set-upstream-to` fails was untested.** The undo now goes through
  `roll_back(git, plan, false)` instead of calling `delete_branch` directly,
  so it has the same checked-out guard and compare-and-swap as every other
  undo. Test `a_tracking_branch_whose_upstream_cannot_be_set_is_undone`:
  `BlockedGit::new(" --set-upstream-to")` stops the upstream step, the test
  deletes `refs/remotes/origin/feature` meanwhile, and after release the
  request answers `GIT`, no local `feature` exists, and no session is listed.
- Changed paths: `crates/oxd/src/worktree.rs`, `crates/oxd/tests/worktrees.rs`,
  this log.

### Verification, repair round 2

| Command | Result | Environment | Evidence |
| --- | --- | --- | --- |
| `cargo test -p oxd --test worktrees` | passed: 18 passed | macOS 26.7.2, git 2.56.0, rustc 1.96.0 | conversation `a2d2b02c-…` tool results |
| Mutation: the undo in `create_branch` removed | `a_tracking_branch_whose_upstream_cannot_be_set_is_undone` failed at the "no local `feature`" assertion; restored (`cmp` identical) | same | same |
| `make check-rust` | passed (exit 0): fmt, clippy `-D warnings`, workspace tests (worktrees 18, daemon 44, oxd unit 47, ox-protocol 30, …) | same | `/tmp/003-r2-check-rust.log` (local) |
| `make smoke-claude` | passed (exit 0): all checks and `passed`, Haiku | same | `/tmp/003-r2-smoke.log` (local) |

## Parent verification

- Verifier: parent coordinator, Hypercube session 2ea441ae, native
  conversation `57fda6b3-c0d1-4d32-a25b-993c5391c607`.
- Round 1 findings 1–3: verified against `crates/oxd/src/worktree.rs`
  (`roll_back` deletes only when `checked_out_at` finds no worktree on the
  branch, still by compare-and-swap; `create_branch` creates every new branch
  at `base` with `--no-track` and then sets the Track upstream) and the log's
  revised Limitations. Checks: `/tmp/003-r1-check.log` (`make check`, exit 0,
  no failures) and `/tmp/003-r1-smoke.log` (`passed`). The reviewer's round 2
  confirmed 1–3 resolved.
- Round 2 finding 2.1: verified. The upstream-failure undo now calls
  `roll_back(git, plan, false)`, and
  `a_tracking_branch_whose_upstream_cannot_be_set_is_undone` covers it; the
  log records it failing with the undo removed. Checks:
  `/tmp/003-r2-check-rust.log` (exit 0, no failures) and
  `/tmp/003-r2-smoke.log` (`passed`). `make check-ui` and `check-scripts`
  passed in round 1 and repair 2 changed only Rust.
- No further review round: under the current xtask rule, the 2.1 fix carries
  out the finding's correction without changing a design decision or
  contract.
- Notes from review, not fixed: `add_session`'s out-parameter could be a
  return type carrying the untaken worktree; a `BlockedGit` or `ExitWatch`
  FIFO open that never completes hangs the test rather than failing it
  (`tokio::net::unix::pipe` would make both cancellable).
