# Fit the composer's settings row on one line, with usage drawn as rings

## Goal

Resolves #171 and #173. On a phone the row under the composer (the model picker, the permission mode, and the subscription usage button) wraps onto a second line, and the usage bars take most of the width it lacks. Each usage window becomes an open ring, and the row stays on one line at phone widths with a long model name truncated.

## Related code

- `cmd/ox/web/src/Usage.tsx` — `Usage` and `WindowRow`. `WindowRow` fills a four-column grid: length label, bar (`role="meter"`), percent, time left. The bar holds a `.fill` and an `.elapsed` marker.
- `cmd/ox/web/src/style.css` — `.composer .settings` (`flex-wrap: wrap`), `button.picker`, `.picker .model` (already ellipsizes), `button.usage` and `.usage-windows` rules (`/* Usage.tsx */`), the tokens `--usage-bar-width`, `--usage-bar-width-narrow`, `--usage-bar-height`, `--usage-track`.
- `cmd/ox/web/src/SettingsFields.tsx` — `ModelPicker`'s trigger button; `Select`.
- `cmd/ox/web/src/SessionView.tsx` — `.settings` holds `SettingsForm` and `Usage`. `NewSessionView.tsx` has its own `.settings` row without usage; it shares the `.composer .settings` rule only if it sits in `.composer`.
- `cmd/ox/web/style.test.mjs` — requires design values to come from `:root` tokens and one rule per selector.
- `cmd/ox/web/e2e/session.spec.ts` — the usage specs (button text, `meter` role, `aria-valuenow`, popover lists windows).

## Decisions

- **Requirements:** "Subscription usage" holds unchanged: the button still shows the shortest window's use and time to reset (the time stays hidden in the narrow layout, as now), clicking lists every window, the reading is dimmed once reset, nothing shows before a reading. No requirement text changes; a ring is a different drawing of the same information.
- **Construction:** `WindowRow` derives everything from the window and the clock, as now; no new state. The ring is one SVG per window: a 270° track arc and a fill arc, both a `circle` with `pathLength="100"` and `stroke-dasharray` (track `75 25`, fill `${0.75 * min(100, used)} 100`), rotated 135° so the gap sits at the bottom and the fill grows clockwise from bottom-left. The elapsed marker is a short radial `line` rotated by `135 + 2.7 * elapsed` degrees. `elapsed` is already clamped to 0–100.
- The `role="meter"` span stays as the ring's wrapper (same aria attributes, `title`, and reset dimming); the SVG is `aria-hidden`. The length label, percent, and time-left cells stay text, so the button's accessible name and the e2e text assertions are unchanged.
- Colors keep the thresholds: `warn` at 75%, `high` at 90%, via the fill's stroke token.
- Size: the ring is `var(--icon-size)` square. Replace `--usage-bar-width`, `--usage-bar-width-narrow`, and `--usage-bar-height` with `--usage-ring-stroke` (drop the bar tokens and their grid column widths; the column is `var(--icon-size)`).
- **#171 layout:** `.composer .settings` becomes `flex-wrap: nowrap`. The usage button and the permission-mode `Select` do not shrink (`flex: none`); the model picker button takes the slack (`flex: 0 1 auto; min-width: 0`) and its model name ellipsizes, with the effort name and chevron kept. On the narrow layout the usage button is length label, ring, percent (time left already hidden), so the freed bar width leaves room for the pickers at typical phone widths. The `margin-left: auto` on usage stays.
- Ownership, settings, settling, Claude Code, trust boundaries: untouched; web client styling and markup only.

## Check your assumptions

None.

## Test plan

- e2e (`session.spec.ts`, existing usage specs): keep passing unchanged, which proves the meter role, `aria-valuenow`, text, and popover list survive the redraw.
- e2e: add to the existing "subscription usage shows under the composer" spec an assertion that the meter is icon-sized (its bounding box width equals the icon size) and that the fill arc's dash length reflects 80% (`stroke-dasharray` begins `60 `). It fails on the bars (no SVG).
- e2e: new spec in `session.spec.ts`, at a phone viewport (390px wide), using a session whose model name is long enough to overflow (the fake harness's catalog; if none is long, intercept the session's model name via the existing `/rpc` interception) after a usage reading: assert the `.settings` children share the same `top` (one line) and that the model name is truncated (`scrollWidth > clientWidth`). It fails on the wrapping row. No existing spec checks layout at a phone width.
- `style.test.mjs` runs as is and covers token use and one rule per selector.
- Check in headless Chrome through `make dev` at 320, 390, and a wide width, with usage shown: row on one line, ring gap at the bottom, fill clockwise, marker moves with elapsed time, popover rings align in the grid, `warn`/`high` colors, reset dimming. Screenshots for the PR come from this.

## Implementation plan

1. `cmd/ox/web/src/Usage.tsx`: replace the bar span in `WindowRow` with the SVG ring inside the `role="meter"` span, per Construction. Update the `Usage` and `WindowRow` doc comments from "bar" to "ring".
2. `cmd/ox/web/src/style.css`: replace the bar tokens with the ring token(s); rewrite the `.bar`, `.fill`, `.elapsed` rules as ring rules (track stroke `--usage-track`, fill stroke by level, marker stroke `--foreground`, the rotate transforms); set grid columns to `auto var(--icon-size) auto auto` for the button and popover and remove the `.narrow` column override; make `.composer .settings` nowrap and set the flex rules for picker, select, and usage.
3. Run `make test` (Node suites), `make e2e` for the touched specs, and the headless Chrome check.
4. Add the e2e assertions and the phone-width spec in `cmd/ox/web/e2e/session.spec.ts`.

## Requirement changes

None.
