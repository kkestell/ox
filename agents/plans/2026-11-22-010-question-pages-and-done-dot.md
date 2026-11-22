# One question per page, and an idle dot for completed tool calls

## Goal

Two web client changes, from [todo.md](../../docs/todo.md):

1. **One question per page** (Questions). A form with several questions shows
   them all in one scrolling container. Show one question at a time, with
   Previous and Next buttons. Next is disabled until the shown question has an
   answer, an option or non-empty Other text. Submit sits on the last question
   and needs every answer. Decline stays available on every question. A form
   with one question shows no Previous or Next.
2. **Idle dot for completed tool calls** (filed under Sidebar, but it is the
   transcript). A completed tool call's marker is a check icon. Show the
   sidebar's idle status dot instead.

## Related code

- `cmd/ox/web/src/ApprovalRequest.tsx` — `QuestionForm` renders every question
  of an AskUserQuestion call as a `<fieldset class="question">` and holds the
  answers in a store, one `Answer` per question. `answerOf` decides whether a
  question is answered. Enter in an Other field submits.
- `cmd/ox/web/src/SessionView.tsx:66-77` — renders one `ApprovalRequest` per
  pending approval with `<For each={props.snapshot.approvals}>`.
- `cmd/ox/web/src/ox.tsx:113-118, 380-391` — the page's approvals: added and
  removed by updates, and reconciled by `id` when a snapshot is reloaded after a
  reconnect, so a pending approval's component and its local state survive the
  reconnect.
- `cmd/ox/web/src/SettingsFields.tsx:49` — the precedent for `flush()` before
  moving focus to an element a state write reveals.
- `cmd/ox/web/src/SessionView.tsx:165-186` — `ToolCall` gives the marker the
  class and accessible name `running`, `done`, `failed`, or `stopped`.
- `cmd/ox/web/src/style.css:81` — `.marker.done` is Lucide's check icon.
  `style.css:188-194` — the sidebar's `.status` dot, `#525252` for `idle` and
  `parked`. `style.css:243` — `.marker.running::before` already draws the same
  dot in the transcript.
- `cmd/ox/web/e2e/session.spec.ts:612-682` — the question form specs, with the
  two-question fixture `questions` (Color, single-select; Sections,
  multi-select) and `askQuestions`.

## Decisions

- **Page state.** `QuestionForm` gets `const [page, setPage] = createSignal(0)`,
  the index of the shown question. It is local to the form, like the answers
  store, and dies with the form. The answers store keeps every question's
  answer regardless of which is shown, so Previous and Next lose nothing, Other
  text included.
