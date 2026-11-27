# Keep tab controls reachable

## Goal

Four findings in [#150](https://github.com/kkestell/ox/issues/150) (audit `agents/audits/2026-11-26-main.md`), all in the web client:

- **F7.** Side-by-side groups can need more width than the tab area has, so they extend behind the Files panel and their controls cannot be clicked.
- **F13.** Opening a file from the tree can leave its selected tab outside the clipped tab strip.
- **F25.** With no session or draft selected, the panel button turns on and, on a narrow screen, dims the page with no drawer.
- **F26.** The narrow tab menu lists two `README.md` files identically and drops the chat tab's directory.

## Related code

- `cmd/ox/web/src/style.css:306-311` — `.center` minimum is one group plus the panel (`--beside`); `:468-473` give each split cell its own minimum, which the center does not count.
- `cmd/ox/web/src/Panels.tsx` — `Sash` limits a drag by the neighbors' computed `min-width`; `.tab-area` has minimum 0, so the right sash never protects the groups.
- `cmd/ox/web/src/App.tsx:132-142,176,222-237` — `.center`'s inline `--tree`, `BareStrip` with its panel button, and the narrow scrim.
- `cmd/ox/web/src/tabs.ts` — layout operations; `splitAt` and `placeTab` build the layout a drop would produce.
- `cmd/ox/web/src/TabArea.tsx` — `Strip`, `GroupView` drag handlers, `PhoneBar`, `labelOf`/`titleOf`.
- `cmd/ox/web/src/TabPicker.tsx` — the narrow menu.
- `cmd/ox/web/style.test.mjs` — requires custom properties set inline to be listed.

## Decisions

- **Construction (F7).** The tab area's minimum width is a function of its layout: `columns(layout)` × group minimum, where `columns` is 1 for a group, the sum of the children's for a row split, and the maximum for a column split. It lives in `tabs.ts` and is the one source: CSS, the panel sashes and the drop check all read it. `App` sets `--columns` on `.center` (1 on the narrow layout); `.center` minimum becomes `columns × --group-min-width + --beside`; `.tab-area` gets `min-width: calc(var(--columns) * var(--group-min-width))`. `Sash` already reads computed minimums, so the session-list and right-panel sashes then stop where the groups reach their minimum, with no new code in `Panels.tsx`.
- **Sharing narrow space (F7).** Follows VS Code's split view: every part (side bars, each editor group) has a minimum size, and a sash drag stops where a neighbor reaches its minimum. Panels give way before groups do (a panel never shrinks the tab area below its columns), and a split that cannot fit is not offered. Concretely, a drop on a group's left or right edge is accepted only when `columns(placeTab(layout, tab, target)) × group minimum` fits in the tab area's current width; otherwise the edge shows no preview and the drop does nothing, and the user frees room by narrowing a panel. Top and bottom edges are always accepted. A window narrowed after the split below the layout's total minimum leaves `.layout` scrolling horizontally (`overflow-x: auto`) instead of clipping, so every control stays reachable.
- **Ownership (F13).** `Strip` scrolls its own `.tabs` element so the active tab is inside it (`scrollLeft` adjusted to the tab's offsets; not `scrollIntoView`, which would also scroll ancestors). It runs in an effect keyed on the group's active tab key, so any activation (tree click, tab click, drop, close) brings it into view. No new state.
- **Construction (F25).** The panel exists only where `Workspace` renders it. The blank page has no panel, so `BareStrip` drops its panel button and the sessions button stays. The narrow scrim's condition uses `panels.right() && hasPanel()`, where `hasPanel` is the same `selected() && files()` test that chooses `Workspace` over `BareStrip`, so the scrim is derived from the rendered drawer.
- **Construction (F26).** `PickerTab` gains `title` (from `titleOf`: path for a file, directory for the chat) and `detail` (a file's parent directory, shown as muted text after the label, absent at the root). Menu items and the picker button carry `title`. Close buttons keep their `Close <label>` names.
- **Requirements.** *Splitting tab groups*, *Resizing panels*, *Tabs*, *Panel toggles*, *Session header* and *Narrow screens* are touched; behavior holds through reload (layout resets), switching sessions (layout is per session, `columns` is derived from the current one) and the breakpoint (`columns` is 1 when narrow, and the tabs are kept).

## Check your assumptions

The user approved the plan's choice on all three, and the requirement changes below.

1. **A split that does not fit is refused.**
   - **Plan:** With both panels open on a small wide screen, dragging a tab to a group's left or right edge shows no split and does nothing; narrowing a panel makes room. Panel dividers stop where the groups reach their minimum width. This is how VS Code's split view treats minimum sizes.
   - **Alternative:** Allow the split and shrink the panels automatically to make room, which changes the panel widths the user set and is harder to predict.
2. **The blank page has no panel button.**
   - **Plan:** With no session or draft selected there is no panel to show, so its button is gone.
   - **Alternative:** Keep the button, greyed out. It costs a control that does nothing.
3. **The narrow tab menu shows each file's folder.**
   - **Plan:** Each file in the menu shows its folder after its name, so two `README.md` files can be told apart on a phone, which has no hover tooltip.
   - **Alternative:** Rely on the long-press tooltip; duplicates stay indistinguishable at a glance.

## Test plan

- `columns` (row, column, nested, after `placeTab`) — `tabs.test.mjs`.
- A split that does not fit is refused at an 840 px viewport with both panels open; fits after narrowing a panel — `workspace.spec.ts` beside the group-drop tests.
- With a two-group split, `Resize panel` and `Resize sessions` stop at the groups' minimums and both groups' close and action buttons receive pointer input (extend the sash test at `workspace.spec.ts:1118`, and assert Files stays in the viewport).
- Opening a file whose tab is outside the strip brings its tab fully inside the strip — `workspace.spec.ts` near the tab tests.
- Blank page: no `Toggle panel` button; on a narrow screen no scrim after the right drawer was open in a session and the selection cleared — `workspace.spec.ts`.
- Narrow: two `README.md` files in different folders are distinguishable in the menu; the chat's directory is on the menu item's title — `workspace.spec.ts` in the narrow tests.
- `style.test.mjs` passes with `--columns` added to its inline list.

## Implementation plan

1. `tabs.ts`: add `columns(node)` and `fits(layout, tab, target, width, min)` (or inline the check in the caller); unit tests.
2. `style.css`, `App.tsx`: `--columns` on `.center`; minimums on `.center` and `.tab-area`; `.layout` `overflow-x: auto`; `style.test.mjs` inline list.
3. `TabArea.tsx`: skip `preventDefault` and the preview for a left/right zone that does not fit; scroll the active tab into view in `Strip`; pass `title` and `detail` to `PickerTab`.
4. `TabPicker.tsx`, `style.css`: render `detail`, apply `title`.
5. `App.tsx`: drop the bare strip's panel button; derive the scrim from `hasPanel`.
6. e2e specs as in the test plan.

## Requirement changes

- **Changed: Splitting tab groups.** Old: "...onto the edge of a group opens a new group beside or above or below it; ... On a wide screen the boundaries between groups resize by dragging." New: append "A split beside a group is offered only while every group would keep its minimum width; the panels' boundaries stop where the groups reach it."
- **Changed: Panel toggles.** Append: "The blank page, with no session or draft, has no panel and no panel button."
- **Changed: Narrow screens.** Append to the picker's menu: "each file's folder shows beside its name."
