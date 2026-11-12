# Deleting a worktree session removes its worktree and Ox's branch without losing work

- Task: `2026-11-12-005-remove-session-worktrees`
- Status: complete
- Plan: `.agents/plans/2026-11-12-005-remove-session-worktrees.md`, blob
  `4995e0c28b9e04e98670cb5d66144c73662db938`
- Dependency plan read: `.agents/plans/2026-11-12-003-create-session-worktrees.md`
  (revision 4)
- Executed on: `882dcce` (004 committed)
- Review: `.agents/reviews/2026-11-12-005-remove-session-worktrees.md` (rounds 1–2)

## Changes

The design is the plan's; this lists where each part landed.

- **Protocol** (`crates/ox-protocol/src/messages.rs`, `client.rs`):
  `Method::SessionClose` became `SessionDelete { session_id, discard, env }`
  (`session.delete`), answering `Deleted { kept_branch }`; `code::UNSAVED_WORK
  = 7`; `Client::close` became `Client::delete(session_id, discard, env)`.
  Doc comments on all three; a decode test for `session.delete` with
  `discard` and `env` absent. `SessionClosed`/`End::SessionClosed` keep their
  names.
- **Removal** (`crates/oxd/src/worktree.rs`): `Git::own()`; `unsaved`,
  `unsaved_work` (the `UNSAVED_WORK` body), `remove_worktree` (the three
  on-disk cases) and `apply_branch_rule`, with private helpers
  `in_repository`, `remove_registered` and `registered`.
- **Session and state** (`session.rs`, `state.rs`): async `Session::stop`
  (abort, await the task, record `Exited(None)`); `lifecycle()`,
  `worktree()` and `workspace_id()` accessors; `restore` always restores,
  using the new `State::unstarted` for a row with no agent session ID.
- **Daemon** (`daemon.rs`): `delete_session` follows the plan's Delete steps;
  a `lifecycle` helper takes the session's lock and answers `NOT_FOUND` if
  the session was deleted while it waited, used by delete and by the
  `session.load` handler. `Daemon::new` is async and does the startup
  cleanup (`remove_unstarted`, with `Git::own()`); rows that fail it are
  restored `exited` and the reason is logged. `close_session` is gone.
- **Store** (`store.rs`): `remove_unstarted_sessions` and its test removed.
- **conn.rs**: `SessionDelete` runs in its own task. **main.rs**: awaits
  `Daemon::new` before binding, so `ready` follows cleanup.
- **Callers**: `oxctl delete [--discard]` prints `kept branch <name>`;
  `started()` deletes with `discard: true`. `crates/ox/src/lib.rs`'s
  `close_session` command calls `delete(.., false, utf8_env())` (its name
  stays for 006). `ui/e2e/helpers.ts` `resetDaemon` runs `oxctl delete
  --discard`. `scripts/smoke-acp.py` runs `delete`; report key
  `delete_and_idle_exit`.
- **Tests**: `crates/oxd/tests/daemon.rs` uses `delete`; the `close_*` tests
  are `delete_*`, and `a_closed_session_is_not_saved` is
  `a_deleted_session_is_not_saved`. `crates/oxd/tests/worktrees.rs` gains
  `ExitWatch` (copied from `daemon.rs`), helpers, and 13 tests (listed
  under Verification).
- **Docs**: `docs/architecture.md` (oxctl `delete`; the delete rules,
  `discard`, kept branches, waiting on startup; startup cleanup; 003's
  interim close sentence replaced). `docs/acp-v2.md` `session/close` row's oxd
  note says deleting.

Changed paths:

- `crates/ox-protocol/src/client.rs`
- `crates/ox-protocol/src/messages.rs`
- `crates/ox/src/lib.rs`
- `crates/oxctl/src/main.rs`
- `crates/oxd/src/conn.rs`
- `crates/oxd/src/daemon.rs`
- `crates/oxd/src/main.rs`
- `crates/oxd/src/session.rs`
- `crates/oxd/src/state.rs`
- `crates/oxd/src/store.rs`
- `crates/oxd/src/worktree.rs`
- `crates/oxd/tests/daemon.rs`
- `crates/oxd/tests/worktrees.rs`
- `docs/acp-v2.md`
- `docs/architecture.md`
- `scripts/smoke-acp.py`
- `ui/e2e/helpers.ts`
- `.agents/work/2026-11-12-005-remove-session-worktrees.md` (this log)

