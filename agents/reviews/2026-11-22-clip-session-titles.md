# Review: clip session titles and workspace names at the row's edge

## Scope and coverage

Uncommitted changes on `clip-session-titles` against base `2e1a6fe` (`git merge-base origin/main HEAD`, also `HEAD`): `cmd/ox/web/src/Sidebar.tsx`, `cmd/ox/web/src/style.css`, `cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/e2e/workspace.spec.ts`. Checked against `agents/plans/2026-11-22-clip-session-titles.md`, which resolves #1. The plan changes no requirements, and `docs/functional-requirements.md` is unchanged. The requirements the change touches are Session labels and Collapsing child sessions. None of the open `bug` issues (#5, #6, #8, #9, #12) is touched by this change.

The implementer's final response was not passed on, so the Tests proved and Checks lists were not available. The review reran the checks and the removals itself (see Checks run).

Gaps:

- None material. The change is page-only styling and sends no request to `oxd`, so `make live` and a `make dev` check are not required.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                           |
| -- | -------- | ----------- | -------- | ------ | ----------------------------------------------- |
| F1 | Low      | Intent      | Rejected | —      | The CSS and the test depart from the plan       |
| F2 | Low      | Conventions | Rejected | —      | New Biome noDescendingSpecificity warning       |
| F3 | Low      | Testing     | Fix      | Resolved | No test fails without the fade before `.actions` |

## F1. The CSS and the test depart from the plan

Low · Intent · Rejected · `cmd/ox/web/src/style.css:173`

The implementer reported five departures from the plan's Decisions and Test plan:

- The chevron's sticky offset is `right: var(--actions)`, with no 3px for `.head`'s overhang.
- `.actions` inherits only its right-hand corner radii, not `border-radius: inherit`.
- The background of `.actions` is a 0.25rem gradient over its left padding, with a solid `content-box` fill, instead of one gradient with a 0.25rem stop.
- `.actions` has only left padding and a fixed width, instead of `padding: 0 0.25rem`.
- The long child's title is `Long child ${words(6)}`, instead of `words(40)`.

### Evidence

- Sticky offsets are measured from the edge of the scroll container's content box. `.head`'s 3px padding cancels its 3px negative margin, so its content edge is the row's edge, and `right: var(--actions)` puts the chevron's right edge at the left edge of `.actions`. Changing it to `right: 0` fails the test's `over.x + over.width <= actions.x` check.
- `.actions` sits at the row's right end, so only its right corners meet the row's rounded corners. With all four corners inherited, the solid background beside a chevron would show rounded left corners in the middle of the row.
- When the plan's single gradient (`padding-right: 0.25rem; background: linear-gradient(to right, transparent, var(--row-background) 0.25rem)`) is swapped in, both new strip comparisons fail (`session.spec.ts:1153`, `workspace.spec.ts:480`). The layered background passes, so the implementer's report of gradient dithering holds.
- With `box-sizing: border-box`, a width of `var(--actions)` and left padding only, the icons sit where the plan places them: the session ⋯ 0.25rem from the row's edge, and the header's + 0.125rem from its edge. Both match the positions before the change. Right padding would add nothing.
- `internal/server/mcp.go:311` caps a child title at 48 runes. `Long child word0 … word5` has 46 characters. The screenshots show it clipped at the row's edge with its text under the ⋯ button, and removing the fade fails `session.spec.ts:1153`, so the shorter title still exercises the case.

### Action

Rejected: each departure delivers the plan's Goal and Decisions in the form that renders correctly in Chrome, and was confirmed by measurement and by removal.

## F2. New Biome noDescendingSpecificity warning

Low · Conventions · Rejected · `cmd/ox/web/src/style.css:318`

`biome check` now warns that `.approval .actions` (0,2,0) follows `nav li:has(button.chevron-right) .actions` (0,2,3). Base had one warning of this rule, and the change adds this second one.

### Evidence

`npx biome check .` reports 2 warnings. The same check on the base `style.css` reports 1. The two selectors cannot match the same element, because the approval card is not inside `nav`. Warnings do not fail `npm run lint`, and `make test` passes.

### Action

Rejected: the warning is a false positive for disjoint elements. Removing it would mean renaming one of the two `.actions` classes, which costs more than keeping a warning of a rule the stylesheet already triggers.

## F3. No test fails without the fade before `.actions`

Low · Testing · Fix · `cmd/ox/web/src/style.css:175`

The fix after 59f8cd9 moves the fade into `nav :is(header, li) .actions::before`, a 0.25rem gradient just left of the solid `.actions`. The fade is the behavior #1 asks for, but no test fails without it. The strips in `session.spec.ts:1153` and `workspace.spec.ts:480` start at the ⋯ button's left edge, which now lies on the solid `.actions`. The chevron strip at `session.spec.ts:1164` lies on `.actions`'s solid padding beneath the chevron. The gap was already there in 59f8cd9, where removing only the gradient layer, and keeping the `content-box` fill, would also have passed. The first round did not catch it, because it removed the whole background.

### Evidence

Deleting the `.actions::before` rule leaves both `a session's chevron hides and shows every session beneath it` and `a long workspace name runs to its header's edge beneath its buttons' fade` passing.

### Action

