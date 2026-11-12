# Review: 2026-11-12-003-create-session-worktrees

## Round 1

- Task: `2026-11-12-003-create-session-worktrees`
- Author: xreview reviewer, child `review-003` of Hypercube session 2ea441ae,
  Claude Code, claude-opus-5-5 (effort as set by the harness, not exposed);
  native conversation ID `f980b822-6be1-425e-8390-4dee8289cecd`
- Independence: I did not write this code; the implementer was `impl-003`.
- Reviewed change: `/Users/kyle/projects/ox/.hypercube/2ea441ae`, branch
  `hypercube/session-2ea441ae`, uncommitted change against HEAD `7c53b7e`
  (`git diff HEAD` plus untracked `crates/oxd/src/worktree.rs` and
  `crates/oxd/tests/worktrees.rs`); plan
  `.agents/plans/2026-11-12-003-create-session-worktrees.md` (blob
  `da01692b…`, matches the log); design
  `.agents/designs/2026-11-12-002-session-checkout-choice.md` (blob
  `027dd070…`, matches); work log
  `.agents/work/2026-11-12-003-create-session-worktrees.md`.

### Verdict

changes-required: the startup-phase rollback can delete a branch that another
session's worktree has checked out, which breaks the design's rule that the
winner's branch is left alone (finding 1). A **Track** branch can also be
created at a different commit from its recorded `base` (finding 2). The rest
of the change matches the plan and design. The protocol shape is right, every
caller is updated, the store write is one transaction, the git environment is
handled as the design says, and the request-phase rollback is correct.

### Findings

1. **Medium: the rollback deletes a created branch that another worktree has
   checked out.** `crates/oxd/src/worktree.rs:446-455` (`roll_back`).
   Session A asks for a new branch `topic`. The request phase runs `git
   branch`, A answers `starting`, and A's `worktree add` runs later in its
   startup task. Request B for `topic` can arrive in that window. B resolves
   it as **Existing**, because `worktree list` does not show `topic` checked
   out yet, so B also answers `starting`. Both `worktree add`s then race. If
   B wins, A's add fails with "already used by worktree" and A's rollback
   runs `update-ref -d refs/heads/topic <base>`. The compare-and-swap passes
   because the branch has not moved, so the branch under B's worktree is
   deleted. I checked this with git 2.56.0 in a scratch repository:
   `update-ref -d` deletes a branch that a linked worktree has checked out,
   and that worktree then reports "No commits yet", so the next commit
   there would be a root commit. The compare-and-swap catches a branch that
   moved, but not one that another worktree has checked out.
   Fix: in `roll_back`, delete the branch only when `worktree list
   --porcelain` shows no `branch refs/heads/<branch>`, and otherwise leave
   it. The design's branch rule for 005 makes the same check. At this point
   B's add has already registered its worktree, because that registration is
   what made A's add fail, so the check is reliable here.
   Test: a deterministic integration test is possible through the client
   environment. The design says git is found on the `PATH` of that
   environment, so put a `git` wrapper on the env's `PATH` that blocks
   `worktree add` on a FIFO. While A is blocked, check out `topic` in
   another worktree, release A, wait for A's `agent_exited`, and assert that
   `topic` still exists. Without the fix, that test fails.
   `of_two_creations_of_one_branch_one_wins` does not reach this
   interleaving. In 90 runs (6 at a time) it did not flake, but if B ever
   resolves `same` as **Existing**, both requests answer `Ok`. The test
   would then fail even though the product behaved correctly, since one
   session would exit at startup.

2. **Low: a Track branch's `base` can differ from the commit the branch is
   created at.** `crates/oxd/src/worktree.rs:240-241` and `:391`.
   `plan` reads `base` from `refs/remotes/<remote>/<branch>`, and then
   `create_branch` creates the branch from that ref by name. A fetch that
   lands in between creates the branch at a newer commit than `base`. Unit
   004 makes this the normal flow: the dialog starts a background fetch, and
   by design creation never waits on it. When it happens, `WorktreeInfo.base`
   is wrong, a later startup-phase rollback's compare-and-swap fails and
   leaves the branch behind ("undoing it failed"), and 005's branch rule
   starts from the wrong `base`.
   Fix: create the branch at the exact commit and then set its upstream
   (`git branch --no-track --end-of-options <local> <base>`, then `git branch
   --set-upstream-to=<remote>/<local> <local>`). Alternatively, have
   `create_branch` read the new branch's tip back into the plan before the
   rows are saved.

