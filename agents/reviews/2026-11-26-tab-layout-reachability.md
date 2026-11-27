# Keep tab controls reachable

## Scope and coverage

Uncommitted changes in the worktree on branch `tab-layout-reachability`, against base `git merge-base origin/main HEAD`, checked against `agents/plans/2026-11-26-tab-layout-reachability.md` (issue #150: F7, F13, F25, F26). Web client only; no child sessions.

The requirements diff in `docs/functional-requirements.md` matches the plan's three changes word for word. No other requirement text changed.

Gaps:

- Phone-only behavior (the drawer and menu on a real touch device) was checked only in Playwright's phone project.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                        |
| -- | -------- | ------------ | -------- | ------ | ------------------------------------------------------------ |
| F1 | Low      | Correctness  | Rejected | —      | A tree drag of an already-open file counts as a new tab       |
| F2 | Low      | Construction | Rejected | —      | `.center` has no CSS default for `--columns`                  |
| F3 | Low      | Correctness  | Rejected | —      | Re-activating the active tab does not rescroll the strip      |

## F1. A tree drag of an already-open file counts as a new tab

Low · Correctness · Rejected · `cmd/ox/web/src/TabArea.tsx:173`

The drag-over check has no key for a drag from the file tree and uses a new file tab. If that file is already open as the only tab of another group, the real result would have one group fewer than the check counts, so a split that would fit can be refused.

### Action

Rejected. The error is conservative (it refuses, never overflows), needs a tree drag of an open file that is alone in its group onto a full-width edge, and `drop` uses the real tab. Tracking the tree's path at drag start costs more than it saves. The drag-start key choice is otherwise sound: tab drags read the key recorded at `onDragStart` and cleared at `onDragEnd`, because `dataTransfer` is unreadable during dragover.

## F2. `.center` has no CSS default for `--columns`

Low · Construction · Rejected · `cmd/ox/web/src/style.css:309`

`App` sets `--columns` inline on every render of `.center`, including the blank page (`columnsOf` returns 1) and the narrow layout (forced to 1). No state leaves it unset, and `style.test.mjs` lists it as an inline property. A CSS default would be a second source for the same fact.

### Action

Rejected.

## F3. Re-activating the active tab does not rescroll the strip

Low · Correctness · Rejected · `cmd/ox/web/src/TabArea.tsx:333`

The effect is keyed on the active tab's key, as the plan decides. Clicking the tree entry of the already-active tab after scrolling the strip away leaves the strip where it is.

### Action

Rejected. The plan keys the effect on the active key deliberately and adds no state; the audit finding concerns opening a file that is not yet active.

## Checks run

- `make test`: passed (lint and 50 Node tests, including the new `columns` and `fits` tests).
- `npm run e2e -- workspace`: 88 passed.
- Removed each guard and ran its test; each failed without it:
  - Drop check (`allowed` always true): the split-refusal spec failed.
  - Scrim without `hasPanel`: the blank-page phone spec failed.
  - Strip scroll disabled: the "brings its tab into the strip" spec failed.
- Not spot-checked by removal: `.tab-area` and `.center` minimums (covered by the sash spec, which asserts both groups keep 239 px and Files stays in the viewport), the picker `detail` and `title` (the phone spec asserts both), and the bare strip's missing button (`toHaveCount(0)`).

## Verdict

Ready to commit. No `Fix` or `Record` findings.
