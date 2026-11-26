# Review: tabs and splits

## Scope and coverage

Base `origin/main` merge-base; the uncommitted working tree on branch `tabs-and-splits`, against `agents/plans/2026-11-26-tabs-and-splits.md`. Web client only. The requirements diff matches the plan's Requirement changes, except the two sentences the parent session set aside (File tree header, Changes updates "an open file's changes").

Gaps:

- Not run: `make screenshots` (the plan regenerates README images only on request), `make live` and a headless-Chrome check through `make dev` (the change does not alter how Ox drives Claude or how the page reaches `oxd`).
- How the layout looks is for the user to judge.

## Summary

| ID | Severity | Category    | Action | Status | Title                                                        |
| -- | -------- | ----------- | ------ | ------ | ------------------------------------------------------------ |
| F1 | Medium   | Correctness | Fix    | Resolved | Splitting or unsplitting remounts a group and its chat view |

## F1. Splitting or unsplitting remounts a group and its chat view

Medium · Correctness · Fix · `cmd/ox/web/src/TabArea.tsx:90`

`NodeView` renders a group and a split through different `Match` branches, and a split's children are keyed by `nodeKey`. A drop on an edge replaces the group's node with a split that contains it, so Solid disposes the old `GroupView` and builds a new one. The same happens when a closed or moved tab removes a group and the parent split collapses to one child, and when a cross-direction split changes a child's `nodeKey` from `g1` to `sg1`. The chat view inside is rebuilt: the transcript loses its scroll position (it returns to the bottom) and its `atBottom` state. The plan's "Chat stays mounted" decision exists to prevent this, and it holds only while the layout's shape is unchanged. A common sequence loses the user's place: drag a file to the right, read it, close its tab.

### Evidence

A temporary e2e spec marked the chat's `[role=log]` element with `data-probe`, then dragged a file tab to the right edge of the chat group. After the split the marked element is gone (`toHaveCount(1)` received 0). The spec was removed.

### Action

Keep the transcript's scroll offset in `SessionControl` (one fact, owned there like the composer text), write it from the log's `onScroll`, and restore it when `SessionView` mounts. This also covers a chat tab moved to another group and the crossing to the narrow layout, which remount the view by design. The alternative is a flat render of groups keyed by id with computed rectangles, which never remounts a group but replaces the nested `NodeView`; it costs more than it saves.

Test: e2e in `workspace.spec.ts`, beside the drag specs. Scroll a long transcript up, drag a file tab to the group's right edge, assert the transcript's `scrollTop` is unchanged, then close the file tab and assert it again. It fails without the fix.

## Checks run

- `make test`: passed (Go, 42 Node tests including `tabs.test.mjs`).
- `make e2e`: 215 passed.
- Probe for F1: failed as described, then removed; the spec file is restored.
- Not spot-checked by removal: the implementer's Tests proved list was not supplied to this review.

## Verification 1

Working tree after the implementer's fix. `SessionControl` holds the transcript's scroll offset (`ox.tsx`), `SessionView` writes it on scroll and restores it on mount. The new e2e spec "the transcript keeps its scroll position when its group splits and when the split closes" fails without the restore (expected -3106, received 0) and passes with it. `make test` passed (42 Node tests); `make e2e` passed (216). The two approved requirement edits match the code: the file tree has no header, and no open-file changes view remains.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | Spec fails with the restore removed, passes with it |

No new findings.

## Verdict

`Ready to commit`.
