# Explicit worktree branch choice

## Scope and coverage

The staged changes for `.agents/plans/2026-11-13-001-explicit-worktree-branch.md`: the `WorktreeBranch` and `LocalBranch` protocol types and their serialization tests, `plan`, `resolve_existing` and `check_new` in `crates/oxd/src/worktree.rs`, `branches::list`, `new_session` in `crates/oxd/src/daemon.rs`, the `oxctl new` flags, the UI's `useBranches`, `ExistingBranchDialog`, `NewBranchDialog` and New Session menu, `crates/oxd/tests/worktrees.rs`, `ui/e2e/specs/worktree.e2e.ts`, and `docs/architecture.md`. The review traced every caller of `Checkout::Worktree` and `Branches.local`, and compared the UI's exists and remote-filter rules with oxd's refusals.

Gaps:

- `make e2e`, `make smoke-claude` and the Rust workspace tests were not rerun, because the fixes change only comments. The work log reports `make check` and `make e2e` passing on the code under review.

## Summary

| ID | Severity | Category      | Fix   | Title                                                       |
| -- | -------- | ------------- | ----- | ----------------------------------------------------------- |
| F1 | Low      | Documentation | Fixed | `session.new` doc says only a new branch is created         |
| F2 | Low      | Comments      | Fixed | Dialog state comment names the removed dialog               |

## F1. `session.new` doc says only a new branch is created

Low · Documentation · Fixed · `crates/ox-protocol/src/messages.rs:64`

The `SessionNew` doc said a worktree's branch is "checked, and a new one created, before the answer". An existing branch found only on a remote is also created before the answer, as a local branch tracking the remote one. A client reading the doc would expect no branch to appear for an existing-branch request. One line of the comment also ran past the width of its neighbours.

### Evidence

`create_branch` in `crates/oxd/src/worktree.rs` returns early only for `Kind::Existing`, and runs `git branch --no-track` for both `Kind::New` and `Kind::Track`. `new_session` calls it before answering.

### Fix

The doc now says the branch is "checked, and created unless it is a local branch already, before the answer", rewrapped to the comment's width.

## F2. Dialog state comment names the removed dialog

Low · Comments · Fixed · `ui/src/SessionList.tsx:60`

The comment "The workspace and agent the New Worktree on Branch dialog is for" named the dialog this change removed, and sat above the new `existingBranch` state while `newBranch` had none.

### Fix

`existingBranch` and `newBranch` each have a comment naming their dialog.

## Checks run

- `cargo fmt --all --check` — passed.
- `make check-ui` (Biome, `tsc`, `tsc -p e2e`) — passed.

## Verdict

The change meets the plan. Resolution, the new-branch refusals, the worktree listing, the `oxctl` flag rules and both dialogs behave as the plan decides, and the UI's exists check and remote filter match oxd's refusals. The two findings, F1 and F2, were stale or inaccurate comments, and both are fixed.
