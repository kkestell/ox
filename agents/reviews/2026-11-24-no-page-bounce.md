# Review: the browser places the page on phones

## Scope and coverage

Uncommitted changes in the `no-page-bounce` worktree on base `f08ea0d` (`git merge-base origin/main HEAD`): `cmd/ox/web/src/style.css`, `cmd/ox/web/src/index.tsx`, the deleted `cmd/ox/web/src/viewport.ts`, the three deleted keyboard specs and their helpers in `cmd/ox/web/e2e/`, `cmd/ox/web/AGENTS.md`, and `docs/architecture.md`. There is no plan; the parent session's brief stands in for it: stop the page rubber-banding, drop the home indicator's inset while the keyboard is up, delete `viewport.ts` so Safari places the page around the keyboard, cap approvals at `50dvh`, and delete the specs that faked `visualViewport`.

Requirements checked: "Touch screens", "Home Screen app", "Narrow screens". The change edits no requirement, and none needs editing: no requirement names the keyboard or the page's bounce.

Gaps:

- Headless Chrome has no on-screen keyboard and no rubber-band, so the brief's items 1–3 rest on the user's check on the phone. This review confirmed them from the source only.
- The user's phone check, as the brief reports it, does not say whether approvals were pending with the keyboard up, the case F1 concerns.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                     |
| -- | -------- | ------------- | -------- | ------ | --------------------------------------------------------- |
| F1 | Low      | Correctness   | Fix      | Resolved | Approvals' cap is half the viewport, not half the layout  |
| F2 | Low      | Documentation | Fix      | Resolved | The overscroll comment says the page is fixed to the visible area |
| F3 | Low      | Correctness   | Rejected | —      | Chrome for Android no longer fits the layout above the keyboard |
| F4 | Low      | Correctness   | Rejected | —      | A hardware keyboard drops the composer into the home indicator |

## F1. Approvals' cap is half the viewport, not half the layout

Low · Correctness · Fix · Resolved · `cmd/ox/web/src/style.css:333`

`.pending` was capped at half of `--viewport-height`, the height of the area the layout filled. It is now capped at `50dvh`. The layout is `position: fixed; inset: 0`, so it fills the layout viewport, and in the Home Screen app the brief's own readout shows iOS shrinking the layout viewport when the keyboard opens. Viewport units, `dvh` included, are defined not to follow the on-screen keyboard unless the page opts in with `interactive-widget`, which iOS ignores. Where they diverge, the cap stays at half the full screen while the layout shrinks to the area above the keyboard.

`.pending` sits inside `.composer`, which is `flex: none`, so only the cap bounds it. With a 422 px cap in a layout of roughly 500 px, the header, the approvals, and the composer overflow the layout by well over 100 px, and the composer's box and settings row fall behind the keyboard. Typing a deny reason or a question's "Other" answer focuses a field inside `.pending`, so this is the ordinary way to reach it.

### Evidence

From the source: `.composer { flex: none }` (`style.css:296`), `.pending` inside it (`SessionView.tsx:107`), and the cap at `style.css:333`. The deleted spec "pending approvals leave the composer above the keyboard" guarded this case against the old `viewport.ts`. Whether WebKit's `dvh` follows the shrunken layout viewport in a Home Screen app was not confirmed on a phone. The cap's meaning holds either way: it should be a share of the box it sits in, and `50dvh` matches that box only when the layout is the full viewport.

### Action

Size the cap from the layout's box instead of the viewport. Change `.center`'s `container-type: inline-size` (`style.css:231`) to `size`, which is safe because `.center`'s height is `100%` of the fixed layout and does not depend on its content, and set `.pending { max-height: 50cqh; }`. `main`'s `min-width: min(240px, 50cqi)` keeps working. Headless Chrome cannot observe the difference, because its layout always equals the viewport, so no spec carries it. Check on the phone in the Home Screen app: with a long approval pending, focus the deny reason and see that the composer's settings row sits above the keyboard.

## F2. The overscroll comment says the page is fixed to the visible area

Low · Documentation · Fix · Resolved · `cmd/ox/web/src/style.css:5`

The new comment reads "The page is fixed to the visible area". `.layout` is fixed to the window, the layout viewport, and in Safari with the keyboard up the visible area is smaller than the window. The `.layout` comment at line 49 already says "fills the window". The two comments contradict each other, and the first describes the design this change removed.

### Evidence

`style.css:5` against `style.css:49` and `.layout { position: fixed; inset: 0; }` at line 59.

### Action