## Deviations

- **The delete-during-startup test uses an existing branch and holds `git
  worktree add`.** The plan's test (Ox branch, delete sent as `session.new`
  answers) passes without the lifecycle wait. An early delete deletes the
  unchecked-out `ox/<id8>` at its base, so the later `worktree add` fails and
  nothing is left. With an existing branch, which delete never touches, the
  early delete finds no worktree yet, and the add then succeeds and leaves
  one. That is the failure the wait prevents. The test holds `worktree add`
  with 003's `BlockedGit`, sends the delete, and releases the add alongside it.
  It asserts the worktree and its registration are gone, the existing branch
  remains, and no session is listed. Its undo check fails 6 of 6 runs without
  the wait, and the test with the wait passed 10 of 10 repeated runs.
- **Agent liveness uses the `--hold` FIFO (`ExitWatch`) and a prompt**, not
  the `pid` directive. "Still alive" is a prompt the agent answers. "Gone" is
  the FIFO reaching end of file. Both are events, where checking a pid would
  also see a zombie.
- **`status` runs as `git --no-optional-locks status …`**, git's documented
  equivalent of `GIT_OPTIONAL_LOCKS=0`. It goes in the argument list, which
  avoids per-call environment overrides in `Git`.
- **Routine choices the plan left open:**
  - A failed `remove_dir_all` (repository gone, `discard`) answers
    `INVALID_STATE` "removing <path>: <error>", because the protocol has no
    local-I/O code.
  - A failed compare-and-swap `update-ref -d` in the branch rule answers
    `GIT`; a retried delete re-evaluates the rule.
  - The `UNSAVED_WORK` message is "deleting the worktree would lose " plus
    the items joined with "; ".
- **Unregistering a missing directory** compares `git worktree list
  --porcelain -z` against the path with its parent canonicalized, because git
  lists real paths (`/private/var/…` on macOS).
- **The locked-worktree test deletes with `discard`.** The fake agent resumes
  only from a log a prompt writes into its cwd, which is an untracked file in
  the worktree.

## Verification

Environment: macOS (Darwin 25.6.0), git 2.56.0, rustc 1.96.0, node 22.22.1,
checkout `/Users/kyle/projects/ox/.hypercube/2ea441ae` on
`hypercube/session-2ea441ae` at `882dcce` plus this change. Code checks ran on
the final tree. Only this log was written after them.

| Command | Result | Evidence |
| --- | --- | --- |
| `make fix` | passed; no further changes | conversation tool result |
| `make check` | passed (rustfmt, clippy `-D warnings`, workspace tests: `worktrees` 40 passed, `daemon` 47, protocol 44, oxd unit 29; biome, tsc; ruff) | `/tmp/ox-005/make-check.log` (local, not in the repository) and this conversation |
| `make smoke-claude` | passed, every check including `delete_and_idle_exit` and `{"check": "passed"}` (Haiku `claude-haiku-5-5`) | `/tmp/ox-005/smoke.log`, artifacts `/private/tmp/ox-acp-rncvc6wz` |
| `make e2e` | passed: 4 spec files, 18 tests | `/tmp/ox-005/e2e.log` |
| `cargo test -q -p oxd --test worktrees a_delete_during_startup` ×10 | 10 of 10 passed | conversation tool result |

New tests in `crates/oxd/tests/worktrees.rs`:

- `a_clean_delete_removes_the_worktree_and_the_branch` (ignored file included;
  agent exit via FIFO; repository status and `HEAD` unchanged)
- `a_branch_merged_elsewhere_is_deleted_and_one_with_its_own_commits_is_kept`
  (kept with and without `discard`)
- `a_branch_that_existed_before_the_session_is_never_deleted`
- `a_tracking_branch_is_kept_until_its_commits_are_pushed`
- `uncommitted_changes_refuse_the_delete_until_it_discards_them`
- `commits_on_no_branch_refuse_the_delete`
- `submodule_commits_on_no_remote_branch_refuse_the_delete` (clean and
  initialized: removed)
- `a_worktree_deleted_outside_ox_is_unregistered`
- `a_worktree_whose_repository_moved_is_removed_only_with_discard`
- `a_locked_worktree_fails_the_delete_and_the_session_loads_again`
- `a_branch_another_worktree_has_checked_out_is_kept`
- `a_delete_during_startup_waits_for_the_worktree`
- `startup_removes_the_worktrees_of_sessions_that_never_started` (clean one
  removed; dirty one listed `exited`, `load` → `INVALID_STATE`, `delete
  --discard` removes it)
