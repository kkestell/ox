# Clip session titles and workspace names at the row's edge

## Goal

A session's title in the sidebar ends in an ellipsis at a width that leaves room for the row's ⋯ button, even while that button is hidden. Show each title in full and let the row's right edge clip it, with no ellipsis. While the ⋯ button shows, a gradient behind it fades the title from transparent on its left to the row's background on its right, so the icon stands clear of the text beneath it. Workspace names get the same treatment: no ellipsis, clipped at the header row's edge, with the fade behind their ⋯ and + buttons. Resolves [#1](https://github.com/kkestell/ox/issues/1).

## Related code

- `cmd/ox/web/src/Sidebar.tsx` — `SessionRow` renders the row: `.head` holds the `open` button (status and `.title`) and the parent's chevron; `MenuButton` ("Session actions") follows `.head` as a flex sibling, so it reserves its width even at opacity 0.
- `cmd/ox/web/src/Sidebar.tsx`, `Sidebar` — each workspace's `<header>` holds its `.name` heading, then `MenuButton` ("Workspace actions") and the "New session" button.
- `cmd/ox/web/src/style.css` (the `nav header` rules, about lines 140–144) — `.name` ends in `text-overflow: ellipsis` inside the header's 0.5rem padding; both buttons are `position: absolute` at the right, over the name's end with no fade, and hidden until the header is hovered or focused.
- `cmd/ox/web/src/style.css` (the `nav li` rules, about lines 146–180, and the `@media (pointer: coarse)` block) — `.title` ends in `text-overflow: ellipsis`; `.head` clips with `overflow: hidden` and a 3px margin and padding for focus rings; the chevron is `position: sticky; right: 0` inside `.head` with a fade from `--row-background`; `nav li button.icon` sets the ⋯ button's size, `margin-right: -0.25rem`, and hover opacity.
- `cmd/ox/web/e2e/session.spec.ts`, test "a session's chevron hides and shows every session beneath it" — already builds a long-titled parent with a child, checks the chevron's place against the ⋯ button, and samples pixels against the row's hover color.

## Decisions

