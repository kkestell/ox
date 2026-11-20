# Answer AskUserQuestion from a form

## Goal

From [`docs/todo.md`](../../docs/todo.md) (Approvals): "Answer
`AskUserQuestion`. Show the questions and options as a form and allow with
`updatedInput` holding the original input plus `answers`
(`docs/claude-code.md` §5). Today Allow echoes the input with no answers."

When done, an `AskUserQuestion` approval shows each question with its options
as a form. Submit allows with the original input plus `answers`, keyed by
question text, so Claude's tool result reads `Your questions have been
answered: …`. Decline denies. Any other approval, and an `AskUserQuestion`
whose input does not have the recorded shape, keeps today's Allow/Deny/Other
card.

The change is confined to the web client. `oxd` already forwards an allow
response whose `updatedInput` is an object (`internal/claude/approval.go:73-86`),
and the protocol carries Claude's `request` and the page's `response`
untouched.

## Related code

- `cmd/ox/web/src/ApprovalRequest.tsx` — the approval card: Allow sends
  `snapshot(request.input)` as `updatedInput`; Deny and Other deny with a
  message; `onAnswer` returns the RPC's promise and `sending` disables the
  card while it is in flight. The form is added here.
- `cmd/ox/web/src/ox.tsx:46-51` — `planOf` and `toolInputJSON`, the pattern
  for reading a tool's own input shape; `questionsOf` goes beside them.
- `cmd/ox/web/src/ox.tsx:222-223` — `control.approve` posts `session/approve`
  through `start`, which shows a failure with `createAttempt` in the session
  view. Unchanged.
- `cmd/ox/web/src/connection.ts:48,73` — `@ungap/raw-json` `parse`/`stringify`
  keep raw JSON numbers in `input`; a shallow spread of `snapshot(input)`
  keeps them.
- `cmd/ox/web/src/SessionView.tsx:71-77` — renders one `ApprovalRequest` per
  pending approval. Unchanged.
- `cmd/ox/web/src/style.css:204-225` — `.approval` styles.
- `cmd/ox/web/e2e/session.spec.ts:217-243` — "Allow sends the exact numbers
  displayed in the tool input": replays a raw `approval` event through a
  patched `EventSource`, since the fake harness never asks for approval, and
  asserts the posted `session/approve` body.
- `cmd/ox/web/e2e/approval.spec.ts` — Haiku approval specs, run by `make live`.

## Decisions

- **Where the form lives.** `ApprovalRequest` renders `QuestionForm` (a new
  component in `ApprovalRequest.tsx`) when `questionsOf(tool_name, input)`
  returns questions, and today's card otherwise. The component list in
  `docs/architecture.md` names files, so keeping the form in the same file
  leaves it accurate.
- **Shape check.** `questionsOf(tool, input)` in `ox.tsx` returns
  `Question[]` when `tool === "AskUserQuestion"` and `input.questions` is a
  nonempty array whose every item has a string `question` and an `options`
  array whose every item has a string `label`; `header` and `description`
  are used when they are strings, and `multiSelect` is `true` only when it is
  `true`. Anything else returns `undefined` and the generic card shows, so an
  unexpected shape is still answerable (Deny). Counts (1–4 questions, 2–4
  options) and header length are Claude's limits, not checked.
  `type Question = { question: string; header?: string; options: { label: string; description?: string }[]; multiSelect: boolean }`,
  exported from `ox.tsx` (Claude's payload, not Ox's protocol, so not in
  `protocol.ts`).
- **Form.** The card heading is "Claude asks" instead of "Allow
  AskUserQuestion?". Each question is a `<fieldset>` whose `<legend>` is the
  question text, preceded by its `header` in a small muted span when present.
  Each option is a radio (single choice) or checkbox (`multiSelect`) labelled
  by its `label`, with its `description` as muted text beneath. Each question
  ends with an Other choice: a radio or checkbox with a text field, as in
  today's card (`aria-label="Other"` on the box, `aria-label="Other answer"`
  on the field). Typing in the field selects Other; for a single choice,
  picking an option clears Other's selection but keeps its text. Enter in a
  field submits. The `details.input` block stays, with subject "Input", as
  for a plan. Buttons: Decline (secondary) and Submit (primary), both
  disabled while `sending`.
- **Answers.** Per question, state `{ labels: string[]; other: boolean; text: string }`
  in one `createStore` array indexed like the questions. The answer is:
  single choice — the chosen label, or `text.trim()` when Other is chosen;
  multi-select — the chosen labels in option order, then `text.trim()` if
  Other is checked and nonempty, joined with `", "`. An empty answer is
  unanswered. `answers` is an object from question text to answer, omitting
  unanswered questions (§5). Submit is enabled once every question is
  answered (see Check your assumptions).
- **Responses.** Submit sends
  `{ behavior: "allow", updatedInput: { ...snapshot(request.input), answers } }`.
  Decline sends `{ behavior: "deny", message: "User declined to answer" }`.
  Both go through `props.onAnswer`, so failures show as today.
- **Guarantees:**
  - 7 (approvals wait for an answer): Submit still sends the input as the
    card shows it, with `answers` added; the original keys and raw numbers
    are untouched. The approval leaves the page only on
    `approval_answered`/`approval_cancelled`, as today, so a lost reply, a
    reconnect, or a second page answering behaves as for any approval: a
    second page's answer removes the form everywhere, and a late Submit is
    rejected by `oxd` and shown as an error.
  - 4 and 5: the form answers through `control.approve`, which already
    settles every request and shows its error in the session view. Form
    state is local to the card; navigating away and back resets it, as
    today's choice does.
- **Claude Code:** §5 `AskUserQuestion` records the input shape, the
  `answers` format (single, multi-select with free text last, omitted
  unanswered questions), the tool result text, and that it arrives as
  `can_use_tool`. The live spec below confirms the answer reaches Claude in
  the current version. Ox starts Claude without `--disallowedTools
  AskUserQuestion`, so the tool is available.

## Check your assumptions

1. **Submit waits until every question is answered.** Kept.
   - **Plan:** Submit stays disabled until each question has a choice or
     typed answer, so a multi-question form is not sent half-filled by
     accident; Decline is the way out.
   - **Alternative:** Submit once any question is answered, sending only
     those; Claude then proceeds without the rest, and the user can skip a
     question they don't care about.
2. **Each question has an Other field for a typed answer.** Kept.
   - **Plan:** Every question ends with Other and a text field, as Claude
     Code's own question prompt and today's approval card offer, so the user
     is never limited to Claude's options.
   - **Alternative:** Options only; a simpler form, but an answer outside
     the options needs Decline and a new prompt.

## Test plan

- Form and posted answer (e2e, `session.spec.ts`): new test "AskUserQuestion
  is answered from a form". No existing test reaches it except through the
  replay fixture, so move the patched `EventSource` from "Allow sends the
  exact numbers displayed in the tool input" into a helper in
  `session.spec.ts` (`replayApprovals(page)` installing the init script,
  and `replay(page, requestId, toolName, inputJSON)` dispatching the update)
  used by both tests. Replay an `AskUserQuestion` with compact input
  `{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red","description":"Warm"},{"label":"Blue","description":"Cool"}],"multiSelect":false},{"question":"Which sections?","header":"Sections","options":[{"label":"Introduction","description":"a"},{"label":"Conclusion","description":"b"},{"label":"Appendix","description":"c"}],"multiSelect":true}]}`.
  Assert:
  - the card shows both legends and the Red option's description;
  - after checking Red only, Submit is disabled;
  - check Appendix, then Introduction, check Other in "Which sections?" by
    typing "Glossary"; Submit is enabled;
  - Decline posts `"response":{"behavior":"deny","message":"User declined to answer"}`
    (the fake harness rejects the answer, so the card stays);
  - Submit posts a body containing
    `"updatedInput":<input without its closing brace>,"answers":{"Which color?":"Red","Which sections?":"Introduction, Appendix, Glossary"}}`,
    proving the original input is echoed, option order, and free text last;
  - a second replayed approval, tool `AskUserQuestion` with input
    `{"questions":"x"}`, shows the generic card (its "Allow" radio).
  Each assertion fails on the current code, which has no form.
- Answer reaches Claude (live, `approval.spec.ts`): new test "an
  AskUserQuestion is answered from its form". New session in `manual`; prompt
  "Load and call the AskUserQuestion tool now with one single-select question,
  "Which color?", with the options Red and Blue. Then reply with just the
  chosen color."; wait for `.approval fieldset`; check Red; Submit; the
  approval disappears and Send returns; expand the `AskUserQuestion` tool card
  and expect its result to contain `="Red"` (the §5 tool result format, not
  model wording). Fails today: Allow sends no `answers`.

## Implementation plan

1. `cmd/ox/web/src/ox.tsx`: add `Question` and `questionsOf` beside `planOf`.
2. `cmd/ox/web/src/ApprovalRequest.tsx`: move today's card body into a
   local component (`PermissionCard`), add `QuestionForm` as decided, and
   have `ApprovalRequest` choose with `<Show when={questionsOf(...)} fallback={…}>`.
   Update the header comment to describe both.
3. `cmd/ox/web/src/style.css`: styles for the question fieldsets (no
   border, legend in `--foreground`, header and descriptions in
   `--muted-foreground`), checkbox alignment matching the radio rules, and a
   gap between Decline and Submit in `.approval .actions`.
4. `cmd/ox/web/e2e/session.spec.ts`: the replay helper and the new test.
5. `cmd/ox/web/e2e/approval.spec.ts`: the live test.
6. Run `make test`, `make e2e`, and `make live`.

## Documentation updates

- `docs/todo.md`: remove the "Answer `AskUserQuestion`" item.
- `docs/guarantees.md` item 7: after "Allow sends the tool's input as the
  card shows it", add that answering Claude's questions sends that input with
  the chosen answers.