3. **Low: the failure path's `store.remove_worktree` has no test, and the
   log does not list it as untested.** `crates/oxd/src/session.rs:129`.
   Nothing that clients can see depends on it before 005: the listing reads
   the in-memory `worktree`, and a restart deletes the unstarted row. Once
   005 lands, a leftover row would send startup cleanup and the branch rule
   to work on a branch that was already rolled back, and possibly on a
   branch of the same name created since. Fix: add it to the log's untested
   list, and have 005's startup-cleanup tests cover a session whose worktree
   could not be added.

### Notes

- Deviations: every one is justified. Reusing `WorktreeInfo` in place of
  `SavedWorktree` and dropping the unused `Git::own()` both remove code.
  Doing the rows and the registry insert under one lock keeps the workspace
  check and the insert atomic. Removing the worktree only when its path
  exists is sound, because git cleans up after its own failed `add`. Leaving
  `close` unable to interrupt git matches the design: 005's delete waits on
  the lifecycle lock.
- Untested list:
  - The existing-directory refusal and a store failure after the branch is
    created are fair to leave untested, since a client cannot choose the
    session ID or make the database fail.
  - The `workspace.remove`-mid-creation path (`daemon.rs:304`, rollback
    with `added = false`) and the request-phase compare-and-swap it shares
    with the store-failure path can be reached deterministically with the
    `PATH`-wrapper seam from finding 1: block `git branch` and remove the
    workspace meanwhile. The implementer should add that test while adding
    the seam.
  - `session.close` during `worktree add` is justified as untested: with
    the fix in place, no agent launches, and that absence has no event a
    test could wait on. The log should give that reason.
- Leaving the lifecycle lock without a waiter until 005 follows the plan, and
  the log names it.
- Design opinion, not blocking: `add_session(&mut Option<(Git,
  WorktreePlan)>)` hands the worktree back to the caller for rollback by
  leaving it in the out-parameter. A return type that carries the
  untaken worktree with the error would state that directly.
- Skipping `make e2e` is justified. `crates/ox` passes
  `Checkout::Workspace`, and the UI does not depend on the order between the
  `session.new` response and the listing push (`ui/src/SessionList.tsx:50`).

### Verification

- Run: a scratch git 2.56.0 repository in a temporary directory (not the
  task checkout). `update-ref -d refs/heads/same <base>` deleted a branch
  checked out in a linked worktree, and a second `worktree add` of that
  branch failed with "already used by worktree". This establishes finding 1.
- Run: `target/debug/deps/worktrees-* of_two_creations_of_one_branch_one_wins`,
  60 times sequentially, then 90 times with 6 at a time: 0 failures. This
  checks whether the race in finding 1 already makes the existing test flaky.
- Reused: `make check`, passed (exit 0), from `/tmp/003-check.log`;
  `make check-rust` on the final tree, passed (exit 0; worktrees 14 passed
  in 5.82 s, daemon 44, oxd unit 47), from `/tmp/003-check-rust.log`;
  `make smoke-claude`, passed (all checks, `passed`), from
  `/tmp/003-smoke.log`; and the two mutation checks recorded in the work log
  (conversation `a2d2b02c-…`).
- Not run: `make e2e`, for the reason in the Notes.

## Round 2

- Task: `2026-11-12-003-create-session-worktrees`
- Author: xreview reviewer, child `review-003` of Hypercube session 2ea441ae,
  Claude Code, claude-opus-5-5 (effort as set by the harness, not exposed);
  native conversation ID `f980b822-6be1-425e-8390-4dee8289cecd`
- Independence: I did not write this code or the repair; the implementer was
  `impl-003`.
- Reviewed change: the repair recorded under "Repair round 1" in
  `.agents/work/2026-11-12-003-create-session-worktrees.md`, in the same
  checkout and branch. Scope: the fix and what it touches:
  `create_branch`, `roll_back`, `checked_out_at`, `delete_branch` and
  `resolve` in `crates/oxd/src/worktree.rs`; `BlockedGit` and the three new
  tests in `crates/oxd/tests/worktrees.rs`; and the revised untested list.

### Verdict

changes-required, for one Low item: the repair adds a rollback path to
`create_branch` with no test, and the log does not list it as untested
(finding 2.1). Findings 1.1 and 1.2 are fixed, and their tests fail without
the fixes. Finding 1.3 is now named in the log. Adding that one test would
make this an accept.

