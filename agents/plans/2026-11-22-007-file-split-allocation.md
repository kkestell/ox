# The layout allocates the file split within the space it has

## Goal

When the side panels leave less than 480 px for the transcript and the file
viewer, dragging the boundary between them in either direction saves the same
split, which can shrink the transcript to a sliver, and the session keeps that
split after the window widens. A saved split also renders below the 240 px
minimum whenever the window is narrower than when it was set.
[Audit F3](../audits/2026-11-22-001-conversation-launch-and-layout.md#f3-the-file-split-assumes-space-the-side-panels-can-take-away),
listed in `docs/todo.md`.

After the change, the stylesheet states each column's limits once, the browser
allocates the widths from them, and every sash drag stops where a neighbor
reaches the limit the layout already rendered. Each center pane gets at least
240 px, or half the center when the center has less than 480 px. In a tight
window the two panes split the center evenly and the file split cannot be
dragged; the session's saved split returns when the window widens.

## Related code

- `cmd/ox/web/src/App.tsx:91–99,150,184–227` — `Layout`: the side panels'
  widths, `main`'s `flex-grow` from the session's split, and the keyed `Show`
  that holds the file viewer, its sash, and the file tree.
- `cmd/ox/web/src/FileView.tsx:9,29` — the viewer's `share` prop as its
  `flex-grow`.
- `cmd/ox/web/src/ox.tsx:178–181,255–260` — the session control's `split` and
  `setSplit`, which clamps a drag to `[240, total − 240]`. Below 480 px that
  interval is inverted and every drag writes `(total − 240) / total`.
- `cmd/ox/web/src/Panels.tsx:3–4,45–48` — `panelWidth` clamps each side panel
  to `[160, 640]` in JavaScript.
- `cmd/ox/web/src/Panels.tsx:73–97` — `Sash` samples its neighbors' rendered
  widths at drag start and passes raw pointer travel.
- `cmd/ox/web/src/style.css:137,198,345,379,404–420` — `nav`, `main`,
  `aside.files`, `.viewer`, and the narrow layout's rules.
- `cmd/ox/web/e2e/workspace.spec.ts:482–483,536–578` — the wide-layout
  describe at a 1600 px viewport, and the sash and file split tests.

## Decisions

- **One allocation rule, in the stylesheet.** `main` and `.viewer` sit in a new
  `<div class="center">` that is a size container. Both panes take
  `min-width: min(240px, 50cqi)`, so the minimum is 240 px when the center has
  room for both and half the center when it does not; the pair can never
  overflow the center. `nav` and `aside.files` take `min-width: 160px;
  max-width: 640px`, replacing `panelWidth`. Flexbox then derives every
  rendered width from the stored preferences and these limits: a split whose
  share falls below a pane's minimum renders at that minimum. Probed in Chrome:
  a share of 0.1 in a 1000 px center renders 240/760, and any share in a 300 px
  center renders 150/150; `getComputedStyle` resolves the minimum to `240px`
  and `150px`.
- **Sash clamps every drag to its neighbors' rendered limits.** At drag start,
  `Sash` reads each neighbor's border-box width and computed `min-width` and
  `max-width` (`none` is unbounded) and clamps the pointer's travel to the
  range that keeps both within their limits. The browser has already rendered
  both neighbors within those limits, so the range always contains 0 and is
  never inverted. `Sash` calls the drag function only when the clamped travel
  changes, so a drag that cannot move writes nothing and leaves the saved
  split alone. This is the single feasibility rule for all three sashes; the
  side panels' sashes border the `.center` box, whose `min-width` is 0, so
  their behavior is unchanged.
- **The control keeps only a share.** `setSplit(share)` writes the
  transcript's share, which the drag computes as `(before + dx) / (before +
  after)`. The clamp and both 240s leave `ox.tsx`. Because `Sash` passes only
  travel within the rendered limits, the share is always in `(0, 1)`.
- **Requirements.** **Resizing panels** holds at every window width 768 px or
  wider: each drag moves its boundary until a neighbor reaches its limit.
  **Layout through navigation** holds: each session and draft keeps its split
  through switching views, and a tight window renders it clamped without
  changing it, so widening shows the saved split again. **Narrow screens** is
  unaffected: the `.center` box is the only flex item beside the fixed drawers,
  and `.narrow main:has(~ .viewer)` still matches because `main` and `.viewer`
  stay siblings.
- **Construction.** The invariant "both center panes fit the center and each
  gets its minimum" is enforced by the stylesheet's `min(240px, 50cqi)`, the
  one source of the 240 px rule. The invariant "a stored width or split is one
  the layout can render" is enforced by `Sash`, which derives its range from
  the rendered limits rather than from constants.
- **Containment.** `container-type: inline-size` on `.center` affects nothing
  positioned inside it: the composer's pickers and the dialogs are top-layer
  popovers and modals, and `main` and `.viewer` hold no fixed-position
  elements.

## Check your assumptions

1. **In a tight window, the transcript and file viewer split evenly.**
   - **Plan:** When the side panels leave the transcript and an open file less
     than 480 px together, the two split that space in half and the boundary
     between them does not move; the session list and file tree keep their
     widths. Widening the window brings back the split you set.
   - **Alternative:** The session list and file tree shrink to keep the
     transcript and file at least 240 px each. Their widths would then jump
     when you open or close a file or switch to a session without one, and
     dragging a squeezed panel's edge would move it the wrong way.

## Test plan

- A tight center splits evenly, ignores split drags, and keeps the saved split:
  e2e, appended to "the file split leaves each side its minimum and comes back
  when the file reopens" in `e2e/workspace.spec.ts`, whose setup already leaves
  a saved split that is not even. After its last `near(main, split)`, set the
  viewport to 820 × 900; assert `main` is within a pixel of the viewer's width;
  drag "Resize file viewer" −35 and assert the widths are unchanged; drag +35
  and assert the same; set the viewport back to 1600 × 900 and
  `near(main, split)`. On the current code, the first assertion fails (the
  saved share renders about 74/169) and the −35 drag leaves the transcript near
  4 px.
- The existing sash test's 640 and 160 bounds and the file split test's 240
  bounds now come from the stylesheet through `Sash`'s clamp; they stay as
  they are and cover the move of the limits out of JavaScript.
- Check in headless Chrome against a `make dev` stack on spare ports that the
  composer's model and effort pickers still open anchored under their buttons
  with `.center` as a size container.

## Implementation plan

1. `cmd/ox/web/src/style.css`: add `min-width: 160px; max-width: 640px` to
   `nav` and `aside.files`. Add a `.center` rule (`flex: 1 1 0; min-width: 0;
   height: 100%; display: flex; container-type: inline-size`) with a comment
   that it is the space the transcript and file viewer share. Change `main`'s
   and `.viewer`'s `min-width: 0` to `min-width: min(240px, 50cqi)`, with a
   comment stating the rule.
2. `cmd/ox/web/src/Panels.tsx`: remove `clamp` and `panelWidth`; `setLeftWidth`
   and `setRightWidth` write their argument. In `Sash`, read each neighbor's
   width, `min-width`, and `max-width` at drag start, clamp travel to the
   range that keeps both within them, and call the returned function only when
   the clamped travel differs from the last value passed (starting at 0).
   Update `Sash`'s comment: a drag stops where either neighbor reaches its
   minimum or maximum width.
3. `cmd/ox/web/src/ox.tsx`: `setSplit(share: number)` writes the share; update
   the comment on `split`.
4. `cmd/ox/web/src/App.tsx`: wrap `main` and the file viewer in
   `<div class="center">`. Split the workspace-keyed `Show` in two with the
   same key: one inside `.center` for the viewer's sash and `FileView`, one
   after it for the tree's sash and `FileTree`, keeping the comment on keying.
   The viewer's drag becomes `(dx) => control.setSplit((before + dx) / (before
   + after))`.
5. `cmd/ox/web/e2e/workspace.spec.ts`: extend the file split test as above.
6. `docs/todo.md`: remove the entry "Dragging the file split can collapse the
   transcript."

## Requirement changes

None.

## Documentation updates

- `docs/todo.md`: remove the entry "Dragging the file split can collapse the
  transcript."
