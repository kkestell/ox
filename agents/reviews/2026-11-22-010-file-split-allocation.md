# Review of the file split allocation

## Scope and coverage

The uncommitted changes on `fix-f3` over `312f3da`: `cmd/ox/web/src/App.tsx`, `Panels.tsx`, `ox.tsx`, `style.css`, `e2e/workspace.spec.ts`, and `docs/todo.md`, checked against `agents/plans/2026-11-22-007-file-split-allocation.md` and the user's answer to its question: in a tight window the transcript and file viewer split evenly and the side panels keep their widths.

The change delivers the plan's Goal and keeps its Decisions. The stylesheet holds every column limit, `Sash` clamps travel to the rendered limits and writes only on change, `setSplit` stores a share, and the workspace-keyed `Show` is split in two with the same key. `docs/functional-requirements.md` is unchanged, as the plan's "Requirement changes: None" says, and **Resizing panels**, **Layout through navigation**, and **Narrow screens** hold. The todo entry for Audit F3 is removed.

Gaps:

- The composer pickers' anchoring under `.center`'s size containment rests on the implementer's headless Chrome check through `make dev`; this review did not repeat it.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                           |
| -- | -------- | ----------- | -------- | ------ | --------------------------------------------------------------- |
| F1 | Medium   | Correctness | Fix      | Open   | Sub-pixel slack lets a tight window's drag overwrite the split  |
| F2 | Low      | Correctness | Rejected | —      | Sash admits zero travel when the computed limits exclude it     |

## F1. Sub-pixel slack lets a tight window's drag overwrite the split

Medium · Correctness · Fix · `cmd/ox/web/src/Panels.tsx:102`

When the center's width is not a whole number of layout units after halving, Chrome resolves `min(240px, 50cqi)` to a rounded value and gives the leftover 1/64 px to one pane. That pane sits a fraction above its minimum, so `Sash` computes a range of a fraction of a pixel on one side of 0. Dragging the file split's sash toward that side passes the fraction to `setSplit`, which saves a share of about 0.5. The rendered widths do not move, but the saved split is gone: widening the window shows an even split instead of the one the user set. This is the behavior the plan's Goal removes.

Fractional widths are ordinary. Browser zoom makes them, and so does a side panel dragged by a pointer whose `clientX` has a fraction.

### Evidence

A temporary e2e test in the wide-screen describe, since removed: open `src/a.txt`, drag "Resize file viewer" −200 and record `main`'s width (311.2), drag "Resize sessions" +0.3, set the viewport to 820 × 900, drag "Resize file viewer" −35 and then +35, and set the viewport back to 1600 × 900. In the tight window the center was 243.703125 px, `main` 121.84375 and the viewer 121.859375, both with a computed `min-width` of `121.852px`. After widening, `main` was 511.3 px, half the center, and `near(main, split)` failed.

### Action

Make `Sash`'s range ignore sub-pixel slack on both sides. Round each end of the range toward 0 to whole pixels (`Math.ceil` for `lowest`'s bound and `Math.floor` for `highest`'s) before clamping it to include 0. A neighbor within a pixel of its limit then admits no travel toward it, and a tight center's range is exactly `[0, 0]`. Update the comment on the range to say it ignores less than a pixel of room on either side.

Keep the test in "the file split leaves each side its minimum and comes back when the file reopens" in `e2e/workspace.spec.ts`. Before narrowing the viewport, drag "Resize sessions" by a fraction of a pixel, such as 0.3, so the tight center has a fractional width. The block's existing ±35 drags and the final `near(main, split)` then guard the saved split. The test fails on the current tree, as the probe above did.

## F2. Sash admits zero travel when the computed limits exclude it

Low · Correctness · Rejected · `cmd/ox/web/src/Panels.tsx:102`

The implementer asked whether `Sash` should always admit a travel of 0, even when a neighbor renders a fraction past its limit and the computed range excludes 0.

### Action

Rejected: the choice is right. A range that excludes 0 would clamp the first pointer move to a nonzero fraction, and `Sash` would write a change the user never made. An inverted range would do the same on every move. Travel of 0 writes only the widths the layout already renders, and the change guard skips it until the pointer has moved away and back. F1 is a separate case: slack inside the limits on the far side of 0, which this choice does not touch.

## Checks run

- `make test` and `make e2e` on the final tree: reported passed by the implementer. Headless Chrome through `make dev` on ports 18380/18381: reported passed. `make live` is not required, since the change does not alter how Ox drives Claude Code.
- `npm run e2e -- workspace -g "sash|file split"` on the reviewed tree: both pass.
- Spot-checked removals, each restored afterward:
  - With the change guard bypassed for a travel of 0, the file split test fails.
  - With the clamp widened by 1e9 px on each side, the file split test fails.
  - With `main` and `.viewer` at `min-width: 0`, the file split test fails.
  - Without the side panels' `max-width: 640px`, the sash test fails.
- The F1 probe described above fails on the reviewed tree.

## Verification 1

The uncommitted tree on `fix-f3` over `312f3da`, after the fixes for F1. `Sash` now rounds each end of its range toward 0 to whole pixels before clamping it to include 0. The file split test drags "Resize sessions" 0.3 px before narrowing the viewport. The fix adds no regressions: whole-pixel rounding gives up less than a pixel at each limit, and the sash test's 640 and 160 bounds still hold.

| ID | Status   | Evidence                                                                                                                                        |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | "the file split leaves each side its minimum and comes back when the file reopens" fails with the `Math.ceil`/`Math.floor` removed and passes with them |

Checks run:

- `npm run e2e -- workspace -g "sash|file split"` with the rounding removed: the file split test fails. Restored: both pass.
- `make test` and `make e2e` on the final tree: reported passed by the implementer (108 passed, 4 skipped).

## Verdict

`Ready to commit`: F1 is resolved, and F2 is rejected.
