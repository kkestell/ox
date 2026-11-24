# Review: Return starts a new line on a touch screen

## Scope and coverage

Uncommitted changes in the `touch-return` worktree on base `f08ea0d`
(`origin/main`), against `agents/plans/2026-11-23-touch-return.md`. The tree
modifies `cmd/ox/web/src/SessionView.tsx`, `cmd/ox/web/e2e/ui.ts`,
`cmd/ox/web/e2e/session.spec.ts`, and `docs/functional-requirements.md`, and
adds the plan.

Checked: `sendOnEnter` and both composers that use it (`SessionView.tsx`,
`NewSessionView.tsx`), the e2e helpers and every touch-emulated block that
sends through them (`session.spec.ts` "on a touch screen", `workspace.spec.ts`
"on a phone"), the requirement diff against the plan's text, and the open
`bug` issues (#5, #6, #33, #34, #55), none of which this change touches.

Gaps:

- None material. The change is local to one keydown handler and does not alter
  how the page reaches `oxd` or how Ox drives Claude Code.

## Summary

| ID | Severity | Category | Action   | Status | Title                                        |
| -- | -------- | -------- | -------- | ------ | -------------------------------------------- |
| F1 | Low      | Intent   | Rejected | —      | `reply` helper switched to Send, not planned |

## F1. `reply` helper switched to Send, not planned

Low · Intent · Rejected · `cmd/ox/web/e2e/session.spec.ts:104`

The local `reply` helper in `session.spec.ts` now sends with `send(page).click()`
instead of pressing Enter. The plan changes only `newSession` and `ask` in
`e2e/ui.ts`. `reply` is used only by two desktop tests (`:111`, `:144`), so the
edit is not needed for the touch blocks.

### Evidence

`grep -n "reply(page" cmd/ox/web/e2e/session.spec.ts` shows the only callers
at lines 111 and 144, both outside any touch-emulated block.

### Action

Rejected: the edit matches the helpers in `ui.ts`, changes no product behavior,
and leaves desktop Enter-to-send covered by the many specs that still press
Enter in the composer (`session.spec.ts:56` and others). Reverting it costs
more review churn than keeping it.

## Checks run

- Requirement diff: "Enter to send" carries the plan's new text word for word,
  and no other requirement changed.
- New test without the change: removed the `(pointer: coarse)` early return
  from `sendOnEnter` and ran `session -g "Return starts a new line"`. It failed
  at `toHaveValue("first line\nsecond line")` with value `"second line"`, the
  first line having been sent. Restored the file; `git diff --stat` matches the
  implementer's tree.
- With the change: `session -g "Return starts a new line|Shift\+Enter|input-method|on a touch screen"`,
  6 passed, covering the new test, the desktop Shift+Enter and IME tests, and
  the rest of the touch block that sends through `newSession`.
- `npm run lint` (biome and `tsc -p e2e`): passes; its two warnings are in
  `src/style.css`, untouched by this change.
- `dprint check`: passes.
- `make test` and `make e2e`: passed on the final tree per the implementer
  (161 passed, 4 skipped). Not re-run here.
- `make live`: not required; the change does not alter how Ox drives Claude
  Code.
- Headless Chrome through `make dev`: not required; the change does not alter
  how the page reaches `oxd`.

## Verdict

Ready to commit. F1 is rejected; there are no `Fix` or `Record` findings.
