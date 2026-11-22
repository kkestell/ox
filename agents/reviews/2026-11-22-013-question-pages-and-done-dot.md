# One question per page, and an idle dot for completed tool calls

## Scope and coverage

Uncommitted changes on `dac4fb8` in `/Users/kyle/src/ox-wt-transcript`:
`cmd/ox/web/src/ApprovalRequest.tsx`, `cmd/ox/web/src/style.css`,
`cmd/ox/web/e2e/session.spec.ts`, `docs/functional-requirements.md`, and
`docs/todo.md`, against
[the plan](../plans/2026-11-22-010-question-pages-and-done-dot.md). Callers
read: `SessionView.tsx` (approvals `<For>`, `ToolCall` markers) and `ox.tsx`
(approval updates and reconcile by `id`).

The requirements diff matches the plan's **Questions from Claude** text word
for word, and `docs/todo.md` removes exactly the two entries and the emptied
Questions heading.

Gaps:

- Two approvals pending at once in one session cannot be produced with the
  fake harness, which holds one approval at a time. Whether an unrelated
  approval's arrival or removal resets the form was settled from the source:
  `apply` pushes and splices the store array, and the snapshot reload
  reconciles it by `id`, so `<For>` keeps each existing item and its
  `QuestionForm`, page signal included. The reconnect spec observes the
  reconcile path.

## Summary

| ID | Severity | Category    | Action   | Status   | Title                                                   |
| -- | -------- | ----------- | -------- | -------- | ------------------------------------------------------- |
| F1 | Low      | Testing     | Fix      | Resolved | Enter on an unanswered question has no test             |
| F2 | Low      | Conventions | Fix      | Resolved | `paged` adds a second untracked read of `props.questions` |
| F3 | Low      | Simplicity  | Rejected | —        | `go()` finds the fieldset by DOM query instead of refs  |

## F1. Enter on an unanswered question has no test

Low · Testing · Fix · Resolved · `cmd/ox/web/src/ApprovalRequest.tsx:161`

`advance()` moves to the next question only when `current()` holds. That guard
carries the requirement that the user "moves on only once the shown question is
answered" for the keyboard path. No spec presses Enter in an empty or blank
Other field on a question before the last.

### Evidence

With `current() &&` removed from `advance()`, all 17 question and reconnect
specs pass. In headless Chrome against the dev stack, the code as written keeps
"1 of 3" after Enter in an empty and in a whitespace-only Other field, so the
behavior is right and only the test is missing.

### Action

In "keyboard moves between questions" in `cmd/ox/web/e2e/session.spec.ts`,
press Enter in Color's empty Other answer before filling it, and assert the
form still shows "1 of 2" and Sections is hidden. Confirm it fails with the
`current()` guard removed from `advance()`.

## F2. `paged` adds a second untracked read of `props.questions`

Low · Conventions · Fix · Resolved · `cmd/ox/web/src/ApprovalRequest.tsx:133`

`QuestionForm` reads `props.questions` in its body, outside any tracking
scope, at `createStore`'s initializer and now again at
`const paged = props.questions.length > 1`. `props.questions` is the `<Show>`
condition accessor from `ApprovalRequest`, so each body read triggers Solid's
`STRICT_READ_UNTRACKED` diagnostic. The base revision logs it once per form;
the change logs it twice. `cmd/ox/web/AGENTS.md` asks for `untrack` on an
untracked read and for diagnostics to be addressed at their cause.

### Evidence

Dev stack in headless Chrome, one three-question form: the changed tree logs
`[STRICT_READ_UNTRACKED] Reactive value "condition"` and `"condition value"`
twice each from `ApprovalRequest.tsx:19`; `HEAD`'s `ApprovalRequest.tsx` logs
each once.

### Action

At the top of `QuestionForm`, read the questions once with
`const questions = untrack(() => props.questions);` and use `questions` for
the store, `paged`, `last`, `current`, `answered`, `submit`, the `<For>`, and
the position text. A form's questions are fixed for its lifetime, since the
approval's input does not change. This also removes the existing diagnostic.
The diagnostic appears only in the dev build, which the e2e specs do not run,
so the check is a clean console on the dev stack with a question form open; no
spec is kept.

## F3. `go()` finds the fieldset by DOM query instead of refs

Low · Simplicity · Rejected · `cmd/ox/web/src/ApprovalRequest.tsx:156`

`go()` takes the shown fieldset as `form.querySelectorAll(".question")[to]`,
where the plan named fieldset refs. A ref callback reading `i()` raises
`STRICT_READ_UNTRACKED`; the documented form is `untrack(i)` inside the ref
callback, or `<For keyed={false}>` for a plain index.

### Action

Rejected: the fieldsets are rendered by one `<For>` over a fixed array, so DOM
order is the questions' order by construction, and the query runs after
`flush()` on the rendered state. A ref array kept beside the `<For>` gives the
same element with more code.

## Checks run

- `make test`: passed.
- `make e2e`: 112 passed, 4 skipped.
- Spot removals, each against the question and reconnect specs:
  - `current()` gate on Next: "Next waits for an answer to the shown question"
    and "choosing an option or Other…" fail.
  - `flush()` in `go()`: "keyboard moves between questions" fails on focus.
  - `.approval .question[hidden] { display: none; }`: "Next waits…" fails on
    `toBeHidden`.
  - `current()` in `advance()`: nothing fails (F1).
- Headless Chrome through a dev stack on ports 19580 and 19581 with the fake
  harness, a three-question form:
  - Enter in an empty or blank Other on question 1 stays on "1 of 3"; Next is
    disabled.
  - Enter with "Green" moves to "2 of 3" and focuses Introduction.
  - Submit on question 3 is disabled until it is answered.
  - Previous from question 3 keeps Introduction and Appendix checked and
    focuses the first checked box; Previous again keeps "Green".
  - The form has no `<form>` element or `required` input, so a hidden
    fieldset cannot block or bypass Submit; Submit is gated by `answered()`.
- Done marker, computed `::before` style in light and dark: `.marker.done`
  and `.status.idle` are both `rgb(82, 82, 82)`, 8×8 px, radius 50%, no mask.
  They share one declaration, so they match by construction. Screenshots of
  the form in both themes were taken for the user to judge.
- `make live`: not required. The change does not alter how Ox drives Claude
  Code.

## Verification 1

Uncommitted changes on `dac4fb8`, after the fixes for F1 and F2.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | "keyboard moves between questions" presses Enter in Color's empty Other answer and expects "1 of 2" with Sections hidden; with `current()` removed from `advance()` it fails on "1 of 2" not found, and it passes with the guard. |
| F2 | Resolved | `QuestionForm` reads `untrack(() => props.questions)` once and uses it throughout. On a dev stack (ports 19580 and 19581, fake harness), a two-question form answered through Next, Previous, Next, and Submit logs no `STRICT_READ_UNTRACKED` diagnostic; the base revision logged one per form. |

No new findings. The changes since the review touch only `ApprovalRequest.tsx`
(the `untrack` read) and the keyboard spec.

Checks run on the final tree:

- `make test`: passed.
- `make e2e`: 112 passed, 4 skipped.

## Verdict

`Ready to commit`: F1 and F2 are resolved; F3 is rejected.
