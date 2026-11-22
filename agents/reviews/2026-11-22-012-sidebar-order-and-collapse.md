# Newest sessions first, and collapsible child sessions

## Scope and coverage

Uncommitted changes on `wt-sidebar` over `dac4fb8`: `cmd/ox/web/src/Sidebar.tsx`,
`cmd/ox/web/src/style.css`, `cmd/ox/web/e2e/session.spec.ts`,
`docs/functional-requirements.md`, and `docs/todo.md`, against
`agents/plans/2026-11-22-009-sidebar-order-and-collapse.md`. Checked in
headless Chrome through a `make dev` stack on ports 18480 and 18481 with
temporary XDG directories and the fake harness.

The implementer's departures were checked:

- No biome-ignore on the `li`: `npm run lint` passes without it.
- The chevron is a 1rem icon in a 1.25rem button with a -0.25rem margin. The
  margin offsets the `li`'s 0.375rem gap, so the icon box stands 0.25rem after
  the title, as in the reference. It is correct only while the chevron is
  collapsed; see F1.
- The `li`'s own click selects only when its target is the `li`. Clicks on the
  title button, the chevron, and ⋯ have their own targets, so none is handled
  twice; the chevron and ⋯ leave the selection and URL unchanged, a click on
  the row's empty space or a child row's indent selects. Keyboard order in a
  row is title, chevron, ⋯; Enter and Space toggle the chevron and keep focus
  on it; Shift+Tab into a row reaches its ⋯ and shows its chevron.
- The chevron takes no width until hover or focus-within, so a truncated
  parent title shortens on hover and its ellipsis moves left; see F2.

Gaps:

- `/ox-work`'s Tests proved and Checks were not passed on with the request.
  This review ran `make test`, `make e2e`, and the spot-checks below itself.

## Summary

| ID | Severity | Category    | Action | Status   | Title                                                       |
| -- | -------- | ----------- | ------ | -------- | ----------------------------------------------------------- |
| F1 | Medium   | Correctness | Fix    | Resolved | The expanded chevron takes the title button's styles        |
| F2 | Low      | Intent      | Fix    | Resolved | A truncated title shortens when its chevron shows           |
| F3 | Low      | Correctness | Fix    | Resolved | In the dark theme a hovered title shows through the chevron |
| F4 | Low      | Testing     | Fix    | Resolved | No test sees the chevron over a long title                  |
| F5 | Low      | Simplicity  | Fix    | Resolved | The chevron's cover rebuilds a translucent hover color      |
| F6 | Low      | Correctness | Fix    | Resolved | A hovered row looks the same as the selected one            |

## F1. The expanded chevron takes the title button's styles

Medium · Correctness · Fix · `cmd/ox/web/src/Sidebar.tsx:186`, `cmd/ox/web/src/style.css:160`

