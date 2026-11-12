# Review: 2026-11-12-010-archive-sessions

## Round 1

- Task: `2026-11-12-010-archive-sessions`
- Reviewer: the xtask coordinator (Claude Code, Opus 5.5), at the user's request.
- Reviewed change: `git diff HEAD` at `ced1463` plus the work log. Plan:
  [plan](../plans/2026-11-12-010-archive-sessions.md). Work log:
  [work log](../work/2026-11-12-010-archive-sessions.md).

### Verdict

accept. Archive checks unsaved work before stopping the agent, stops it with
`Parked` (which ends an open turn), removes the worktree and keeps the branch;
the flag lives in its own table that cascades with the session and is reloaded
on restart; `session.load` refuses an archived session; unarchive re-adds the
worktree on its branch; delete of an archived session skips the unsaved check
for the missing directory and applies the branch rule. Each path has a test in
`crates/oxd/tests/worktrees.rs`.

### Verification

Reused the work log's `make check-rust`, `make check-scripts` and
`make smoke-claude` passes.
