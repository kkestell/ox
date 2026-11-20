# Answer AskUserQuestion from a form

## Scope and coverage

Uncommitted changes on `main` at `3aa563a` (`cmd/ox/web/e2e/approval.spec.ts`, `cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/src/ApprovalRequest.tsx`, `cmd/ox/web/src/ox.tsx`, `cmd/ox/web/src/style.css`, `docs/guarantees.md`, `docs/todo.md`), against `.agents/plans/2026-11-19-002-answer-askuserquestion.md`. Also read the callers and state the form depends on: `SessionView.tsx` (one `ApprovalRequest` per pending approval), the approval store in `ox.tsx` (push on `approval`, splice on `approval_answered`/`approval_cancelled`, `reconcile` by `id` on reconnect), and `docs/claude-code.md` §5.

The change delivers the plan's Goal and keeps its Decisions: `questionsOf` checks the recorded shape and falls back to the generic card; the form posts the original input with `answers` keyed by question text, multi-select in option order with free text last; Submit waits for every question; Decline denies with "User declined to answer"; both go through `onAnswer`, so failures and a second page's answer behave as for any approval. Reconnect keeps the form's state, since `reconcile` by `id` keeps the approval object and `For` keeps the card. No departures from the plan beyond F2.

Gaps:

- The live spec was not run against the base revision; from the source, the base card has no `fieldset`, so its first wait fails.

## Summary

| ID | Severity | Category | Action | Status | Title                                                        |
| -- | -------- | -------- | ------ | ------ | ------------------------------------------------------------ |
| F1 | Medium   | Testing  | Fix    | Open   | Single-choice Other handling has no test                     |
| F2 | Low      | Intent   | Fix    | Open   | Checkboxes keep the text-input box and native look           |

## F1. Single-choice Other handling has no test

Medium · Testing · Fix · `cmd/ox/web/src/ApprovalRequest.tsx:149`

Three conditions decide which answer a single-choice question sends when the user moves between an option and Other, and none is protected by a test:

- `pick` sets `a.other = false` (line 149). Without it, typing "Green" in Other and then picking Red shows Red selected (the browser unchecks the Other radio) but sends "Green", breaking guarantee 7's "sends that input with the chosen answers" as the card shows them.
- The Other field's `onInput` clears `labels` for a single choice (line 206). Without it, picking Red, typing in Other, and clearing the field shows Other selected with no text, yet Submit is enabled and sends "Red".
- The Other radio's `onChange` clears `labels` for a single choice (line 192). Without it, picking Red and then the empty Other radio leaves Submit enabled and sends "Red".

The current code is correct; the e2e test only ever picks Red in "Which color?" and never touches its Other, so each condition can be removed with the test still passing.

### Evidence

Removed each condition in a scratch worktree and ran "AskUserQuestion is answered from a form": all three mutants passed. Removing the option-order sort or changing `every` to `some` in `answered` made it fail, as expected. A headless probe on the unmutated code confirmed the intended behavior: after typing "Green" then picking Red, Other is unchecked, its field keeps "Green", and Submit sends `"…?":"Red"`.

### Action

Extend "AskUserQuestion is answered from a form" (`cmd/ox/web/e2e/session.spec.ts:254`) on "Which color?", after "Which sections?" is answered so Submit's state reflects only the first question: pick Red, then check the empty Other radio, and expect Submit disabled; pick Red, fill Other with "Green" and then clear it, and expect Submit disabled; fill Other with "Green", pick Red, and expect the Other field still to read "Green". The existing posted-body assertion (`"Which color?":"Red"`) then guards the last step. Confirm each of the three conditions fails the test when removed.

## F2. Checkboxes keep the text-input box and native look

Low · Intent · Fix · `cmd/ox/web/src/style.css:222`

The base `input` rule (`style.css:86-89`) gives every input a 2.25rem height, horizontal padding, border, background, and shadow; `input[type="radio"]` overrides that, but nothing does for checkboxes. In a multi-select question each checkbox is a 13×36px box: the native checkbox is drawn centered in it, about 8px below its label's first line rather than level with it as the radios are, with a faint strip of the input background and shadow showing beneath it. The checkboxes are the browser's blue native control beside the monochrome custom radios, and their labels sit 3px left of the radio questions' labels. The plan's step 3 asked for checkbox alignment matching the radio rules; extending the margin rules at lines 222-223 to checkboxes does not achieve it because of the inherited height.

