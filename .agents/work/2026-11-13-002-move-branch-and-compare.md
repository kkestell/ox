# Move a branch and compare a session's branch

## Plan

`.agents/plans/2026-11-13-002-move-branch-and-compare.md`

## Summary

`oxd` has `branch.move`, `session.set_compare` and `session.refresh`. Worktree sessions carry a comparison ref (`compare_refs` table, `WorktreeInfo.compare`) and publish `SessionInfo.comparison`, which a per-session task recomputes at creation and restore, on turn end, and after a move, delete, startup cleanup or fetch in the repository. `oxctl` has `new --compare`, `compare` and `move-branch`. Local merge works by hand from `oxctl`, so the plan's goal is met.

## Departures from the plan

- `branches::compare` returns `Option<Comparison>`, `None` when the worktree has no comparison ref, so callers don't check the ref first.
- `branches::move_branch` takes the daemon's busy check as a closure, called with the checkout's path between finding the checkout and the `git status` check. This keeps the plan's step order with the git steps in `branches.rs`.
- `Session::set_compare` computes the new comparison under the `comparing` lock and publishes the new ref and the comparison in one `update`. It does not update the worktree and then call `compare`. Two refs that point at one commit give equal comparisons, so with that order the second `update` would see no change.
- `Daemon::fetch` became a wrapper that calls `recompare` after the old body, which is now `fetched`. Each request that waits on a shared fetch calls `recompare`, and `Notify` coalesces those calls.

## Decisions

- A failed comparison reports `{ref} is not a commit` (for the branch: `branch {branch} is not a commit`). It does not use git's output, because `rev-parse --verify` prints only "Needed a single revision".
- `info_of` publishes a comparison only while the worktree has a comparison ref. A worktree that could not be added, or a ref cleared while a comparison is running, therefore never shows a stale comparison.
- `session.set_compare` holds the session's lifecycle guard, so it waits for a startup or load to finish, and a session being deleted is not found. A session with no worktree is refused with `INVALID_REQUEST`.
- `branch.move` answers `null`. It refuses a branch name that `git check-ref-format` rejects, and a missing branch with `no branch <name>`, both as `INVALID_REQUEST`. A busy session is refused as `session <id> is <state> in <checkout>`.
- The UI's `protocol.ts` types are unchanged. `compare` defaults to `None` on the wire, and the app part of the todo adds the new fields.

## Checks run

- `make check-rust` passed: fmt, clippy `-D warnings`, and the workspace tests, including the 11 new tests in `crates/oxd/tests/worktrees.rs`.
- `make smoke-claude` passed.
- I disabled each recompare trigger in turn (turn end, move, fetch, delete). Each matching test failed, and each passed once the trigger was restored.

## Manual verification

1. Local merge by hand with `oxctl` against a throwaway daemon.

   ```sh
   T=$(mktemp -d); export XDG_DATA_HOME=$T/data OX_SOCKET=$T/oxd.sock
   git init -q -b main $T/repo && git -C $T/repo -c user.name=a -c user.email=a@b commit -q --allow-empty -m init
   W=$(target/debug/oxctl workspace add $T/repo)
   target/debug/oxctl new --workspace $W --compare main -- target/debug/oxd-fake-agent
   target/debug/oxctl new --workspace $W --worktree --compare nope -- target/debug/oxd-fake-agent
   S=$(target/debug/oxctl new --workspace $W --worktree --compare main -- target/debug/oxd-fake-agent)
   target/debug/oxctl compare $S
   WT=$(target/debug/oxctl ls | awk -v s=$S '$1==s{print $4}')
   git -C $WT -c user.name=a -c user.email=a@b commit -q --allow-empty -m work
   target/debug/oxctl compare $S
   TIP=$(git -C $WT rev-parse HEAD); MAIN=$(git -C $T/repo rev-parse main)
   target/debug/oxctl move-branch --workspace $W main $TIP --expected $TIP
   target/debug/oxctl move-branch --workspace $W main $TIP --expected $MAIN
   target/debug/oxctl compare $S
   target/debug/oxctl compare $S --clear
   ```

   Clap refused `--compare` without a worktree flag, and `oxd` refused `nope is not a commit`. `compare` printed `main <tip> <tip> 0 0`, then `1 0` after the commit. The move with the wrong `--expected` was refused with `main is at <main>, not <tip>`. The correct move succeeded, and `compare` then printed `0 0` with both tips at the session's commit. `--clear` printed nothing.
