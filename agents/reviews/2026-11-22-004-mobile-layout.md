# Mobile layout

## Scope and coverage

Uncommitted changes on `mobile-layout` over `d79ffe6`, checked against
`agents/plans/2026-11-22-003-mobile-layout.md`: `index.html`, `App.tsx`,
`Panels.tsx`, `Sidebar.tsx`, `FileTree.tsx`, `FileView.tsx`, `style.css`, the
new phone test in `workspace.spec.ts`, `docs/architecture.md`, and
`docs/functional-requirements.md`. The UI was judged from headless Chrome
screenshots at 390×844 with touch and a 47 px top and 34 px bottom safe-area
inset, against the ChatGPT and Claude mobile web apps.

The requirements diff matches the plan's Requirement changes word for word, and
the architecture row matches task 9.

Gaps:

- No iPhone Safari check. How `100dvh` and `env(safe-area-inset-bottom)` meet
  Safari's floating toolbar is left to the user's check on the phone.

## Summary

| ID | Severity | Category    | Action   | Status   | Title                                                    |
| -- | -------- | ----------- | -------- | -------- | -------------------------------------------------------- |
| F1 | Low      | Testing     | Fix      | Resolved | Safe-area insets beyond the composer's are untested      |
| F2 | Low      | Correctness | Fix      | Resolved | A long directory wraps out of the session header's path  |
| F3 | Low      | Simplicity  | Rejected | —        | FileView's share is passed only while wide               |
| F4 | Low      | Conventions | Rejected | —        | Lint suppression on the scrim                            |
| F5 | Low      | Conventions | Rejected | —        | Composer margin and transcript inset live on `.layout`   |
| F6 | Low      | Correctness | Rejected | —        | Narrow sessions toggle's margin                          |
| F7 | Low      | Intent      | Rejected | —        | Drawers have no close control and the scrim cuts out     |
| F8 | Low      | Intent      | Rejected | —        | An open file hides the panel toggles on a narrow screen  |

## F1. Safe-area insets beyond the composer's are untested

Low · Testing · Fix · Resolved · `cmd/ox/web/src/style.css:50`

Touch screens requires the page to keep its controls clear of the notch,
rounded corners, and browser toolbars. The phone test sets a bottom inset only
around the composer check. Nothing protects the `.layout` top and side padding,
the drawers' top and outer-side padding, or the bottom padding of `nav`, the
file tree's `.tree`, and the viewer's tab panel.

### Evidence

Removing the bottom inset from `nav`, `aside.files .tree`, and
`.viewer [role="tabpanel"]` leaves "on a phone › the page shows one pane at a
time" passing. Replacing `.layout`'s padding with `padding: 0` also leaves it
passing.

### Action

In the same e2e test, set the override once with a top and bottom inset (for
example `{ top: 47, bottom: 34 }`) before the composer check and clear it after
the file viewer steps, so the drawers and viewer run under it. Assert:

- the session header's `h1` top is at least the top inset;
- with the sessions drawer open, Add workspace's top is at least the top inset
  and `nav`'s computed `padding-bottom` is `42px`;
- with the files drawer open, its header's top is at least the top inset and
  `.tree`'s `padding-bottom` is `42px`;
- with `a.txt` open, the tab panel's `padding-bottom` is `42px`.

Each assertion fails when its inset is removed.

## F2. A long directory wraps out of the session header's path

Low · Correctness · Fix · Resolved · `cmd/ox/web/src/style.css:203`

`.context .path` is a fixed 2.25rem row whose text wraps. At 390 px a directory
longer than about 45 characters wraps to two lines, 39 px tall, past the row's
bottom border, and pushes against the transcript. Child sessions show their
worktree directory there, which is routinely that long. The wide layout's
session titles and file names truncate to one line with an ellipsis instead.

### Evidence

Screenshot of the new session view and the session view at 390×844 with the
e2e workspace path: the path's second line touches the row's border.

### Action

