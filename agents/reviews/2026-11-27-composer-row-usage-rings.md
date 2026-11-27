# Review: composer row usage rings (#171, #173)

## Scope and coverage

Base `git merge-base origin/main HEAD`; uncommitted working tree on `composer-row-usage-rings` (`Usage.tsx`, `style.css`, `session.spec.ts`), against `agents/plans/2026-11-27-composer-row-usage-rings.md`. Web client only, reviewed without children.

Checked in headless Chrome against a stack on spare ports (built binaries, fake harness, a model name 60+ characters long, the "usage" and "usage reset" inputs) at 320px, 390px, and 1280px.

Gaps:

- I did not run `make test`, `make e2e`, or the new spec; the implementer's final response was not passed to this session. I ran `style.test.mjs` and the Biome lint (no errors).

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                  |
| -- | -------- | ----------- | -------- | ------ | ------------------------------------------------------ |
| F1 | Medium   | Correctness | Fix      | Resolved | Elapsed marker is 90° off the arc                      |
| F2 | Low      | Testing     | Rejected | —      | Dropped ring-size and dash assertions                  |
| F3 | Low      | Testing     | Rejected | —      | No truncation assertion in the phone spec              |
| F4 | Low      | Conventions | Fix      | Resolved | `Usage` doc comment is not re-wrapped after the edit   |

## F1. Elapsed marker is 90° off the arc

Medium · Correctness · Fix · `cmd/ox/web/src/Usage.tsx:110`

The track and fill are rotated 135°, which puts the arc's start at the bottom left, ending at the bottom right. The marker is a line drawn from the top of the ring (12 o'clock) and rotated by `135 + 2.7 * elapsed`. A line at 12 o'clock needs `225 + 2.7 * elapsed` to sit on the arc. The plan's formula has the same error.

At 0% elapsed the marker sits at the bottom right, the arc's end. At 50% it sits at 9 o'clock, not at the top of the arc. At 100% it sits at the upper right, not the bottom right. It therefore shows the wrong time passed and wanders off the arc's range.

### Evidence

I rendered six rings enlarged in the running page, with used and elapsed set to 0/0, 30/0, 30/50, 80/100, 95/25, 100/75. The marker sat at bottom right for elapsed 0, left for 50, upper right for 100, bottom left for 25, and top-left for 75. The expected positions are bottom left, top, bottom right, left-lower, and right-upper. The other geometry is correct in the same render: the gap is at the bottom, the fill grows clockwise from bottom left, 100% fills the whole arc, and warn (orange) and high (red) apply at 80% and 95%.

### Action

Change the marker's rotation to `225 + 2.7 * elapsed`, and fix the comment that explains the arc. The 135° comment is correct for the circles only. Both rings are visible in the popover and the button, so check one in Chrome after the fix. No kept test can observe the marker's angle under `cmd/ox/web/AGENTS.md` (no CSS or geometry assertions), so none is required.

## F2. Dropped ring-size and dash assertions

Low · Testing · Rejected

The plan asked for a bounding-box width of the meter and `stroke-dasharray` starting `60 ` on the fill. `cmd/ox/web/AGENTS.md` bans `boundingBox` arithmetic on sizes and locating by CSS class or attribute, and the dash lives on an `aria-hidden` SVG with no role or name. The existing spec already covers the percent through `aria-valuenow`. Dropping both was right. Adding a role or name just for the test would change the markup for no user-visible reason.

## F3. No truncation assertion in the phone spec

Low · Testing · Rejected

The plan asked for `scrollWidth > clientWidth` on the model name. That is a one-shot DOM read and a layout measurement, which the web AGENTS bans. The spec keeps the behavior that matters: it holds the long name, checks that the three controls share one `y` (one line), which is geometry that is the behavior, and uses `toBeInViewport({ ratio: 1 })` on the usage button, which fails if the row overflows. Without the change the row wraps and the `y` check fails. In Chrome, the name truncated at 320px and 390px and the row stayed on one line at all three widths with the usage button inside the viewport.

## F4. `Usage` doc comment is not re-wrapped after the edit

Low · Conventions · Fix · `cmd/ox/web/src/Usage.tsx:20`

The reworded comment leaves one line longer than its neighbours ("window has passed. The button is named ..."). Re-wrap it to the file's width.

### Action

Re-wrap the comment. No test.

## Checks run

- Headless Chrome through a stack built from this tree (`make build`; `bin/oxd` and `bin/ox` on a temporary socket, database, and port). At 320, 390, and 1280px with a usage reading: the row stays on one line, the long model name is truncated at 320 and 390 (not at 1280), nothing overflows the page, and the popover grid aligns (label, ring, percent, time left). Reset dimming is applied to label, ring, percent, and time left (opacity 0.5) in the button and popover. Ring gap, fill direction, and warn/high colors are correct; the marker is not (F1).
- `node --test cmd/ox/web/style.test.mjs` and `npm run lint` in `cmd/ox/web`: no failures.
- Not run: `make test`, `make e2e`, and the new spec, because I did not receive the implementer's results and the review did not need them to find F1. The parent should confirm they ran on the final tree.

## Verdict

Ready to commit.

## Verification 1

Revision: the uncommitted working tree after the fixes, rebuilt with `make build`. Checks: a stack on a temporary socket, database, and port, with the fake harness's "usage" reading, in headless Chrome at 1280px.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `Usage.tsx` now rotates the marker by `225 + 2.7 * elapsed`. The real 5h window (60% elapsed) rendered `rotate(386.1 8 8)`, which measures 26° clockwise from the top, as expected for 60% of the arc. Driving the same element through elapsed 0, 50, and 100 with the fixed formula measured 225° (bottom left, the arc's start), 0° (top), and 135° (bottom right, the arc's end). Only the real 60% reading came from the app's own computation; 0, 50, and 100 set the rotation by hand because the fake harness reports fixed windows. |
| F4 | Resolved | The `Usage` doc comment is re-wrapped. |

New findings: none. The new comment above the SVG explains the 135° and 225° rotations and is accurate. The implementer reports `make test` and `make e2e` passing on the final tree; I did not rerun them.
