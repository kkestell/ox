# Review: 2026-11-12-005-remove-session-worktrees

## Round 1

- Task: `2026-11-12-005-remove-session-worktrees`
- Author: reviewer child of Hypercube session 2ea441ae, Claude Code, Opus 5.5
  (claude-opus-5-5), effort not exposed; coordinator ID `review-005`; native
  conversation ID `03ec4c88-ff96-47d0-bbc6-827a50794455`
- Independence: I did not write this code; the implementer was `impl-005`
  (native conversation `dced0850-ced1-4dc9-95f8-253cad931641`).
- Reviewed change: `/Users/kyle/projects/ox/.hypercube/2ea441ae`, branch
  `hypercube/session-2ea441ae`, `git diff HEAD` against `882dcce`; plan
  `.agents/plans/2026-11-12-005-remove-session-worktrees.md` (blob
  `4995e0c28b9e04e98670cb5d66144c73662db938`, matching the log), with the
  contracts of `.agents/plans/2026-11-12-003-create-session-worktrees.md`;
  work log `.agents/work/2026-11-12-005-remove-session-worktrees.md`.

### Verdict

accept: the change implements the plan's delete, removal, branch rule and
startup cleanup faithfully, every caller is renamed, and the required checks
passed on the final tree. Two plan findings go to the user; neither is a
defect in carrying out the plan.

### Findings

No blocking findings. The invariants that carry the data-loss risk hold:

- **Two unsaved checks.** `Daemon::delete_session` checks before
  `Session::stop` and returns `UNSAVED_WORK` with the session untouched
  (`crates/oxd/src/daemon.rs:486`). `remove_worktree` checks again after the
  stop unless `discard` (`crates/oxd/src/worktree.rs`, `remove_worktree`),
  so work written in between refuses with the session `exited`. `unsaved`
  counts untracked files (`--untracked-files=all`), submodule content
  (`--ignore-submodules=none`, which overrides `.gitmodules` `ignore`),
  detached commits (`HEAD --not <base> --branches --remotes --tags`), and
  initialized submodules' commits on no remote branch, recursively. A
  submodule's git directory sits under the worktree's admin directory, so
  this check is the only one that protects it.
- **Missing repository.** A dangling `.git` file makes git fail rather than
  walk up to an enclosing repository (checked with git 2.56), so a worktree
  inside some other repository is still reported as having lost its
  repository. Case 2 removes it only with `discard`.
- **Branch rule.** `--exclude=<branch>` binds only to the following
  `--branches`, and `check-ref-format` rejects `*`, `?` and `[`, so a branch
  name is never a glob. `created_branch` guards pre-existing branches.
  Deletion is the CAS `update-ref -d <ref> <tip>`. A failed rule after the
  worktree was removed leaves the session `exited` with its directory gone,
  and a retried delete re-evaluates the rule.
- **Waiting for the agent.** The ACP library's `ChildGuard::drop` sends
  `SIGKILL` to the process group synchronously
  (`agent-client-protocol-5.2.0/src/acp_agent.rs:353-381`), and `stop` awaits
  the aborted task, so the signal has been sent before git runs.
- **Lifecycle lock.** Delete and load take `Daemon::lifecycle`, which
  rechecks the registry after acquiring the lock. A load or second delete
  queued behind a delete answers `NOT_FOUND`. A delete queued behind startup
  sees `worktree` set to `None` after a failed add, or a launched agent.
  Registry removal and `Session::close` both happen while the guard is held.
