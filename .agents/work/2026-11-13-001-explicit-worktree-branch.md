# Explicit worktree branch choice

## Plan

`.agents/plans/2026-11-13-001-explicit-worktree-branch.md`

## Summary

`session.new` now takes `branch: null | {type: "existing", name} | {type: "new", name, start}`. oxd refuses an existing branch that isn't there and a new branch whose name a local branch, a remote branch's full name, or a branch on any remote has. `workspace.branches` reports each local branch's worktree. The app has separate existing-branch and new-branch dialogs, and `oxctl new` takes `--worktree`, `--branch`, or `--new-branch [--start]`. The plan's goal is met.

## Departures from the plan

- `docs/architecture.md` described the old resolution, the single "New Worktree on Branch…" item, and the old `oxctl` flags. Its passages on the app menu, the branch kinds, `workspace.branches`, and `oxctl new` were rewritten to match.
- `started_holding` in `crates/oxd/tests/worktrees.rs` lost its `branch` parameter. Every caller passed `None`, and with `in_worktree` gone, the parameter had no single meaning to keep.
- The e2e test that picks `origin/feature` uses the existing `choose` helper. The embedded WebDriver's `selectByAttribute` and option clicks select nothing.

## Decisions

- The Branch `<select>` in `ExistingBranchDialog` uses the `text-field` class, the same as its peer inputs in dialogs. No new CSS.
- The new-branch dialog's exists note uses `<p className="message branch-exists">`, next to the existing `fetch-error` message.
- `check_new` checks a remote branch's full name with `refs/remotes/{name}`, and checks the branches on each remote with `refs/remotes/{remote}/{name}`.
- `branches_name_the_worktree_that_has_them_checked_out` canonicalizes the session's worktree path, because git reports the resolved path (`/private/var/…` on macOS).

## Checks run

- `make check` (fmt, clippy `-D warnings`, the Rust workspace tests, biome, tsc, ruff) — passed.
- `make e2e` — 8 of 8 spec files passed.

## Manual verification

1. `oxctl new` against a throwaway daemon and repository with `main` and `other`.

   ```sh
   T=$(mktemp -d); export XDG_DATA_HOME=$T/data OX_SOCKET=$T/oxd.sock
   git init -q -b main $T/repo && git -C $T/repo -c user.name=a -c user.email=a@b commit -q --allow-empty -m init && git -C $T/repo branch other
   W=$(target/debug/oxctl workspace add $T/repo)
   target/debug/oxctl new --workspace $W --branch otehr -- target/debug/oxd-fake-agent
   target/debug/oxctl new --workspace $W --new-branch other -- target/debug/oxd-fake-agent
   target/debug/oxctl new --workspace $W --branch other -- target/debug/oxd-fake-agent
   target/debug/oxctl new --workspace $W --new-branch topic --start main -- target/debug/oxd-fake-agent
   git -C $T/repo worktree list
   ```

   The typo was refused with `no branch otehr locally or on a remote`, and `--new-branch other` with `a branch other exists`. The last two each started a session, and `git worktree list` showed worktrees on `other` and `topic`.

2. Clap flag rules.

   ```sh
   target/debug/oxctl new --workspace w --start x
   target/debug/oxctl new --workspace w --branch a --new-branch b
   target/debug/oxctl new --workspace w --worktree --branch a
   ```

   The first was refused because `--new-branch` was missing, and each of the other two was refused as a conflicting pair.