Wrap the path text in `SessionView` and `NewSessionView` in a span that keeps
one line with an ellipsis (`min-width: 0; overflow: hidden; white-space: nowrap;
text-overflow: ellipsis`), as `nav li .title` does. Keep it in the phone e2e
test, whose workspace path is already long: assert the path row's text box is
one line tall (its height at most the row's).

## F3. FileView's share is passed only while wide

Low · Simplicity · Rejected · `cmd/ox/web/src/App.tsx:206`

Rejected: the gating is required. On a narrow screen `main` is hidden and the
viewer is the row's only grown item, so a `flex-grow` below 1 leaves it that
fraction of the width. Removing the gating makes the phone test fail at
`expect((await box(viewer)).width).toBe(390)`. It follows the same rule as
`width()`, which drops the side panels' widths while narrow.

## F4. Lint suppression on the scrim

Low · Conventions · Rejected · `cmd/ox/web/src/App.tsx:146`

Rejected: the scrim is a pointer-only target, as the overlays behind the
ChatGPT and Claude drawers are. A button would add a tab stop with no visible
control, and the header's toggles already close the drawer from the keyboard,
which the test covers. The suppression states its reason, as the existing one
in `SettingsFields.tsx` does.

## F5. Composer margin and transcript inset live on `.layout`

Low · Conventions · Rejected · `cmd/ox/web/src/style.css:49`

Rejected: `.narrow` is set on the layout's root, so the narrow override has to
sit on that element or below it; `:root` cannot see it. The variables' only
readers, `.composer`, `.transcript`, and `.log ~ …`, are inside `main`. The test
checks both values at 390 px (`composer.x` 8, transcript `padding-left` 29px).

## F6. Narrow sessions toggle's margin

Low · Correctness · Rejected · `cmd/ox/web/src/style.css:412`

Rejected: the title row has no gap, so the reordered toggle needs its own
spacing. The 0.5rem between the menu button and the title matches the spacing
in the ChatGPT and Claude mobile headers, and the screenshot shows it aligned
with the transcript's inset.

## F7. Drawers have no close control and the scrim cuts out

Low · Intent · Rejected · `cmd/ox/web/src/App.tsx:144`

An open drawer covers its own toggle, and the drawer offers no close button of
its own. Closing, the sessions drawer slides out while the scrim disappears at
once, and the files drawer disappears without sliding.

Rejected: the plan settles how drawers close (a tap outside, picking a session
or draft, opening a file) and that the file tree unmounts so its polling stops.
Both reference apps close their drawers the same ways. The missing fade-out is
cosmetic and would need the scrim kept mounted behind a transition.

## F8. An open file hides the panel toggles on a narrow screen

Low · Intent · Rejected · `cmd/ox/web/src/style.css:414`

With a file open on a narrow screen, `main` and its header are hidden, so
neither drawer can be opened until the tab is closed.

Rejected: the plan's One pane decision and the Narrow screens requirement have
the file replace the transcript until its tab is closed. ChatGPT's canvas and
Claude's artifact view behave the same way on a phone: a full-screen pane that
its close button dismisses.

## Checks run

- `make test`: passed.
- `make e2e`: passed (44 passed, 3 skipped live specs).
- "on a phone › the page shows one pane at a time": passed.
- Removed conditions, one at a time, then restored:
  - FileView's narrow `share` gating: the test fails at the viewer's width.
  - `panels.dismiss()` in the sidebar's `onSelect`: the test fails at
    `expect(nav).toBeHidden()` after New session.
  - `setDrawer(undefined)` on crossing the breakpoint: the test fails at
    `expect(nav).toBeHidden()` on returning to narrow.
  - Bottom inset on `nav`, `.tree`, and the tab panel: the test passes (F1).
  - `.layout`'s safe-area padding: the test passes (F1).
- Headless Chrome screenshots at 390×844 through the e2e harness: the bare URL
  with the sessions drawer, the new-session view, a session, the sessions
  drawer, the files drawer, and an open file.
- The change does not alter how the page reaches `oxd`, so no `make dev` check
  is required; `make live` does not apply.

## Verification 1

Checked the uncommitted tree over `d79ffe6` after `/ox-work fix`. Since the
review: `SessionView.tsx` and `NewSessionView.tsx` wrap the path in
`span.directory`, `style.css` adds `.context .directory`, and the phone test
holds a 47/34/12/12 px safe-area override from the composer check through the
sessions drawer. `App.tsx` and `Panels.tsx` are unchanged.

| ID | Status   | Evidence                                                                                                                                                                                                |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The phone test asserts the header, drawers, and tab panel clear the insets. Removing `nav`'s bottom inset fails at its `padding-bottom`; replacing `.layout`'s padding with `0` fails at the `h1`'s top. |
| F2 | Resolved | The path keeps one line with an ellipsis. Removing `.context .directory` fails the test at its overflow check. `session.spec.ts`'s `toHaveText` on `.context .path` still matches through the span.    |

No new findings.

Checks run:

- `make test`: passed.
- "on a phone › the page shows one pane at a time": passed, and failed with
  each of the three removals above, restored after each.
- `make e2e`: the implementer reports 44 passed, 3 live specs skipped, on this
  tree.

## Verdict

Ready to commit. F1 and F2 are resolved; F3–F8 are rejected.