Keep an e2e check that the fade changes the title's pixels just before the actions, in the long-child part of `a session's chevron hides and shows every session beneath it`. Make the row's color the same whether or not the actions show, for example by selecting the long child. With the row neither hovered nor focused, take the strip over the title in the 0.25rem just left of the ⋯ button's box. Hover the row, take the same strip again, and expect the two to differ. Keep the existing check that the strip at the ⋯ button's edge matches the empty row. The new check should fail with `.actions::before` removed.

## Checks run

- `make test` (lint, Go tests with the race detector, Node tests): passed.
- `make e2e`: 116 passed, 4 skipped.
- The two changed tests pass on the change and fail on the base `style.css` and `Sidebar.tsx` (`session.spec.ts:1136`, `workspace.spec.ts:470`).
- Removing the `.actions` background fails `session.spec.ts:1153` and `workspace.spec.ts:480`.
- Removing `nav li:has(button.chevron-right) .actions` fails `session.spec.ts:1163`.
- Setting the chevron's sticky `right` to `0` fails `session.spec.ts:1162`.
- Swapping in the plan's single gradient fails `session.spec.ts:1153` and `workspace.spec.ts:480`.
- Headless Chrome screenshots of the sidebar at rest, hovering the long parent, the long child, a short row, and the workspace header, in light and dark themes, and with keyboard focus on a long title: titles and the workspace name run to the edge without an ellipsis, the fade sits behind ⋯ and +, no title shows between the chevron and ⋯, and the focus ring surrounds the whole row. The temporary spec was removed.
- `make live` was not run: the change does not alter how Ox drives Claude Code.

## Verification 1

Checked the uncommitted fix on top of 59f8cd9 (`style.css`, `session.spec.ts`), which addresses a defect found live at deviceScaleFactor 2: on a hovered long parent, a sliver of title showed at the seam between the chevron and `.actions`, and a faint rectangle showed around the chevron's box. The chevron no longer has a background, and its z-index of 1 puts it above `.actions`. `.actions` is solid, takes its fade from `::before`, and on a parent's row extends 1.25rem left beneath the chevron. The fix supersedes F1's evidence about the layered background and the chevron's offset. F1 stays rejected. The parent's throwaway `e2e/zz-pr-shots.spec.ts` was ignored, as asked. It fails `biome check` on formatting, so it must not be committed.

| ID | Status   | Evidence                                                                                          |
| -- | -------- | ------------------------------------------------------------------------------------------------- |
| F1 | —        | Rejected                                                                                          |
| F2 | —        | Rejected. `biome check` still reports this warning and the one already on base, and nothing new. |
| F3 | Open     | New finding: removing `.actions::before` fails no test                                            |

The live defect is fixed:

- The revised strip at `session.spec.ts:1164`, running from 2px inside the chevron's right edge across the seam, fails on 59f8cd9's `style.css` and passes with the fix.
- Removing `nav li:has(button.chevron-right) .actions { padding-left: 1.25rem; }` fails the same check.
- Removing the chevron's `z-index: 1` fails the test's click on the long parent's chevron (`locator.click` times out because `.actions` intercepts it).
- Headless Chrome screenshots at deviceScaleFactor 2 of the hovered long parent, in light and dark themes, show no title at the seam and no box around the chevron. The hovered long child shows the fade before ⋯.
- The ⋯ and + icons keep their places: `padding-right: 0.25rem` on a row and `0.125rem` on a header, as before.

Checks: `biome check` on `src` and the two changed specs (2 warnings: the one already on base and F2), `tsc -p e2e`, and `npm run build` passed. `make e2e`: 117 passed, 4 skipped. No Go code changed since 59f8cd9, where `make test` passed. `make live` was not run, because the change does not alter how Ox drives Claude Code.

## Verification 2

Checked the uncommitted change on top of 59f8cd9. Besides Verification 1's fix, it now holds the F3 fix and two changes the user requested through the parent session. The workspace header is `height: var(--row)`, as tall as a session row. A selected row uses a new `--sidebar-selected` token, so it differs from a hovered row, and the selected color takes precedence over the hover color. It also holds a rule the user requested in `AGENTS.md`, Workflow step 7: a pull request that changes the UI ends with before-and-after screenshots. No functional requirement names these colors or heights, and `docs/functional-requirements.md` is unchanged. The throwaway `e2e/zz-pr-shots.spec.ts` is gone.

| ID | Status   | Evidence                                                                                                         |
| -- | -------- | ---------------------------------------------------------------------------------------------------------------- |
| F1 | —        | Rejected                                                                                                         |
| F2 | —        | Rejected                                                                                                         |
| F3 | Resolved | `session.spec.ts:1161` compares the strip before ⋯ on a selected row, unhovered and hovered. Removing `.actions::before` fails it. |

New tests, each failing without its change:

- Making `nav li.selected` use `--sidebar-accent` again fails the hovered-versus-selected swatch at `session.spec.ts:1167`.
- Setting `nav header` back to `height: 1.75rem` fails `a workspace header is as tall as a session row, with its buttons centered` at `workspace.spec.ts:466`.

No new findings. The `AGENTS.md` rule is an instruction to agents, which is the file's own subject, and contradicts no other document.

Checks: `make test` (lint with Biome's two warnings: the one already on base and F2; Go tests; Node tests) passed. `make e2e`: 117 passed, 4 skipped. `make live` was not run, because the change does not alter how Ox drives Claude Code.

## Verdict

Ready to commit. F3 is resolved, and F1 and F2 are rejected.
