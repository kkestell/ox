# Review: size the page to the visible area on phones

## Scope and coverage

Uncommitted changes on `mobile-viewport-height` against base `b5419ba` (HEAD,
and `git merge-base origin/main HEAD`), checked against
`agents/plans/2026-11-22-mobile-viewport-height.md`, which fixes #9 and #15.
Working tree: modified `cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/e2e/ui.ts`,
`cmd/ox/web/e2e/workspace.spec.ts`, `cmd/ox/web/src/index.tsx`,
`cmd/ox/web/src/style.css`, `docs/architecture.md`; new
`cmd/ox/web/src/viewport.ts` and the plan.

The change delivers the plan's Goal and keeps its Decisions: one source for the
page's size and top (`viewport.ts`), one `--bottom-inset` read by all four
bottom edges, drawers and scrim positioned against the fixed layout, the
pending card capped at half the visible height, and pinch zoom ignored. The
plan makes no requirement changes and `docs/functional-requirements.md` is
untouched. **Touch screens** and **Narrow screens** hold with the keyboard
open and closed, as emulated. No request to `oxd` is involved.

Gaps:

- Headless Chromium has no on-screen keyboard, so the specs and this review
  confirm only that the page follows what the visual viewport reports. The
  plan's iPhone-only items stand: Safari's `visualViewport.height` and
  `offsetTop` with the keyboard, `innerHeight` staying put, the
  keyboard-closed band in #9, and no jump between Safari's pan and `resize`.
  #9 and #15 should be closed only after a check on an iPhone.

## Summary

| ID | Severity | Category | Action   | Status | Title                                                    |
| -- | -------- | -------- | -------- | ------ | -------------------------------------------------------- |
| F1 | Low      | Testing  | Fix      | Resolved | No test covers the visual viewport's `scroll` event      |
| F2 | Low      | Intent   | Rejected | —      | The top inset should drop while the keyboard pans the page |

## F1. No test covers the visual viewport's `scroll` event

Low · Testing · Fix · Resolved · `cmd/ox/web/src/viewport.ts:19`

`followViewport` listens for both `resize` and `scroll`. Safari pans the page
with a `scroll` event alone when the keyboard is already open and focus moves,
or when the pan lands after the `resize`; the plan's Decision on the layout
relies on that ("a pan that arrives before the resize event settles once the
event does"). `openKeyboard` dispatches only `resize`, so removing the
`scroll` listener leaves every test passing, and the header would then stay
off-screen after such a pan, which is #15's defect.

### Evidence

Deleted the `addEventListener("scroll", follow)` line and ran the three new
tests (`npx playwright test -g "visible area|above the keyboard"`): 3 passed.
Restored the file.

### Action

Keep a test that a pan alone moves the layout. In `ui.ts`, add a helper (or an
option on `openKeyboard`) that changes the own `offsetTop` getter and
dispatches `scroll` on `visualViewport`. In the existing workspace test "the
header and composer stay at the visible area's edges as the keyboard opens and
closes", after the keyboard opens at `offsetTop` 120, pan to a different
`offsetTop` with only a `scroll` event and assert the header's top and the
composer's bottom move by the same amount. Confirm it fails with the `scroll`
listener removed.

## F2. The top inset should drop while the keyboard pans the page

Low · Intent · Rejected · `cmd/ox/web/src/style.css:56`

The implementer asked whether `.layout`'s `padding-top: env(safe-area-inset-top)`
should still apply while the keyboard is open and the page is panned. It
should. The top inset is the status bar and sensor housing at the top of the
screen, and they stay on screen when the keyboard opens. The visual viewport's
top edge, where the layout now sits, is the screen's top edge whether the page
is panned or not, so in a home-screen web app with a translucent status bar
the top 47–59 px of the visible area are still covered. Dropping the inset
would put the header's buttons under the status bar while typing. The bottom
inset differs because the keyboard covers the home indicator, and the change
already drops that one. In Safari the top inset is 0 in portrait, so there is
nothing to observe there. Rejected: keeping the inset is the intended
behavior, and the test's 167 px (120 + 47) header position is correct.

## Checks run

- From `/ox-work`'s report: `make test` passed; `make e2e` passed (120 passed,
  4 skipped); headless Chrome through `make dev` in this worktree at 390×844
  with insets and an emulated keyboard, and at 1280×800, passed. `make live`
  is not required: the change does not alter how Ox drives Claude Code.
- From the same report: all three new tests fail on the base revision with
  the values the plan predicted.
- Spot check: removing the `scroll` listener leaves the three new tests
  passing (F1).
- By trace: removing the `scale > 1` guard, the `max(0px, …)` clamp, the
  drawers' `position: absolute`, or the `.pending` cap each changes a value
  one of the new tests asserts.

## Verification 1

Checked the working tree on base `b5419ba` after the fix for F1. `ui.ts` adds
`panKeyboard`, which changes `offsetTop` and dispatches only `scroll`; the
header and composer test pans from 120 to 60 and expects 107 and 560.

| ID | Status   | Evidence                                                                                                                         |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | "the header and composer stay at the visible area's edges…" fails with the `scroll` listener removed and passes with it restored |

No new findings. The fix changes only the e2e helpers and the one test.

Checks run: `make test` passed; `make e2e` passed (120 passed, 4 skipped).
`make live` is not required.

## Verdict

`Ready to commit`. F1 is resolved; F2 is rejected. Close #9 and #15 after a
check on an iPhone.