### Evidence

Headless Chrome on the change, through the e2e stack with a replayed two-question approval at 1280×900: `boundingBox` of the Appendix checkbox is 13×36 at x=333, its label at x=358; the Red radio is 16×16 at x=333, its label at x=361. Screenshots show the offset, the native blue check, and the strip under each checkbox. The rest of the form lays out well: header muted above the question, descriptions muted under each label, the Other field full width, Input collapsed, Decline and Submit right-aligned with a gap.

### Action

Add an `input[type="checkbox"]` rule beside the radio rules in `style.css` (`appearance: none`, `flex: none`, 1rem square, no padding, the radio's border, background, and shadow, a small radius, a `--primary` check mark when `:checked`, and the `:focus-visible` border), so checkboxes match the radios' size, look, and alignment. Styling needs no kept test; the user judges the look.

## Checks run

- `make test`: passed.
- `make e2e`: 15 passed, 3 skipped (the live specs).
- `make live`: `TestLive` passed; `approval.spec.ts` 3 passed, including "an AskUserQuestion is answered from its form".
- "AskUserQuestion is answered from a form" against the base revision's `src` with the change's specs: fails at the legend assertion.
- Removed conditions, each alone, running "AskUserQuestion is answered from a form": `a.other = false` in `pick` (passed — F1), `labels = []` in the Other field's `onInput` (passed — F1), `labels = []` in the Other radio's `onChange` (passed — F1), option-order sort in `answerOf` (failed), `every` → `some` in `answered` (failed).
- Headless Chrome probe through the e2e stack (replayed approval, long question and description text, 1280×900): layout as in F2's evidence; Enter in an Other field submits; single-choice Other interplay behaves as planned.

## Verification 1

Checked the uncommitted changes on `3aa563a` after the fixes, which touch only `cmd/ox/web/e2e/session.spec.ts` (new steps in "AskUserQuestion is answered from a form") and `cmd/ox/web/src/style.css` (an `input[type="checkbox"]` rule beside the radio rules). Reran `make test` (passed), `make e2e` (15 passed, 3 skipped), and `make live` (`TestLive` passed; `approval.spec.ts` 3 passed), and looked at the form in headless Chrome through the e2e stack in light and dark schemes.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The test now moves "Which color?" between Red and Other three ways. In a scratch worktree, removing `a.other = false` in `pick`, the `labels = []` in the Other field's `onInput`, or the `labels = []` in the Other radio's `onChange` each fails it; unmutated, it passes. |
| F2 | Open     | Size and alignment are fixed: each checkbox is 16×16 at x=333 with its label at x=361, the same as the radios, with no strip beneath it, in both schemes. The check mark is hard-coded `#888` in the data URL instead of `--primary`, so a checked box reads fainter than a checked radio: a gray tick beside the radio's black dot in light mode, and beside its near-white dot in dark mode. |

For F2, draw the mark in `--primary` the way the radio does: keep the `:checked` background as the unchecked one, and add `input[type="checkbox"]:checked::before { content: ""; display: block; width: 100%; height: 100%; background: var(--primary); mask: url(<the same check SVG>) center / 100% no-repeat; }`. Injected into the page in headless Chrome, this renders a black tick in light mode and a near-white tick in dark mode, matching the radio dots, with the box's border and size unchanged.

No new findings.

## Verification 2

Checked the uncommitted changes on `3aa563a`. The only change since Verification 1 is in `cmd/ox/web/src/style.css`: the checked checkbox keeps the unchecked background and draws its mark as a `::before` filled with `var(--primary)` and masked to the check. Reran `make test` (passed), `make e2e` (15 passed, 3 skipped), and `make live` (`TestLive` passed; `approval.spec.ts` 3 passed), and looked at the form in headless Chrome through the e2e stack in light and dark schemes.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | Unchanged since Verification 1. |
| F2 | Resolved | In both schemes each checkbox is 16×16 at x=333 with its label at x=361, level with the radios, and the tick is drawn in `--primary`: black in light mode and near-white in dark mode, matching the checked radio's dot. |

No new findings.

## Verdict

Ready to commit.