The chevron carries the class `open` while its children show, for the
rotation. The row's title button is `button.open`, so every `nav li
button.open` rule applies to the expanded chevron too:

- `flex: 0 1 auto; min-width: 0`: beside a title too long to fit, the expanded
  chevron shrinks to about 10px while its 16px icon overflows it, so its hit
  target is half the collapsed one's.
- `justify-content: flex-start`: the icon sits at the button's left instead of
  its center.
- `nav li button.open:hover { background: transparent }` and `cursor: default`:
  the expanded chevron has no hover background or pointer cursor, while the
  collapsed one has both.
- `nav li.archived button.open` and `nav li:has(.status.parked) button.open`
  dim the expanded chevron, and not the collapsed one.

Expanded is the default state, so every parent row shows this.

### Evidence

In the `make dev` stack, a parent titled "Parent session with a very long
title that cannot fit in the sidebar", hovered and expanded, computes
`flex: 0 1 auto`, `min-width: 0px`, width 9.95px for `button.icon.chevron-right.open`,
beside the ⋯ button's `flex: 0 0 auto`, width 24px. A short-titled parent's
chevron is 20px wide.

### Action

Drop the `open` class from the chevron and rotate it on its own state:
`nav li .chevron-right[aria-expanded="true"]::before { transform: rotate(90deg) }`.
`aria-expanded` is then the one source for both the accessible state and the
rotation. Keep a check in the "a session's chevron hides and shows every
session beneath it" spec: give Parent a title too long to fit the sidebar and
assert the hovered chevron's bounding box is 20px wide both expanded and
collapsed.

## F2. A truncated title shortens when its chevron shows

Low · Intent · Fix · `cmd/ox/web/src/style.css:167`

On hover or focus, a truncated parent title loses the chevron's width and its
ellipsis moves left ("…with a very lon…" becomes "…with a very lo…"). The
reference, `~/src/hypercube/internal/ui/web/sidebar/session-item.tsx`, keeps
the title still and overlays the chevron on its end with a fade, measuring
each title with a `ResizeObserver`. A short title does not move in either.

### Evidence

In the `make dev` stack, the long title's box goes from 207px unhovered to
195px hovered (about 22px once F1 gives the chevron its full width). The `li`'s
box does not change, so hover does not flicker.

### Action

First rejected, because the plan's Chevron decision accepted the cut on show.
The top-level agent overrode the rejection and directed the fix: the chevron
follows a title that fits and overlays the end of one that does not, as the
reference does, without moving the title.

## F3. In the dark theme a hovered title shows through the chevron

Low · Correctness · Fix · `cmd/ox/web/src/style.css:147`, `cmd/ox/web/src/style.css:172`

The chevron hides the end of a long title under a gradient into
`--row-background`. On a hovered row that is `--accent`, which the dark theme
defines as `oklch(0.269 0 0 / 50%)`. The gradient is half transparent, so the
title's last letters and ellipsis show through the chevron, and the chevron
sits on a square lighter than the row, since its accent is painted over the
row's accent. Light-theme hover, selected rows, and touch screens use opaque
backgrounds and look right.

### Evidence

In headless Chrome through `make dev`, dark color scheme, hovering a parent
with a long title: the chevron's computed background is
`linear-gradient(to right, rgba(0, 0, 0, 0), oklch(0.269 0 0 / 0.5) 4px)`, and a
4x screenshot shows the title's ellipsis beneath the chevron's icon, on a
lighter square.

### Action

Make the fade end in what the row actually shows: give the chevron a second
gradient layer into the opaque `--sidebar` beneath the `--row-background`
layer, so its two layers composite to the row's own `--accent` over
`--sidebar`. Keep a check in the long-title part of "a session's chevron hides
and shows every session beneath it": with the dark color scheme emulated and
the row hovered, a 1px screenshot inside the chevron's box, clear of its icon,
equals a 1px screenshot of the row's empty space.

## F4. No test sees the chevron over a long title

Low · Testing · Fix · `cmd/ox/web/e2e/session.spec.ts`

Removing `position: sticky` from the chevron leaves it in flow past the end of
a long title, clipped by `.head`, so a long-titled parent shows no chevron. The
long-title check still passes: the title's width does not change, the
chevron's box is still 20px, and Playwright's visibility check ignores
clipping by an ancestor's overflow.

### Evidence

With `position: sticky; right: 0` removed from `nav li button.chevron-right`,
"a session's chevron hides and shows every session beneath it" passes.

### Action

In the long-title part of that test, assert that the hovered chevron's box ends
at or before the left edge of the row's Session actions button. Without
`sticky` the chevron's box runs under the ⋯ button.

## F5. The chevron's cover rebuilds a translucent hover color

Low · Simplicity · Fix · `cmd/ox/web/src/style.css:147`, `cmd/ox/web/src/style.css:172`

F3's fix gives the chevron a transparent left border, an opaque `--sidebar`
background, an inset box-shadow of `--row-background`, and a masked `::after`
over the border for the fade: three rules, a border trick, and a comment, all
to rebuild the row's color because the dark theme's `--accent` is translucent.
The translucent hover color is the cause, and the row's hover color is the
only reason the cover needs layers.

shadcn's sidebar, whose tokens this stylesheet already uses (`--sidebar`), has
an opaque token for exactly this: its menu buttons hover in `--sidebar-accent`,
`oklch(0.97 0 0)` light and `oklch(0.269 0 0)` dark. With an opaque hover
color, the chevron needs only the one-line gradient into `--row-background`
that F3 started from.

### Evidence

In headless Chrome, a 20px box on a row of translucent dark `--accent` over
`--sidebar`:

- The current border, box-shadow, and background match the row exactly
  (31,31,31 against 31,31,31).
- A two-layer gradient, `--row-background` over `--sidebar`, comes out one
  level off (30 against 31) and fails the F3 pixel check.
- On a row of opaque `oklch(0.269 0 0)`, the one-line gradient into the same
  color matches exactly (38,38,38 against 38,38,38).

### Action

Add `--sidebar-accent: light-dark(oklch(0.97 0 0), oklch(0.269 0 0))` beside
`--sidebar`, and use it for the hovered row's `--row-background` and for `nav
header:hover`, so the two sidebar hovers keep matching. Replace the chevron's
border, box-shadow, padding-box background, and `::after` with
`background: linear-gradient(to right, transparent, var(--row-background) 0.25rem)`,
and shorten the comment to match. The F3 pixel check in "a session's chevron
hides and shows every session beneath it" keeps guarding the look.

In the dark theme this makes a hovered sidebar row a shade lighter, 38,38,38
instead of 31,31,31, as shadcn's sidebar shows it. The light theme does not
change.

## F6. A hovered row looks the same as the selected one

Low · Correctness · Fix · `cmd/ox/web/src/style.css:148`

A hovered row's background is `--sidebar-accent` and the selected row's is
`--secondary`. In both themes they have the same value, `oklch(0.97 0 0)` and
`oklch(0.269 0 0)`, so while the pointer is over another row, two rows show
the same background. Only the selected row's text color sets it apart: in dark,
`--secondary-foreground` against the hovered row's `--text` (#d4d4d4), a
slight brightening. Light had this before the change; F5 brought it to dark,
where hover used to be the dimmer half-transparent accent.

shadcn's sidebar has the same backgrounds, since its menu button takes
`bg-sidebar-accent` on hover and when active. It tells the active item apart by
weight: an active menu button is also `font-medium`.

### Evidence

In headless Chrome through `make dev`, with "Second session" selected and the
pointer over "First session", both rows show the same background in both
themes. Only Second session's text is brighter (dark) or darker (light).

### Action

Copy shadcn's active state: the selected row takes `--sidebar-accent`, the same
value as `--secondary`, so the two states name one token, and `font-weight: 500`
on its title. Keep a check in "a hash change selects the session it names" or
another selection spec: the selected session's title has CSS `font-weight`
`500`, and an unselected one `400`.

## Checks run

- `make test` (lint, Go tests, node tests): passed.
- `make e2e`: 111 passed, 4 skipped (the live specs).
- `dprint check` on `docs/`: passed.
- Requirements diff: Session order, Collapsing child sessions (placed after
  Session status), and Touch screens match the plan's Requirement changes word
  for word. `docs/todo.md` drops the two Sidebar entries.
- Spot-checks, each reverted, running the new and changed specs:
  - Removing the `li` click: "a session's chevron hides and shows every
    session beneath it" fails.
  - Removing the touch rule's chevron `display`: the touch-screen spec fails.
  - Removing the hover rule's chevron `display`: the chevron spec and the
    open-session spec fail.
  - Descending into collapsed rows: all three new specs fail.
- Headless Chrome through `make dev` on spare ports: hover layout, click
  targets, keyboard order and activation, as in Scope and coverage.
- `make live`: not required; the change does not alter how Ox drives Claude
  Code.

## Verification 1

Checked the uncommitted tree over `dac4fb8` after the fixes for F1 and F2,
with the new `.head` wrapper, `--row-background`, and the row click through
`closest("button")`. In headless Chrome through a `make dev` stack on ports
18490 and 18491:

- Focus rings: the title button's and the chevron's rings show whole inside
  `.head`'s 3px padding. The title button's ring encloses the chevron that
  overlays its end.
- Scrolling: `.head` stays at `scrollLeft` 0 when its title button, chevron, and
  ⋯ take focus by Tab and Shift+Tab, after chevron clicks, and after a drag
  across the title past the row's end.
- Row click: a click on the chevron or ⋯ keeps the selection and URL; a click
  on the row's corner or on `.head`'s top padding selects.
- `--row-background`: plain, hovered, and selected rows in both themes, and
  touch in both themes, look right except F3.

| ID | Status   | Evidence                                                                                                                                              |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The chevron rotates on `aria-expanded`; with the `open` class put back, the chevron spec fails on the archived chevron's opacity (0.6, expected 1).    |
| F2 | Resolved | Fixed at the top-level agent's direction; with the title button shrinking again, the chevron spec fails on the title's width (209, expected 229).        |
| F3 | Open     | New.                                                                                                                                                  |
| F4 | Open     | New.                                                                                                                                                  |

Checks run:

- `make test`: passed.
- `make e2e`: 111 passed, 4 skipped (the live specs).
- `dprint check` on `docs/`: passed.
- Spot-checks, each reverted: the `open` class back on the chevron and the
  title button shrinking again each fail the chevron spec; the row click
  selecting for clicks on buttons fails "collapsing the open session's family
  leaves it open, and a new child stays hidden"; removing `position: sticky`
  passes every spec (F4).
- `/ox-work fix`'s Tests proved and Checks were not passed on with the request.

## Verification 2

Checked the uncommitted tree over `dac4fb8` after the fixes for F3 and F4.

| ID | Status   | Evidence                                                                                                                                                |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F3 | Resolved | The cover paints `--row-background` over `--sidebar`; with the earlier one-layer gradient back, the chevron spec's dark-theme pixel check fails.          |
| F4 | Resolved | The chevron spec checks that the hovered chevron ends before Session actions; with `position: sticky` removed it fails (265, expected at most 251).      |
| F5 | Open     | New.                                                                                                                                                    |

Checks run:

- `make test`: passed.
- `make e2e`: 111 passed, 4 skipped (the live specs).
- Spot-checks, each reverted: the one-layer gradient and the missing `sticky`
  each fail the chevron spec; a two-layer gradient fails its pixel check by one
  level.
- `/ox-work fix`'s Tests proved and Checks were not passed on with the request.

## Verification 3

Checked the uncommitted tree over `dac4fb8` after the fix for F5: a hovered
row and the workspace header hover in `--sidebar-accent`, and the chevron's
cover is the one-line gradient into `--row-background`.

| ID | Status   | Evidence                                                                                                                                |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| F5 | Resolved | The cover is one gradient over an opaque hover color; the F3 dark-theme pixel check passes, and fails with the translucent `--accent` back. |
| F6 | Open     | New.                                                                                                                                    |

The top-level agent accepted the lighter dark hover shade F5 brings.

Checks run:

- `make test`: passed.
- `make e2e`: 111 passed, 4 skipped (the live specs).
- Headless Chrome through `make dev`: a selected and a hovered row, light and
  dark (F6).
- `/ox-work fix`'s Tests proved and Checks were not passed on with the request.

## Verification 4

Checked the uncommitted tree over `dac4fb8` after the fix for F6: the selected
row takes `--sidebar-accent`, and its title button `font-weight: 500`.

| ID | Status   | Evidence                                                                                                                     |
| -- | -------- | ---------------------------------------------------------------------------------------------------------------------------- |
| F6 | Resolved | "a hash change selects the session it names" checks weights 500 and 400; with the selected weight removed it fails (400, expected 500). |

A truncated title's first ellipsis dot can show just left of the chevron. At
the top-level agent's direction this is left to the todo **Clip session titles
at the panel edge**, which replaces the ellipsis with a fade, and is not a
finding here.

Checks run:

- `make test`: passed.
- `make e2e`: 111 passed, 4 skipped (the live specs).
- Spot-check, reverted: removing `nav li.selected button.open { font-weight: 500; }`
  fails the hash-change spec.
- `/ox-work fix`'s Tests proved and Checks were not passed on with the request.

## Verdict

Ready to commit. F1 through F6 are resolved.
