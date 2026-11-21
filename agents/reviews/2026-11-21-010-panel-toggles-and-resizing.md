# Panel toggles and resizable panels

## Scope and coverage

Reviewed the uncommitted changes on base `8f5ae5e` against
`agents/plans/2026-11-21-010-panel-toggles-and-resizing.md`: `cmd/ox/web/src`
(`App.tsx`, `Panels.tsx`, `Sidebar.tsx`, `FileTree.tsx`, `FileView.tsx`,
`SessionView.tsx`, `NewSessionView.tsx`, `ox.tsx`, `style.css`), the two e2e
specs, `docs/architecture.md`, and `docs/functional-requirements.md`. The
unrelated edits under `.claude/skills/` are out of scope.

The implementer's listed deviations from the plan (the `<hr>` sash, the
`.title` height assertion, no `?? 1` on `main`'s grow, the cursor on `:root`
and its descendants, the empty title row without an `h1`) are sound. The
missing `?? 1` is not the cause of F1. The requirements diff matches the
plan's Requirement changes word for word.

Gaps:

- `/ox-work`'s Tests proved and Checks were not passed on. Three added
  conditions were spot-checked by removal instead (see Checks run).

## Summary

| ID | Severity | Category    | Action | Status   | Title                                             |
| -- | -------- | ----------- | ------ | -------- | ------------------------------------------------- |
| F1 | High     | Correctness | Fix    | Resolved | Transcript fills half its space with no file open |

## F1. Transcript fills half its space with no file open

High · Correctness · Fix · `cmd/ox/web/src/App.tsx:229`

`main` takes `flex-grow: files()?.control.split()` whenever a session or draft
is selected, whether or not a file is open. With no viewer, `main` is the only
growing item, and a flex container hands out only that fraction of the free
space when the grow factors sum to less than 1. So with a session selected
and no file open, the transcript takes `split` of the width between the
panels (half, by default) and leaves the rest empty. That is the default view
of every session. It also breaks the plan's Split layout decision, "With no
viewer `main` fills the space alone".

The cause is that `main`'s share and the viewer's presence are decided in two
places: `main`'s style reads the split unconditionally, while the viewer
renders only under `control.file()`.

### Evidence

A temporary e2e probe at a 1600 px viewport selected a session with no file
open. The result: `nav 288 main 512 files 288 grow 0.5`. That leaves 512 px empty
beside the transcript. The new test does not catch this, because it measures
`main` only while a file is open.

### Action

Give `main` the split as its grow factor only while the viewer shows: derive
one accessor in `Layout`, such as
`share = () => (files()?.control.file() ? files()?.control.split() : undefined)`,
and use it for `main`'s `flex-grow`, so `main` falls back to the CSS `1`
whenever no file is open, whether nothing is selected or the file was closed.

Test: in `workspace.spec.ts`, "panel toggles and drags change the layout,
which lasts until reload", assert that with a session selected and no file
open, `main`'s width equals the viewport width minus `nav` and the files
panel (within 1 px). After the split drags, close the file and assert the
same again, so a dragged split does not leak into the single-pane layout.

## Checks run

- `make test`: passed.
- `make e2e`: 42 passed, 3 skipped, including the new test and the updated
  archived-session test.
- Probe for F1 (temporary spec, removed): `main` 512 px of 1024 px available
  with no file open.
- Removed conditions, each run against the new test, then restored:
  - the primary-button check in `Sash`'s `pointerdown`: fails (right-button
    drag resizes `nav`).
  - the clamp in `setSplit`: fails (split passes 240 px).
  - `Toggles`' `!tree()` guard: fails (two Toggle sessions buttons).
- `make live` and a `make dev` Chrome check are not required: the change
  neither drives Claude Code nor changes how the page reaches `oxd`.

## Verification 1

Checked the uncommitted tree on base `8f5ae5e` after the F1 fix: `Layout`'s
`share()` accessor in `App.tsx` and the added assertions in the panel-toggles
e2e test. No new findings.

| ID | Status   | Evidence                                                                                                                                                                |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `main` takes the split only while a file is open. "panel toggles and drags change the layout" fails with `share()` reverted to the split alone, and passes with the fix |

Checks run: `make test` passed; `make e2e` 42 passed, 3 skipped.

## Verdict

`Ready to commit`. F1 is resolved.
