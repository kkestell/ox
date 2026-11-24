# Question form without raw input, with its buttons in view

## Goal

A question from Claude shows a collapsed `Input` row with the call's raw JSON
under its form, and a long question pushes Decline, Previous, Next, and Submit
out of the approval region until the user scrolls it. The form should show only
the question and its options, and its buttons should stay in view however long
the question is, with the question scrolling beneath them. Resolves
[#55](https://github.com/kkestell/ox/issues/55).

## Related code

- `cmd/ox/web/src/ApprovalRequest.tsx` — `QuestionForm` renders the questions,
  then the `<details class="input">` row with `toolInputJSON`, then
  `.actions`. `PermissionCard` renders its plan, its input row, its choices,
  and an `.actions` row with Submit.
- `cmd/ox/web/src/SessionView.tsx` — the `section.pending` ("Waiting on you")
  holds every pending approval's `article.approval` above the composer's box.
- `cmd/ox/web/src/style.css` — `.pending` is the one scroll container, capped
  at `50cqh`, with `padding-bottom: 2rem` and `margin-bottom: -1rem`, so its
  last 1rem sits beneath the composer's box. `.approval .actions` is a plain
  flex row. `.approval .input` styles stay in use by `PermissionCard`.
- `cmd/ox/web/e2e/session.spec.ts` — the question form specs
  (`askQuestions`, "Decline denies the question", …) and the permission card
  specs, driven through the fake harness's `approve <tool> <input JSON>`
  prompt (`ask` in `e2e/ui.ts`).

## Decisions

- **The input row goes from the question form only.** The form already shows
  every question, header, option, and description, and Submit sends the
  original input plus the answers. `PermissionCard` keeps its input row, since
  Allow approves the input exactly as shown. The transcript's tool call still
  shows the full input.
- **Buttons stick to the bottom of the approval region.** `.approval .actions`
  becomes `position: sticky` at the bottom of `.pending`'s scrollport, on the
  card's background. Sticky is bounded by its `article`, so with several
  approvals pending, each approval's buttons stay in view while that approval
  is, and the region keeps one scroller. The pinned row rests where it sits
  when the region is scrolled to its end: above the 1rem the composer's box
  covers, with the strip below it filled by the card's background so options
  scrolling beneath do not show between the row and the box. The rule is on
  `.approval .actions`, so it also keeps a permission card's Submit in view
  beneath a long plan or input, which the same cap cuts off today.
- **Requirements:** "Questions from Claude" (decline from any question, move
  with Previous and Next, submit from the last) now holds for a question of
  any length without scrolling to the buttons. "Allow and Deny" holds: the
  permission card still shows its input. No request, reply, or state changes,
  so the request lifecycle is unchanged; the form is hidden while its answer is
  in flight, as before.
- **Construction:** the buttons stay visible by layout alone, in CSS; the page
  reads no viewport measurements, as `cmd/ox/web/AGENTS.md` requires.

## Check your assumptions

1. **Permission cards keep their Submit in view too.**
   - **Plan:** A permission request whose plan or input is long, such as a plan
     approval on a phone, also keeps its Submit button pinned at the bottom of
     the approval area while the plan scrolls beneath it.
   - **Alternative:** Only question forms pin their buttons; a long permission
     card keeps Submit below its plan and input, reached by scrolling the
     approval area.

## Test plan

All e2e, in `cmd/ox/web/e2e/session.spec.ts` beside the question form specs.

- New test "a question form shows no raw input": `askQuestions`, then the form
  does not contain the input's JSON (for example `not.toContainText('"multiSelect"')`).
  Fails before the change, since the closed `<details>` keeps the JSON in the
  form's text. No existing test checks what the form omits.
- New test "a long question keeps its buttons in view": at a 390 × 700
  viewport (`page.setViewportSize`), ask one single-select question with
  enough options and descriptions to overflow the approval region (about 12).
  The question's legend and the Decline and Submit buttons are each
  `toBeInViewport({ ratio: 1 })`; then check the last option, which scrolls the
  region, and Submit is still in view and enabled. Geometry is the behavior
  here, the exception `cmd/ox/web/AGENTS.md` allows. Fails before the change,
  where Decline lies below the region's visible part.
- New test "a long permission card keeps Submit in view": at the same
  viewport, ask `ExitPlanMode` with a plan long enough to overflow the region;
  Submit is `toBeInViewport({ ratio: 1 })`. Fails before the change.

## Implementation plan

1. `cmd/ox/web/src/ApprovalRequest.tsx`: delete the `<details class="input">`
   block from `QuestionForm`. Update `QuestionForm`'s comment only if it names
   the input row.
2. `cmd/ox/web/src/style.css`: make `.approval .actions` sticky at the bottom
   of `.pending`, on `var(--card)`, resting above the composer box's overlap
   with the strip below it covered, as Decisions describes. Adjust the
   `.pending` comment if it no longer describes the layout.
3. `cmd/ox/web/e2e/session.spec.ts`: add the three tests above.
4. Check the long question form and a long plan approval in headless Chrome at
   390 × 700 and at a desktop size, with one and with two approvals pending,
   and name both views in the report.

## Requirement changes

None.