- **Rendering.** Keep the `<For>` over every question and set
  `hidden={i() !== page()}` on each fieldset, so each question's inputs keep
  their DOM and ids. `.approval .question` sets `display: flex`, which overrides
  the browser's `[hidden]` style; add `.approval .question[hidden] { display:
  none; }`, following `nav[hidden]` in `style.css`.
- **Gating.** `current()` is `answerOf(props.questions[page()],
  answers[page()]) !== ""`. Next is disabled unless `current()`. Submit keeps
  `answered()` (every question). Because Next is the only way forward and needs
  the shown question answered, every question before the shown one is answered,
  so Submit on the last question is enabled exactly when the last is answered;
  `answered()` stays the rule Submit checks.
- **Layout.** The actions row follows the standard wizard footer: on the left,
  the position as muted text, "2 of 3"; on the right, Decline, Previous, then
  Next, or Submit on the last question. Previous is disabled on the first
  question. With one question, the position, Previous, and Next are not
  rendered, and the form is as today. "Claude asks" and each question's header
  and legend stay as they are. The position adds information: nothing else
  shows how many questions remain.
- **Keyboard.** Enter in an Other field advances: Next on any question but the
  last, Submit on the last, each only when its button is enabled. On Previous or
  Next, `flush()` and move focus to the shown question's first checked input,
  or its first input, so keyboard focus never rests on a hidden or newly
  disabled button. Radio arrow keys and Tab order are the browser's.
- **Sending.** While a Submit or Decline is in flight, Previous and Next are
  disabled with every other control.
- **Requirements.** *Questions from Claude* changes (below). Through a request's
  lifecycle: a form withdrawn or answered from another page while on any
  question disappears with its approval, as every approval does under *Approvals
  wait* and *Permission requests*; nothing of its page or answers outlives it.
  A Submit from this page after another page answered gets the existing error
  under *One answer per approval*, shown by `control.approve`'s attempt. A
  reconnect reconciles approvals by `id`, so the form keeps its page and
  answers. A reload or a second page starts the form on its first question with
  no answers, as answers do today. *Tool calls* holds: the marker keeps the
  accessible name `done`.
- **Done marker.** `.marker.done` draws the dot instead of the check: extend
  `.marker.running::before` to `.marker.running::before, .marker.done::before`,
  and extend `.status.idle, .status.parked` to include `.marker.done`, so the
  idle color is declared once. Remove the check icon rule. `failed` and
  `stopped` keep their icons.

## Check your assumptions

1. **Questions shown one at a time.** Approved.
   - **Plan:** A form with several questions shows one question at a time.
     Previous and Next move between them, keeping answers; Next waits for an
     answer to the shown question; Submit is on the last question; Decline is
     on every question. Requirement text below.
   - **Alternative:** Keep all questions in one scrolling form. The user scrolls
     a tall form above the composer to find unanswered questions.
2. **Position shown as "2 of 3".** Approved.
   - **Plan:** The form's footer shows which question is shown and how many
     there are, so the user knows how many remain.
   - **Alternative:** No position text. The user learns the count only by
     reaching the last question.
3. **Enter in an Other answer moves to the next question.** Approved.
   - **Plan:** Enter in an Other field goes to the next question, and submits
     on the last one, as Enter submits today.
   - **Alternative:** Enter submits only on the last question and does nothing
     earlier. The user reaches for the mouse or Tab to continue.

## Test plan

All page behavior, in `cmd/ox/web/e2e/session.spec.ts` with the fake harness.
The two-question fixture reaches every case. Locate Next, Previous, and Submit
by role and name within the form.

- Replace "Submit waits for an answer to every question" with "Next waits for
  an answer to the shown question": the form shows "1 of 2"; Color is visible,
  Sections is not; Previous is disabled; Submit is absent; Next is disabled
  until Red is checked; Next shows Sections and "2 of 2", Next is absent,
  Submit is disabled until Appendix is checked.
- New "Previous keeps answers": type "Green" in Color's Other answer, Next,
  check Appendix, Previous: Color's Other is checked with "Green"; Next:
  Appendix is checked and Submit is enabled. Fails today because there is no
  Next.
- New "keyboard moves between questions": fill Color's Other answer and press
  Enter: Sections is shown and its Introduction checkbox is focused. Check it,
  press Previous: Color's Other radio is focused. Fails today because Enter
  submits.
- "choosing an option or Other leaves one answer to a single-select question":
  the Color assertions move from Submit to Next; check Introduction after Next,
  then Submit.
- "submitted answers reach the tool by question text": click Next between the
  questions; the assertions on the answers are unchanged.
- "Decline denies the question": click Next after answering Color, then
  Decline from the second question, so Decline mid-form is covered.
- New "a form keeps its question and answers across a reconnect": answer Color,
  Next, check Appendix, `services.restartRelay()`, wait as "a reconnect keeps
  typed text and sends nothing again" does; the form still shows "2 of 2" with
  Appendix checked. It guards the reconcile by `id` that the page state relies
  on; no existing test reaches a pending form across a reconnect.
- New "a single question has no Previous or Next": `ask` with a one-question
  input: Previous, Next, and the position text are absent; Submit is enabled
  once an option is checked. The live `approval.spec.ts` case also uses one
  question and passes unchanged.
- Done marker: the existing assertions on the accessible name `done` stand. Its
  look is checked in headless Chrome through the `make dev` stack on spare
  ports, with a screenshot of a transcript holding a done, a running, and a
  failed tool call beside the sidebar, for the user to judge.

## Implementation plan

1. `cmd/ox/web/src/style.css`: replace the `.marker.done` check rule with the
   dot (Decisions, Done marker); add `.approval .question[hidden]` and a muted
   position style in `.approval .actions` that pushes the buttons right.
2. `cmd/ox/web/src/ApprovalRequest.tsx`: in `QuestionForm`, add `page`,
   `current()`, fieldset refs, `go(page)` that sets the page, flushes, and
   focuses, and `advance()` for Enter; hide all but the shown fieldset; render
   the position, Previous, and Next only when there is more than one question;
   update the comment above `QuestionForm`.
3. `cmd/ox/web/e2e/session.spec.ts`: the tests above.
4. `docs/functional-requirements.md`: the approved requirement text.
5. `docs/todo.md`: remove both entries; the Questions section goes with its
   only entry.

## Requirement changes

- **Changed:** **Questions from Claude.**
  - Old: A question from Claude appears as a form with its options and an Other
    answer; the user submits answers to every question or declines.
  - New: A question from Claude appears as a form with its options and an Other
    answer, one question at a time. The user moves between questions with
    Previous and Next without losing answers, moves on only once the shown
    question is answered, submits from the last question, and can decline from
    any question.

## Documentation updates

- `docs/todo.md`: remove **One question per page** and **Idle dot for completed
  tool calls**, and the emptied Questions heading.