- **Layout.** The ⋯ button moves into a new `<div class="actions">` positioned absolutely at the row's right end (`top: 0; bottom: 0; right: 0`), out of the flex flow. `nav li` drops its right padding, so `.head` (and the title's clip) reaches the row's right edge. `.title` loses `text-overflow: ellipsis` and keeps `overflow: hidden; white-space: nowrap`, so it clips mid-glyph at the edge.
- **The fade.** `.actions` has `padding: 0 0.25rem`, `border-radius: inherit` (so its solid end follows the row's rounded corners), and the chevron's background, `linear-gradient(to right, transparent, var(--row-background) 0.25rem)`. The ⋯ icon keeps its place, 0.25rem from the row's edge, and keeps its own hover square, because the gradient sits on the wrapper rather than the button. The `margin-right: -0.25rem` on `nav li button.icon` goes.
- **Showing.** The opacity rules that hide and show the ⋯ button apply to `.actions` instead, so the gradient shows and hides with the icon: hidden until the row is hovered or focused, always shown under `pointer: coarse`. With the row neither hovered nor focused, the title runs to the row's edge uncovered.
- **Chevron.** A `--actions` custom property on `nav li` holds the width of `.actions`. The chevron's sticky `right` offset becomes the distance from `.head`'s clip edge to the left edge of `.actions` (`.head` overhangs the row by 3px), so a long parent's chevron sticks just left of the fade instead of under it. While the row shows a chevron (`nav li:has(button.chevron-right)`), `.actions` takes a solid `var(--row-background)`: the chevron's own fade already covers the title from that point, and a second fade would let a sliver of title show between the two icons. When the title is short the chevron follows it and nothing lies under `.actions`, so the solid background changes nothing there.
- **Workspace header.** The header's ⋯ and + buttons move, in that order, into a `<div class="actions">`, the same element and the same `nav :is(header, li) .actions` rules as a session row's, which hold its position, padding, fade, rounded corners, and opacity. The buttons lose their own `position: absolute` and `right` offsets and sit side by side in it; the + icon keeps its place, 0.125rem from the header's edge. `nav header` drops its right padding, and `.name` loses `text-overflow: ellipsis`, so the name clips at the header's edge. `nav header` sets `--row-background` (`--sidebar`, or `--sidebar-accent` on hover) and paints its background from it, as `nav li` does, so the fade matches the header's hover color.
- **Clicks.** A click on `.actions` outside the ⋯ button lands on no button, so the row's `onClick` opens the session, as a click on the title does now.
- **Requirements:** Session labels — each label still shows; only its clip changes. Collapsing child sessions — the chevron still shows after the title while the row is hovered or focused, and stays clear of the ⋯ button. Neither text changes. The change is page-only styling, with no request to `oxd`.
- **Construction:** The fade's color comes from `--row-background`, the single source each row or header, the chevron, and `.actions` read, so hover and selection recolor the fade with the row. `.actions`'s width is `var(--actions)`, set on `nav li` (0.25rem fade, 1.5rem ⋯, 0.25rem trailing) and on `nav header` (0.25rem fade, 1.5rem ⋯, 1.5rem +, 0.125rem trailing); the chevron's offset reads the same property.

## Check your assumptions

1. **Where a long title is cut off.** Kept.
   - **Plan:** The title runs to the edge of its row, the rounded highlight that shows on hover and selection, which sits half a rem inside the sidebar's border. The fade behind ⋯ ends at that same edge.
   - **Alternative:** The title runs past its row to the sidebar's border itself. A hovered or selected row's text would then spill outside its highlight, and the fade would need to reach across the gap beside the row.
2. **Workspace names keep their ellipsis.** Changed: workspace names get the same edge clip, with no ellipsis, and the same fade behind their ⋯ and + buttons.
   - **Plan:** Only session titles change. A long workspace name still ends in an ellipsis, and its hover icons still sit over its end without a fade.
   - **Alternative:** Give workspace names the same edge clip and fade behind their ⋯ and + icons, so both kinds of row read alike, at the cost of widening the change beyond the issue.

## Test plan

All in `cmd/ox/web/e2e/session.spec.ts`, test "a session's chevron hides and shows every session beneath it", whose setup already makes a long-titled parent with a child (e2e):

- Give the long parent's child a long title too (`Long child ${words(40)}`), so the test has a long title with no chevron. Update its references.
- With the mouse away from the sidebar, the long parent's title reaches its row's right edge: the right edge of `sessionButton(page, long)` is within a pixel of its row's right edge. Its title text (`getByText(long, { exact: true })` in the row) has CSS `text-overflow: clip`. Both fail before the change, where the title stops short of the ⋯ button's slot and ends in an ellipsis.
- Hovering the long child (no chevron): a strip as tall as the title's text, at the left end of the "Session actions" button's box before its icon, matches the same strip in an empty part of the row. This shows the fade is solid behind the icon although the title runs beneath it.
- Hovering the long parent (chevron shown): the strip just left of the "Session actions" button, inside `.actions`'s fade zone, matches the row's empty strip, so no title shows between the chevron and the ⋯ icon. The existing checks that the title keeps its width and the chevron stays clear of the ⋯ button stay.

In `cmd/ox/web/e2e/workspace.spec.ts`, a new test, "a long workspace name runs to its header's edge beneath its buttons' fade" (e2e). No existing test adds a workspace whose name overflows the header:

- Add a workspace at a fresh temporary directory whose name is long enough to overflow the sidebar (about 100 characters), removed in a `finally`, as "another workspace's draft shows that workspace's tree" does.
- With the mouse away from the sidebar, the name's heading reaches the right edge of its workspace's region, within a pixel, and has CSS `text-overflow: clip`. Both fail before the change.
- Hovering the header: a strip as tall as the name's text, at the left end of the "Workspace actions" button's box before its icon, matches the same strip in an empty part of the header.

## Implementation plan

1. `cmd/ox/web/src/Sidebar.tsx`: in `SessionRow`, wrap `<MenuButton label="Session actions" …/>` in `<div class="actions">`. Update the `SessionRow` comment to say the title runs to the row's edge beneath the actions' fade. In `Sidebar`, wrap the header's "Workspace actions" `MenuButton` and "New session" button in `<div class="actions">`.
2. `cmd/ox/web/src/style.css`:
   - `nav li`: right padding 0; add `--actions` (the width of `.actions`: 0.25rem fade, 1.5rem button, 0.25rem trailing).
   - `nav li .title`: drop `text-overflow: ellipsis`.
   - `nav header`: right padding 0; `--row-background` (`--sidebar`, `--sidebar-accent` on hover) and `background: var(--row-background)` in place of the hover background; `--actions` for its two buttons. `nav header .name`: drop `text-overflow: ellipsis`. `nav header button.icon`: drop `position: absolute` and `right`, and remove the `nav header button.icon.ellipsis` rule.
   - Add `nav :is(header, li) .actions` (absolute at the right, flex-centered, width `var(--actions)`, padding, `border-radius: inherit`, the gradient) and `nav li:has(button.chevron-right) .actions { background: var(--row-background); }`.
   - `nav li button.icon`: drop `margin-right: -0.25rem`.
   - Chevron: sticky `right` from `--actions` and `.head`'s 3px overhang.
   - Opacity: replace `nav header button.icon` and `nav li button.icon` with `.actions` under each in the hide and hover/focus-within rules and in the `pointer: coarse` block; the chevron keeps its `display` rules. Update the `.head` and chevron comments to describe the new clip and fade.
3. `cmd/ox/web/e2e/session.spec.ts` and `cmd/ox/web/e2e/workspace.spec.ts`: the cases above.

## Requirement changes

None.