- **Startup cleanup.** It runs under the instance and startup locks, before
  `bind`/`ready`. A session whose worktree could not be added has no
  `worktrees` row (003's rollback), so its row is deleted without touching
  the branch, which `startup_leaves_a_branch_named_like_a_worktree_that_could_not_be_added`
  protects. A refused cleanup restores the session through
  `State::unstarted`, which `load` refuses.

The log's deviations are sound. The plan's delete-during-startup test could
not fail without the wait: an early delete deletes `ox/<id8>`, so the later
`add` fails and leaves nothing behind. Using an existing branch with
`BlockedGit` makes the test catch a missing wait (6/6 failures without it).
`ExitWatch` events are a better liveness signal than a pid. The locked test's
`discard` follows from the fake agent writing its log into its cwd.

### Plan findings

1. **A worktree session whose workspace subdirectory is gone cannot be
   deleted, even with `discard`.** Plan, Removal case 1, runs `git -C
   <workspace> worktree remove --force <path>`. 003 allows a workspace that
   is a subdirectory of its checkout. If that subdirectory no longer exists
   while the repository does (removed, renamed, or absent on the branch the
   main checkout switched to), `git -C` fails with `cannot change to ...`
   (reproduced with git 2.56). The delete then answers `GIT` on every
   attempt, and startup cleanup keeps such a session. In the same state,
   `apply_branch_rule` finds no repository at the workspace and returns
   `None`, so an Ox branch is left without being reported in `kept_branch`.
   Running the removal from the worktree itself works: `git -C <path>
   worktree remove --force <path>` succeeded in the same reproduction. The
   branch rule could run from the repository the worktree names (`git -C
   <path> rev-parse --git-common-dir`). Consequence: a stuck, undeletable
   session until the user recreates the directory. It is uncommon, and
   nothing is lost.
2. **Startup cleanup runs unbounded git work before `ready`, and the client
   allows 5 s.** `connect_or_start` gives up after `START_TIMEOUT = 5 s`
   (`crates/ox-protocol/src/client.rs:27`). Each unstarted worktree session
   costs a `status --untracked-files=all` plus `git worktree remove` of the
   whole checkout, sequentially, before `ready`. Each failed agent launch
   leaves one such session, so a few of them in a large repository can push
   the first start past 5 s. The client then reports "oxd did not become
   ready", while the daemon carries on and serves the next attempt. The plan
   settled "runs before `ready`". The alternative is running cleanup after
   `ready` in a task that holds each session's lifecycle lock, so that
   clients see those sessions as `exited` until it finishes. I have not
   measured the cleanup time in a large repository.

### Notes

- `AGENTS.md:15` still says a stuck session "is closed with `session.close`,
  the method a UI uses". That method is now `session.delete`. The plan's
  caller list left this file out, and it is the user's instruction file, so
  the parent or the user should decide on the wording.
- Deleting a worktree session emits `agent_exited` (from `stop`) before
  `session_closed`, while a plain session emits only `session_closed`. This
  is harmless, but 006's UI should not treat the first event as a failure.
- The branch rule's `--not <base>` deletes an Ox branch that is the only ref
  to its base. This happens when `start` was a dangling commit, or when the
  start branch was deleted during the session. The plan specifies it, and
  the result is the repository as it was before the session, except in the
  second case. Dropping `--not <base>` would keep and report such a branch
  instead.
- `ExitWatch` is now duplicated in `crates/oxd/tests/daemon.rs` and
  `crates/oxd/tests/worktrees.rs`. A shared `tests/common` module would hold
  one copy.
- No test covers the second check refusing after `stop`, the stop/kill
  ordering, a lost branch CAS, or a load answering `NOT_FOUND` after a
  delete. The log names all four. The load case is reachable
  deterministically: hold `worktree add` with `BlockedGit`, queue a delete,
  then a load from a second client, and release. It would be worth adding
  when this code is next touched.
- `session.load` waits on the lifecycle lock inline in `conn.rs`, so a load
  queued behind a slow `worktree add` or delete stalls that connection. The
  log names this, and the choice is the plan's.

### Verification

- Reused: `make check`: passed (rustfmt, clippy `-D warnings`, workspace
  tests including `worktrees` 40 and `daemon` 47; Biome, tsc; ruff), from
  `/tmp/ox-005/make-check.log`. The log shows every crate recompiled, and
  the undo script restores the original file contents byte for byte, so the
  result applies to the current tree.
- Reused: `make smoke-claude`: passed, including `delete_and_idle_exit` and
  `{"check": "passed"}`, from `/tmp/ox-005/smoke.log`.
- Reused: `make e2e`: passed, 4 spec files, from `/tmp/ox-005/e2e.log`.
- Reused: 12 undo checks (`/tmp/ox-005/undo.py`; I inspected each mutation
  and its target test) and the 10/10 repeat of
  `a_delete_during_startup_waits_for_the_worktree`, from the work log.
- Run: two throwaway git reproductions in a temp directory, no Ox code. One
  confirmed that a dangling gitfile fails instead of discovering an
  enclosing repository. The other confirmed that `git -C <missing subdir>
  worktree remove` fails while `git -C <worktree> worktree remove --force
  <worktree>` succeeds. They were needed to settle the missing-repository
  path and plan finding 1.

## Round 2

- Task: `2026-11-12-005-remove-session-worktrees`
- Author: reviewer child of Hypercube session 2ea441ae, Claude Code, Opus 5.5
  (claude-opus-5-5), effort not exposed; coordinator ID `review-005`; native
  conversation ID `03ec4c88-ff96-47d0-bbc6-827a50794455`
- Independence: I did not write this code; the implementer was `impl-005`.
- Reviewed change: the same checkout and branch. This is a focused re-review
  of "Repair round 1" in `.agents/work/2026-11-12-005-remove-session-worktrees.md`
  against plan revision 4 (blob `6eaee1e93132f0867ee987fbf8af7e6f5ea3033a`,
  matching the log) and 003's revision 5 note. It covers the repair and what
  it touches: 003's `plan`, `roll_back` and `checked_out_at`, the store
  schema, `Daemon::new`, `clean_up`, idle, `main.rs`, the four new tests,
  `AGENTS.md` and docs.

### Verdict

accept: the repair implements revision 4. Removal and the branch rule no
longer depend on the workspace directory, and startup cleanup runs after
`ready` without letting a client race it. No blocking defect was found.

### Findings

No blocking findings. The invariants the repair changes:

- **Repository location.** `plan` reads `--path-format=absolute
  --git-common-dir` as the third line of its existing `rev-parse`. I checked
  the line order with git 2.56, including the empty `--show-prefix` line at
  the top level. The store writes `common_dir` in the same transaction as
  the row. `Repository` runs `git -C <common_dir> --git-dir=<common_dir>`.
  A missing common dir fails at `-C`, and a non-repository at that path
  fails `rev-parse --git-dir`, which I also checked. So R never discovers
  some other repository. `apply_branch_rule` reports the branch as kept
  without R, as entry 6 settled. `roll_back`'s worktree removal still runs
  from `plan.top`, which exists at creation, so that is correct.
- **Removal cases.** Case 2 with `discard` deletes the directory and falls
  through to case 3's unregistration. Without `discard`, `unsaved` refuses
  both "no longer exists" and "no longer recognizes". This matches entries
  2 and 7.
- **Cleanup after `ready`.** `Daemon::new` takes each unstarted worktree
  session's guard with `try_lock_owned` on a fresh mutex before anyone can
  see the session. `clean_up` drops each guard at the end of its iteration,
  after `forget` and `close`, so a waiting load or delete rechecks the
  registry and answers `NOT_FOUND`. After a refusal, a load answers
  `INVALID_STATE` (`State::unstarted`) and a delete proceeds. `cleaning`
  enters `is_idle`, and its reset goes through `update`, which signals
  `changed`. The idle loop therefore re-evaluates and starts its timer only
  then.
- **The 2-second wait in `the_daemon_stays_up_until_startup_cleanup_ends`**
  is acceptable under AGENTS.md's timer rule. The behavior under test is the
  passage of an idle period: the daemon must not exit during one. No event
  marks an exit that did not happen. The wait is two 1 s idle periods
  (`--idle-exit-ms 1000`) after `blocked()` confirms that cleanup is held.
  Correct code cannot fail it, because the daemon cannot go idle while
  `cleaning` is set. The regression exits after one period, which V6 showed.
  `lifetime` in `crates/oxd/tests/daemon.rs:694` uses the same bounded
  negative wait.

### Resolution of earlier findings

| Finding | Status | Evidence in current code |
| --- | --- | --- |
| Round 1 plan finding 1 (workspace subdirectory gone) | resolved by plan revision 4 | `crates/oxd/src/worktree.rs` `Repository`, `remove_worktree` and `apply_branch_rule` take no workspace. `a_session_whose_workspace_subdirectory_is_gone_is_deleted` covers it, and undo V2 makes it fail. |
| Round 1 plan finding 2 (cleanup before `ready` against the 5 s start timeout) | resolved by plan revision 4 | `crates/oxd/src/main.rs` spawns `clean_up` after `report_ready()`. `startup_cleanup_runs_after_ready_and_holds_the_session` covers it, and undo V1 makes the start time out. |
| Round 1 note (`AGENTS.md:15` names `session.close`) | resolved | `AGENTS.md:15` now names `session.delete`. |
| Round 1 other notes | unchanged | They were not in this repair's scope. |

### Notes

- **A worktree whose `.git` file was deleted** is the case the log names. It
  can lose no data, but it can leave a session that cannot be deleted. W
  (`usable`) uses discovery, so if the worktrees directory sits inside a
  repository (for example a `$HOME` kept in Git), W succeeds against that
  repository. `unsaved` then checks the wrong tree. `rev-list` usually
  fails on the unknown `base`, which gives `GIT`. `git worktree remove
  --force` then refuses to validate a worktree with no `.git`, even with
  `discard`. A W that compares the worktree's `rev-parse
  --path-format=absolute --git-common-dir` with the recorded `common_dir`
  would close the gap with one comparison. Without an enclosing repository,
  W fails and the case is handled like a removed registration.
- **Shutdown during cleanup can leave an orphaned git process.** The
  spawned `git worktree remove` is not `kill_on_drop`, so it can outlive
  `oxd` and finish on its own. The next start's cleanup may then see a
  half-removed directory as uncommitted changes and keep the session for a
  `discard` delete. Nothing is lost, and this is rare.
- `startup_cleanup_runs_after_ready_and_holds_the_session` sends the load
  before the release without proving the daemon received it first. The plan
  and log both name this, and V5 failed 5 of 5.
- The repair's undo checks ran before the two clippy-only edits. The log
  says so. Those edits are a `let` binding and a `Duration` constructor, and
  `make check`, smoke and e2e all ran after them.

### Verification

- Reused: `make check`: passed (`worktrees` 44, `daemon` 47, protocol 44,
  oxd unit 29; clippy, Biome, tsc, ruff), from
  `/tmp/ox-005/make-check-2.log`. It was written at 11:56:48, after the last
  code change at 11:55:45.
- Reused: `make smoke-claude`: passed, including `delete_and_idle_exit`,
  from `/tmp/ox-005/smoke-2.log`.
- Reused: `make e2e`: passed, 4 spec files, from `/tmp/ox-005/e2e-2.log`.
- Reused: undo checks V1–V7 (`/tmp/ox-005/undo2.py`) and the 8/8 repeat of
  the two cleanup tests, from the work log.
- Run: a throwaway git reproduction in a temp directory, with no Ox code. It
  confirmed the `rev-parse` output order that `plan` parses, at the top
  level and in a subdirectory, and that `git -C <dir> --git-dir=<dir>
  rev-parse --git-dir` fails on a non-repository. Both are needed for R and
  for reading `common_dir`.