Reword line 5 to "The page is fixed to the window; a drag past a scroller's end leaves it in place." No test.

## F3. Chrome for Android no longer fits the layout above the keyboard

Low · Correctness · Rejected · `cmd/ox/web/src/viewport.ts` (deleted)

The deleted file's comment names Chrome for Android, whose default `resizes-visual` mode leaves the layout viewport full height under the keyboard. Without `viewport.ts`, Chrome pans the page to the focused field and the header leaves view while the keyboard is up, as in Safari outside the Home Screen app.

Rejected: no requirement covers placement around the keyboard, the user's device is an iPhone, and the brief's design gives placement to the browser. Adding `interactive-widget=resizes-content` to the viewport meta tag is the cheap fix if Android use ever appears.

## F4. A hardware keyboard drops the composer into the home indicator

Low · Correctness · Rejected · `cmd/ox/web/src/style.css:62`

`--bottom-inset` drops to 0 whenever a text field in `.layout` has focus. On an iPad with a hardware keyboard, focus raises no on-screen keyboard, so while the composer has focus its settings row sits in the home indicator's inset.

Rejected: an unreported configuration whose cost is a row about 20 px lower while typing, with the row still tappable. No CSS signal distinguishes the two keyboards.

## Checks run

- Probe (temporary spec, removed): in headless Chrome at 390×844 with touch and a 34 px bottom safe-area inset from `Emulation.setSafeAreaInsetsOverride`, the composer's bottom margin is 34 px at rest and 16 px once the model picker's search field has focus. Tapping the picker's Effort slider leaves focus on the search field, so the focus selector's match of `input[type=range]` never drops the inset.
- `make test` and `make e2e`, from the brief: pass (157 passed, 4 skipped). Not rerun.
- `make live`: not required; the change does not alter how Ox drives Claude Code.
- No new or changed tests to spot-check: the change deletes specs and adds none, and its behavior shows only on a phone, as `cmd/ox/web/AGENTS.md` now states.
- Open `bug` issues read: #55 (question form buttons cut off) concerns the form's own height and is unchanged by this change.

## Verification 1

Checked the uncommitted tree on base `f08ea0d` after the fixes, which changed only `cmd/ox/web/src/style.css`.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `.center` is `container-type: size` (`style.css:231`) and `.pending` is `max-height: 50cqh` (`style.css:333`). A temporary spec with an 80-line approval measured `.pending` at half of `.center`'s height (422 of 844 px on a phone, 450 of 900 px wide) with the composer inside the window. `.center`'s height still comes from the fixed layout, so size containment changes nothing else, and `main`'s and `.viewer`'s `50cqi` minimums are unchanged. Headless Chrome cannot show the keyboard case; it stays a phone check. |
| F2 | Resolved | `style.css:5` reads "The page is fixed to the window; a drag past a scroller's end leaves it in place.", matching the `.layout` comment. |

No new findings.

Checks: the temporary spec above, removed afterward. `make test` and `make e2e` pass according to the fixing session (157 passed, 4 skipped); not rerun here. `make live` is not required.

## Verification 2

Checked commit `a9205fc` on base `b90e637`, which adds one change since Verification 1. Tapping Send on an emulated phone missed: a tapped button takes focus in Chrome, the prompt loses focus, `--bottom-inset` returns to the home indicator's inset, and the composer moves out from under the tap before it lands. `keepPromptFocus` (`SessionView.tsx:22`) cancels `pointerdown` inside the composer's box except on the textarea, and both forms use it. The spec "tapping Send sends and keeps the keyboard on the prompt" in `session.spec.ts` covers it.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | Unchanged since Verification 1. |
| F2 | Resolved | Unchanged since Verification 1. |

The box holds only the thumbnails with their remove buttons, Attach Image, the prompt, Stop, and Send. The settings row and the approvals sit outside it, so the cancelled `pointerdown` reaches no select, picker, or text field that needs focus. Each button still gets its `click`. Keeping the keyboard up after Send matches the standard chat-app pattern, and no requirement covers it.

No new findings.

Checks:

- The new spec passes as committed.
- With `onPointerDown={keepPromptFocus}` removed from `SessionView`'s form, the spec fails on the second reply.
- With it removed from `NewSessionView`'s form, the spec fails on the first reply.
- Both edits were reverted.
- `make test` and `make e2e` pass according to the fixing session (159 passed, 4 skipped); not rerun here.

## Verdict

Ready to commit. F1 and F2 are resolved, F3 and F4 stay rejected, and Verification 2 found no new defects in the Send fix.
