# Review: show sidebar row buttons for keyboard focus only

## Scope and coverage

Uncommitted changes on `chevron-focus-visible` at base `b5419ba` (HEAD, and `git merge-base origin/main HEAD`): `cmd/ox/web/src/style.css` and `cmd/ox/web/e2e/session.spec.ts`, with the untracked plan. Checked against `agents/plans/2026-11-22-chevron-focus-visible.md` as amended (chevron hidden with `opacity: 0` and kept rendered), which resolves #12.

Checked for regressions: rows at rest against main, touch screens, and the keyboard order open → chevron → ⋯.

Gaps:

- Safari and Firefox were not run; their `:focus-visible` handling of clicked buttons is taken from the plan.

## Summary

| ID | Severity | Category | Action   | Status | Title                                                   |
| -- | -------- | -------- | -------- | ------ | ------------------------------------------------------- |
| F1 | Medium   | Testing  | Fix      | Resolved | No test guards the chevron showing on a touch screen    |
| F2 | Low      | Testing  | Rejected | —      | The transparent chevron takes clicks on a row at rest   |

## F1. No test guards the chevron showing on a touch screen

Medium · Testing · Fix · Resolved · `cmd/ox/web/e2e/session.spec.ts:1237`

On main the chevron was `display: none` at rest, so the touch test "a parent's chevron shows without hover and keeps the session list open" failed if the `@media (pointer: coarse)` rule did not show it: `tap()` refuses a hidden element. Now the chevron is always rendered, and Playwright counts an `opacity: 0` element as visible and tappable. The test passes whether or not the chevron can be seen, so a regression in the coarse-pointer rule would leave touch users with an invisible chevron and no failing test.

The current CSS is correct: the coarse rule sets `opacity: 1` and has the base rule's specificity, later in the file.

### Evidence

Replaced `nav li button.chevron-right { opacity: 1; }` in the coarse-pointer block (`style.css:455`) with an empty rule and ran `npm run e2e -- session -g "touch screen"`: the test passed. Restored the file afterwards.

### Action

In that touch test, before `chevron(page, "Parent").tap()`, add `await expect(chevron(page, "Parent")).toHaveCSS("opacity", "1");`, which is how the desktop test checks the chevron's visibility. Confirm it fails with the coarse rule emptied as above.

## F2. The transparent chevron takes clicks on a row at rest

Low · Testing · Rejected · `cmd/ox/web/src/style.css:180`

A parent row's chevron is now rendered at `opacity: 0` at rest, so it takes pointer events over the end of a long title, where `display: none` took none.

### Evidence

With a mouse, the pointer reaches the chevron only by hovering its row, and hovering shows the chevron before any click. On a touch screen the chevron always shows. `.actions` at rest already works the same way.

### Action

Rejected: in normal use the chevron is never clicked while it is transparent.

## Checks run

- Screenshots in `/tmp/shots-main` and `/tmp/shots-branch` (rest, long-title hover, short-title hover): visually identical apart from the random workspace name. `metrics.json` (title, open button, and head boxes, title scroll and client widths) is identical across main and the branch.
- Spot-checked removals against "a session's chevron hides and shows every session beneath it":
  - `.actions` show rule back to `:focus-within`: fails at line 1097.
  - Chevron show rule without `:has(:focus-visible)`: fails at line 1103.
  - Chevron back to `display` toggling: fails at line 1083.
- Coarse-pointer chevron rule emptied: the touch test still passes (F1).
- Keyboard order open → chevron → ⋯ is asserted by the extended test (lines 1098–1106) and passes.
- `make test`: passed.
- `make e2e`: 117 passed, 4 skipped.
- `make live`: not required, since the change does not alter how Ox drives Claude Code. A `make dev` check in headless Chrome is not required either, since the change does not alter how the page reaches `oxd`.

## Verification 1

Checked the uncommitted tree on base `b5419ba`. Since the review, only `cmd/ox/web/e2e/session.spec.ts` changed; `style.css` is byte-identical to the reviewed copy.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The touch test asserts `toHaveCSS("opacity", "1")` on Parent's chevron before the tap. With the coarse-pointer chevron rule emptied, it fails (expected "1", received "0"); with the rule restored, it passes. |

No new findings.

Checks run: `make test` passed; `make e2e` 117 passed, 4 skipped. `make live` and the `make dev` check are not required, as in the review.

## Verdict

`Ready to commit`: F1 resolved, F2 rejected.