- `startup_leaves_a_branch_named_like_a_worktree_that_could_not_be_added`
  (003 review finding 3: covers the startup-failure path's
  `store.remove_worktree`)

Undo checks: each mutation was applied, the named test run, and the source
restored. The script is `/tmp/ox-005/undo.py`. Every mutation made its test
fail:

| Undone | Test that failed |
| --- | --- |
| U1 delete's pre-check | `uncommitted_changes_refuse_the_delete_until_it_discards_them` (the prompt to the stopped agent fails) |
| U2 startup removal (`remove_unstarted` → `Ok`) | `startup_removes_the_worktrees_of_sessions_that_never_started` (worktree remains) |
| U3 lifecycle wait in delete | `a_delete_during_startup_waits_for_the_worktree` (worktree remains; 6 of 6 runs) |
| U4 `store.remove_worktree` in the startup-failure path | `startup_leaves_a_branch_named_like_a_worktree_that_could_not_be_added` (`topic` deleted) |
| U5 checked-out check in the branch rule | `a_branch_another_worktree_has_checked_out_is_kept` |
| U6 `--exclude=<branch>` | `a_branch_merged_elsewhere_is_deleted_and_one_with_its_own_commits_is_kept` |
| U7 submodule check | `submodule_commits_on_no_remote_branch_refuse_the_delete` |
| U8 detached-commit check | `commits_on_no_branch_refuse_the_delete` |
| U10 unregistering a missing directory | `a_worktree_deleted_outside_ox_is_unregistered` |
| U11 `remove_dir_all` (made `remove_dir`) | `a_worktree_whose_repository_moved_is_removed_only_with_discard` |
| U12 `created_branch` guard | `a_branch_that_existed_before_the_session_is_never_deleted` |
| U13 `session.stop()` in delete | `a_locked_worktree_fails_the_delete_and_the_session_loads_again` (session not `exited`) |

## Session provenance

| Stage | Role/name | Coordinator ID | Harness | Model/effort | Native conversation ID |
| --- | --- | --- | --- | --- | --- |
| parent | xtask coordinator | `2ea441ae` | Claude Code (Hypercube) | claude-opus-5-5 | `57fda6b3-c0d1-4d32-a25b-993c5391c607` |
| plan, revision 4 | planner | `planner` | Claude Code (Hypercube child) | opus / high | `5ce61adf-7d6c-4b85-ab78-5167733b9e23` |
| work, repair 1 | xwork implementer, child of Hypercube session 2ea441ae | `impl-005` | Claude Code (Hypercube child) | claude-opus-5-5 / effort not exposed | `dced0850-ced1-4dc9-95f8-253cad931641` |
| review rounds 1–2 | review-005 | `review-005` | Claude Code (Hypercube child) | claude-opus-5-5 | `03ec4c88-ff96-47d0-bbc6-827a50794455` |

## Limitations

- **Untested races** (as the plan names them):
  - the second unsaved check after `stop`, meaning work written between the
    pre-check and the agent stopping;
  - that awaiting the aborted task means the agent's process group was
    signalled before git runs;
  - the branch rule's compare-and-swap losing to a concurrent commit;
  - a `session.load` waiting on a delete and then answering `NOT_FOUND`.
- **Other untested paths:**
  - a `remove_dir_all` failure (`INVALID_STATE`);
  - startup cleanup failing with `GIT` rather than a refusal;
  - `registered` when the worktrees directory itself is gone.
