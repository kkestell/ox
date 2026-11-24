# Review: Stop iOS Safari from enlarging monospace text

## Scope and coverage

Uncommitted change on `mobile-text-size` against base `0367545` (`HEAD`, also the merge base with `origin/main`). The working tree changes one file, `cmd/ox/web/src/style.css`, adding `html { -webkit-text-size-adjust: 100%; text-size-adjust: 100%; }` with a comment. There is no plan; the request is the plan: monospace text in the file viewer's unwrapped source and the transcript's one-line tool rows rendered far larger than its CSS size on iPhone Safari, with sizes varying block to block, from iOS text autosizing.

The review checked the change against `AGENTS.md`, `cmd/ox/web/AGENTS.md`, the requirements **Narrow screens** and **Touch screens**, and the open `bug` issues (#5, #6, #33, #34, none of which this change touches).

Gaps:

- Headless Chrome does not autosize text, so the fix cannot be observed in `make e2e`. `cmd/ox/web/AGENTS.md` assigns phone-only behavior to a check on the user's phone, which the user ran against this worktree's build and confirmed.

## Summary

No findings.

The value `100%` turns off autosizing without disabling pinch zoom, which `none` would. The page's own sizes, including the 15px touch-screen sizes in the `pointer: coarse` block, now apply as written. No requirement changes: the fix restores the CSS sizes the page already declares, and no requirement states text sizes. No document owns this fact, and the comment gives the non-obvious reason the rule exists. No kept test is possible: a Playwright spec cannot observe autosizing and may not assert styling.

## Checks run

- `make test`: passed.
- `make e2e`: 165 passed, 4 skipped (the live specs).
- iPhone Safari against this worktree's build: the user confirmed the file viewer and transcript render at normal size.
- `make live`: not required; the change does not alter how Ox drives Claude Code.

## Verdict

Ready to commit.
