# Show sidebar row buttons for keyboard focus only

## Goal

Clicking a parent session's chevron, or any other button in a sidebar row or workspace header, focuses that button, and the row keeps its chevron and ⋯ button shown after the pointer leaves until something else takes focus. After this change, a row shows them while hovered or while it holds keyboard focus, so they hide when the pointer leaves a clicked row and stay shown for a row reached from the keyboard. Resolves [#12](https://github.com/kkestell/ox/issues/12).

## Related code

- `cmd/ox/web/src/style.css:184` — `nav :is(header, li):is(:hover, :focus-within) .actions { opacity: 1; }` shows a row's or header's ⋯ (and the header's +) while it holds any focus.
- `cmd/ox/web/src/style.css:186` — `nav li:is(:hover, :focus-within) button.chevron-right { display: inline-flex; }` shows a parent's chevron the same way.
- `cmd/ox/web/src/style.css:56` — the global `:focus-visible` ring; the browser's `:focus-visible` heuristic is what separates keyboard focus from a click.
- `cmd/ox/web/src/style.css` `@media (pointer: coarse)` — touch screens show the buttons always; unchanged.
- `cmd/ox/web/src/Sidebar.tsx` `SessionRow` — the row's `button.open`, its chevron, and its `.actions` with the ⋯ `MenuButton`; all are focusable descendants the selectors match.
- `cmd/ox/web/e2e/session.spec.ts` — "a session's chevron hides and shows every session beneath it" already clicks the selected row's chevron and checks it hides when the pointer leaves an unfocused row.

## Decisions

- **Cause.** `:focus-within` matches mouse focus as well as keyboard focus. Both rules change `:focus-within` to `:has(:focus-visible)`, which matches only when a descendant shows a focus ring. Chromium and Safari do not give `:focus-visible` to a button focused by a click, and do give it to one reached with Tab. The fix covers the session row's `button.open` too: clicking a session's title also left its ⋯ shown until focus moved, by the same rule.
- **Chevron stays rendered (amended; the top-level agent decided this).** With
  `display: none`, the chevron lost focus the moment it hid, and Tab from a
  row's title then skipped it. The chevron stays rendered and focusable on a
  parent row (`display: inline-flex`) and is hidden with `opacity: 0`, the way
  `.actions` is. Both rules use `:is(:hover, :has(:focus-visible))`, so focus
  never sits on a non-rendered element. The chevron already occupies its space
  when shown and shown never moves the title, so a row at rest keeps its title
  box, clip, and ellipsis; screenshots of main and this branch match.
- **Requirements.** *Collapsing child sessions* says the chevron shows "while its row is hovered or focused". Keyboard focus still shows it and hover still shows it; a row focused by a click no longer does, which is what the issue asks and what the requirement's reader expects. The requirement's text stays. No request, `oxd` state, or lifecycle is involved.
- **Scope in `style.css`.** Only the chevron's base rule, the two show rules, and the coarse-pointer chevron rule change. Other worktrees are editing `style.css` for #8, #9/#15, and #16; this change touches no other rule.

## Check your assumptions

None.

## Test plan

- Extend "a session's chevron hides and shows every session beneath it" in `cmd/ox/web/e2e/session.spec.ts` (e2e). Right after the step that clicks Child's chevron and checks `aria-expanded="false"`, while the chevron holds focus from the click:
  - `page.mouse.move(0, 0)`, then expect `chevron(page, "Child")` and Child's `.actions` (`sessionItem(page, "Child").locator(".actions")`) to have CSS `opacity` `"0"`. Both fail before the change, since the clicked chevron keeps `:focus-within` true. Playwright counts an `opacity: 0` element as visible, so the spec checks `opacity`, not visibility.
  - Press `Shift+Tab` then `Tab` to move focus to `button.open` and back to the chevron from the keyboard, with the pointer still away. Expect the chevron to be focused with `opacity` `"1"`, and Child's `.actions` to have `opacity` `"1"`. Press `Tab` once more and expect the ⋯ button focused.
  The rest of the test continues from there unchanged; `toggleChildren` hovers before clicking, so later steps are unaffected.

## Implementation plan

1. In `cmd/ox/web/src/style.css`, change line 184 to `nav :is(header, li):is(:hover, :has(:focus-visible)) .actions { opacity: 1; }` and line 186 to `nav li:is(:hover, :has(:focus-visible)) button.chevron-right { opacity: 1; }`. In the chevron's base rule, replace `display: none` with `display: inline-flex; opacity: 0`. In the `@media (pointer: coarse)` block, change the chevron rule to `opacity: 1`.
2. Add the assertions above to `cmd/ox/web/e2e/session.spec.ts`.

## Requirement changes

None.