- **The app's Close Session button now sends `session.delete` with `discard:
  false`.** A worktree session with unsaved work shows the `UNSAVED_WORK`
  error with no way to confirm a discard until 006 adds the dialog and
  renames the Tauri command. e2e covers only workspace sessions.
- **`session.load` stays inline in `conn.rs`.** While it waits on a delete
  that is running git, that client's connection serves nothing else.
  Spawning it would let a later request on the connection overtake the load.
- Agent processes outside the agent's process group may still write into a
  removed worktree (out of scope, as the plan states).

## Repair round 1

- Plan executed: `.agents/plans/2026-11-12-005-remove-session-worktrees.md`
  revision 4, blob `6eaee1e93132f0867ee987fbf8af7e6f5ea3033a`; 003's plan
  revision 5 note read.
- Review: `.agents/reviews/2026-11-12-005-remove-session-worktrees.md` round
  1 (accept; plan findings 1 and 2 and the `AGENTS.md` note, decided by the
  user into revision 4).
- Author: same worker as above (`impl-005`, native conversation
  `dced0850-ced1-4dc9-95f8-253cad931641`, claude-opus-5-5).

### Findings addressed

- **Plan finding 1, the workspace directory is gone.**
  - `crates/oxd/src/store.rs`: the `worktrees` table gains `common_dir TEXT
    NOT NULL`. A new `SavedWorktree { info: WorktreeInfo, common_dir }` is the
    store's worktree type; `WorktreeInfo` on the wire is unchanged. The store
    test round-trips `common_dir`. No migration: a 003/004 database must be
    deleted (plan entry 8). None existed under `target/dev` or
    `~/.local/share/ox` on this machine, so nothing was deleted.
  - `crates/oxd/src/worktree.rs`, 003's code:
    - `plan` reads `--path-format=absolute --git-common-dir` in its existing
      `rev-parse`.
    - `WorktreePlan::info` became `saved()`.
    - `checked_out_at` and `roll_back` now run through the new `Repository`.
  - `Repository` runs `git -C <common_dir> --git-dir=<common_dir> …`, and has
    `found()`, the R probe.
  - `usable(path)` is the W probe.
  - `unsaved` lists, when the directory exists but W fails:
    - "the repository of … no longer exists …" without R;
    - "git no longer recognizes … as a worktree …: <git's message>" with R.
  - `remove_worktree(git, worktree, discard)` follows the plan's cases 1–3.
    Case 2 continues as case 3. Neither `remove_worktree` nor
    `apply_branch_rule` takes a workspace any more.
  - `apply_branch_rule` returns `Some(branch)` without R.
  - `crates/oxd/src/session.rs` and `daemon.rs` carry `SavedWorktree`, and
    `delete_session` no longer looks up the workspace. `Session::workspace_id`
    is removed, since nothing uses it now.
- **Plan finding 2, startup cleanup moves after `ready`.**
  - `Daemon::new` is synchronous again. It deletes unstarted rows without a
    worktree. It restores those with a worktree (`State::unstarted`) and keeps
    each with its lifecycle guard (`Session::hold_lifecycle`, which
    `Session::spawn` now uses too) in `Daemon::unstarted`.
  - `Registry::cleaning` keeps `is_idle` false while cleanup runs.
  - `Daemon::clean_up` cleans each session in turn with `Git::own()`. A
    success deletes the session through the new `Daemon::forget`, which
    delete also uses, and then `Session::close`. Every failure is logged.
    After the last session it clears `cleaning`, which signals `changed`.
  - `crates/oxd/src/main.rs` spawns `clean_up` after `ready`, and shutdown
    does not wait for it.
- **`AGENTS.md:15`**: "A session stuck in startup is deleted with
  `session.delete`, …".
- **Docs** (`docs/architecture.md`):
  - the row's `common_dir`;
  - the refusal when git cannot use the worktree;
  - a kept branch when the repository is gone;
  - removal from `common_dir`;
  - background cleanup after `ready`, the idle hold, and shutdown not waiting
    for cleanup.

### Changed paths (this round)

`AGENTS.md`, `crates/oxd/src/{daemon,main,session,store,worktree}.rs`,
`crates/oxd/tests/worktrees.rs`, `docs/architecture.md`, this log.

### Tests

- Changed:
  - `a_worktree_whose_repository_moved_is_removed_only_with_discard` now
    expects `kept_branch` naming the Ox branch, which exists in the moved
    repository.
  - `startup_removes_the_worktrees_of_sessions_that_never_started` watches
    until the clean session leaves the listing.
- New:
  - `a_session_whose_workspace_subdirectory_is_gone_is_deleted`: an untouched
    worktree gives `kept_branch` `None` and its branch is deleted; one with a
    commit is kept and named.
  - `a_worktree_whose_registration_was_removed_is_removed_only_with_discard`
  - `startup_cleanup_runs_after_ready_and_holds_the_session`: the harness
    gains `start_with` and `restart_with`, and puts `BlockedGit::new("
    worktree remove ")` on oxd's `PATH`. The test checks that `ready`
    arrived, that cleanup is held, and that the session is listed `exited`.
    A load is then sent and git released; the load answers `NOT_FOUND`, and
    the session, its worktree and its branch are gone.
  - `the_daemon_stays_up_until_startup_cleanup_ends`: with no client, oxd is
    still running 2 s (two 1 s idle periods) after `blocked()`. It exits
    successfully after the release.

### Undo checks

Script: `/tmp/ox-005/undo2.py`. Each mutation is applied, the named test run,
and the files restored byte for byte.

| Undone | Test that failed |
| --- | --- |
| V1 cleanup awaited before `ready` instead of spawned after | `startup_cleanup_runs_after_ready_and_holds_the_session` (start timed out waiting for `ready`) |
| V2 removal and branch rule run as `git -C <workspace>` with no `--git-dir` | `a_session_whose_workspace_subdirectory_is_gone_is_deleted` (delete fails) |
| V3 branch rule returns `None` without the repository | `a_worktree_whose_repository_moved_is_removed_only_with_discard` |
| V4 a removed registration not listed as unsaved | `a_worktree_whose_registration_was_removed_is_removed_only_with_discard` |
| V5 cleanup drops each session's lifecycle guard before working | `startup_cleanup_runs_after_ready_and_holds_the_session` (load does not answer `NOT_FOUND`; 5 of 5 runs) |
| V6 `cleaning` left out of `is_idle` | `the_daemon_stays_up_until_startup_cleanup_ends` |
| V7 `common_dir` not read back by the store | `store::tests::a_session_round_trips_with_its_worktree` |

The first V2 mutation deadlocked on the registry lock, which was a bug in the
mutation, not in the code. It was rewritten and then failed as listed. The
undo checks ran before two clippy fixes: `store.sessions()` is now bound
before the `for` loop, and the test uses `Duration::from_secs(2)`. Neither
fix changes behaviour.

### Verification

Same environment as above. The final tree is the one these ran on.

| Command | Result | Evidence |
| --- | --- | --- |
| `make fix` | passed | `/tmp/ox-005/fix2.log` |
| `make check` | first run failed clippy (the two lints above), fixed; rerun passed: rustfmt, clippy, workspace tests (`worktrees` 44, `daemon` 47, protocol 44, oxd unit 29), biome, tsc, ruff | `/tmp/ox-005/make-check-2.log` |
| `make smoke-claude` | passed, `delete_and_idle_exit` and `{"check": "passed"}` | `/tmp/ox-005/smoke-2.log` |
| `make e2e` | passed, 4 spec files | `/tmp/ox-005/e2e-2.log` |
| `cargo test -q -p oxd --test worktrees -- startup_cleanup the_daemon_stays` ×8 | 8 of 8 passed | conversation tool result |

### Untested or unhandled in this round

- **The cleanup-after-`ready` load is not provably ordered.** The load is sent
  before the release, but a load that did not wait would usually be handled
  before the release too. The plan accepts this; V5 failed 5 of 5.
- **Cleanup failing with `GIT`, and `Daemon::forget` failing in cleanup**,
  are untested (only the refusal path is tested).
- **A worktree whose `.git` file was deleted** is not handled. In such a
  directory W discovers any repository enclosing the worktrees directory,
  and the unsaved checks would run against that repository. Neither the plan
  nor the tests cover this.
- **The earlier rounds' untested races stand**, as listed under Limitations
  above.

## Parent verification

- Verifier: parent coordinator, Hypercube session 2ea441ae, native
  conversation `57fda6b3-c0d1-4d32-a25b-993c5391c607`.
- Review round 1 accepted the code with two plan findings and an `AGENTS.md`
  note; the user decided them into plan revision 4 (and confirmed its new
  entries 6–8). Repair round 1 implemented it; because it changed 003's schema
  and the startup contract, review-005 re-reviewed it (round 2): accept, no
  blocking or plan findings.
- Checks after the last code edit: `make check`, `make smoke-claude` and
  `make e2e` passed (`/tmp/ox-005/make-check-2.log`, `smoke-2.log`,
  `e2e-2.log`).
- Notes from review, not fixed: a worktree whose `.git` file was deleted,
  inside a repository-tracked directory, can leave a session that cannot be
  deleted; a shutdown during cleanup can leave a `git worktree remove`
  running; the cleanup-hold test does not prove the load arrived before the
  release; `ExitWatch` is duplicated across two test files; four delete races
  stay untested, as named above.