### Findings

1. **Low: the rollback `create_branch` runs when `--set-upstream-to` fails
   has no test.** `crates/oxd/src/worktree.rs:398-412`. This path is new in
   the repair: if setting the upstream fails, the branch just created is
   deleted (compare-and-swap at `base`) and the request answers `GIT`.
   Removing the `delete_branch` call fails no test, so a regression would
   leave a branch behind on a refused request, which breaks "refused at
   once, nothing created". AGENTS.md requires a test for each behaviour, and
   the log neither tests this path nor lists it as untested. A test is cheap
   with the existing seam: `BlockedGit::new(" --set-upstream-to")`, delete
   `refs/remotes/origin/feature` while it is stopped (as a pruning fetch
   would), release it, then assert code `GIT`, no local `feature`, and no
   session listed.

### Resolution of earlier findings

| Finding | Status | Evidence in current code |
| --- | --- | --- |
| 1.1 Rollback deletes a branch another worktree has checked out | resolved | `worktree.rs:444-471`: `roll_back` deletes only when `checked_out_at` returns `None`, keeps the branch when it returns `Some`, and reports a failed `worktree list`. Test `a_failed_worktree_leaves_a_branch_another_worktree_checked_out` follows the round-1 scenario exactly (A stopped at `worktree add`, `topic` checked out elsewhere, A released) and asserts that the branch survives and stays checked out. The log records that the mutation makes it fail. |
| 1.2 Track `base` differs from the branch's creation commit | resolved | `worktree.rs:383-412`: every new branch is created at the exact `base` with `--no-track`, then Track sets `--set-upstream-to=refs/remotes/<remote>/<branch>`. Test `a_tracking_branch_starts_at_the_commit_it_records` moves the remote ref while `git branch` is stopped and asserts that `base` equals the branch tip and the old remote tip, and checks `branch.feature.remote`/`merge`. The log records that the mutation makes it fail. |
| 1.3 Startup failure path's `store.remove_worktree` untested | resolved (named) | Repair Limitations names it and gives the reason; unit 005's startup-cleanup tests must cover it. |

### Notes

- `checked_out_at` (`worktree.rs:355`) is a straight extraction: `resolve`
  keeps the same refusal and message (`:273`), and `roll_back` reuses it.
  Neither caller changed meaning.
- `create_branch`'s undo calls `delete_branch` directly, without the
  checked-out check that `roll_back` makes. Another session would have to
  resolve and add a worktree for the branch within the few milliseconds
  between `git branch` and `--set-upstream-to`, all inside the request
  phase. Calling `roll_back(git, plan, false)` there would close that gap
  and use one undo path. Optional.
- `BlockedGit` is reliable for these tests:
  - Its patterns match only the intended invocation. `" branch "` does not
    match `check-ref-format --branch`, `" worktree add "` does not match
    `worktree list`, and `mkdir once` stops only the first matching call.
  - Each test gets its own directory, so parallel tests do not interfere.
  - The `blocked` FIFO reports a positive event before the test acts, as
    AGENTS.md asks.
  - One weakness: if a regression keeps the wrapper from ever reaching a
    FIFO, the `spawn_blocking` open stays blocked after the timeout fires,
    and dropping the runtime waits for it, so the test hangs instead of
    failing. `ExitWatch` in `crates/oxd/tests/daemon.rs` already works the
    same way, so this is a project-wide pattern rather than something this
    repair introduced. `tokio::net::unix::pipe` would make both cancellable.
- `a_workspace_removed_during_a_creation` covers both sides of the
  request/startup split, and its first half exercises the request-phase
  compare-and-swap deletion. Taking that item off the untested list is
  justified.
- The revised untested list is sound, apart from finding 2.1.

### Verification

- Run: `target/debug/deps/worktrees-5c8cad83820d4a2b` (built from the
  current tree by `cargo test -p oxd --test worktrees --no-run`), the full
  suite 12 times in a row: 12/12 passed, 17 tests each, about 6 s per run.
  The new tests rely on FIFOs and a `PATH` wrapper, so this checks them for
  flakiness.
- Reused: `make check`, passed (exit 0; worktrees 17 passed in 5.87 s), from
  `/tmp/003-r1-check.log`; `make smoke-claude`, passed (`passed`), from
  `/tmp/003-r1-smoke.log`; the two repair mutation checks recorded in the work
  log.
- Not run: `make e2e`. The repair touches only `oxd`'s git handling and
  its tests.
