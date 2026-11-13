# Move a branch and compare a session's branch

## Scope and coverage

The staged changes for `.agents/plans/2026-11-13-002-move-branch-and-compare.md`: the protocol additions in `ox-protocol`, `branches::compare` and `branches::move_branch`, the comparison fields and task in `Session`, the `branch.move`, `session.set_compare` and `session.refresh` handlers and the recompare triggers in `Daemon`, the `compare_refs` table in `Store`, the `oxctl` commands, the new tests in `crates/oxd/tests/worktrees.rs`, and the documentation. I traced the session lifecycle paths the comparer task touches (spawn, failed worktree add, restore, archive, unarchive, delete, startup cleanup, close), the lock order between the registry, a session's state, worktree and comparison locks, and the git sequence of a move in a checkout.

Gaps:

- The UI's `protocol.ts` is unchanged; the app part of the todo covers it.
- `make smoke-claude` was not rerun; the work log reports it passed, and the review made no code changes.

## Summary

| ID | Severity | Category | Fix       | Title                                      |
| -- | -------- | -------- | --------- | ------------------------------------------ |
| F1 | Low      | Comments | Not fixed | Task-list comment in `conn.rs` is unwrapped |

## F1. Task-list comment in `conn.rs` is unwrapped

Low · Comments · Not fixed · `crates/oxd/src/conn.rs:131`

The edited comment above the methods that run in their own task has one line of 117 columns, while the rest of the comment wraps at the file's usual width. It has no effect on behavior.

### Evidence

`awk 'length > 100'` over `crates/oxd/src/conn.rs` reports line 131 at 117 columns; the line is new in this change. `rustfmt` does not wrap comments, so `make check-rust` passes.

### Fix

Not fixed: Low findings are recorded and left unchanged for this review.

## Checks run

- `make check-rust`: formatting, clippy with warnings as errors, and the workspace tests passed.

## Verdict

The change implements the plan: moves check the expected commit, fast-forward unless forced, refuse busy agents and uncommitted tracked changes, and put the branch back when the checkout cannot move; comparisons are computed at creation and restore, on turn end, after moves, deletes, startup cleanup and fetches, and on request, and are serialized by the session's comparing lock. The only finding is F1, a cosmetic comment wrap.
