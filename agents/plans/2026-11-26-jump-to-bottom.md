# Jump to bottom, and return to the bottom on send and answer

## Goal

A transcript scrolled up from its bottom stops following output, and the only
way back is to scroll by hand. When the change is done, a round button with a
down arrow floats over the transcript, centered above the composer, whenever
the transcript is scrolled up; pressing it returns the transcript to its
bottom, where it follows output again. Sending a prompt, or answering an
approval or a question from Claude, does the same.

## Related code

- `cmd/ox/web/src/style.css` (`.log`, ~line 266) — the transcript scroller is
  `flex-direction: column-reverse`, so a scroll offset of zero is the bottom
  and follows new output; once scrolled up, native scroll anchoring holds the
  view. Returning to the bottom is setting the offset back to zero, and
  "scrolled up" is an offset below zero.
- `cmd/ox/web/src/SessionView.tsx` — `SessionView` renders `.log`
  (`role="log"`, "Transcript"), the composer's `send` submit handler, and the
  pending approvals through `ApprovalRequest`. `keepRowInPlace` already writes
  the scroller's `scrollTop`. `keepPromptFocus` keeps focus in the prompt when
  a button is pressed.
- `cmd/ox/web/src/ApprovalRequest.tsx` — `PermissionCard.submit` (Allow, Deny,
  Other) and `QuestionForm.send` (Submit, Decline) each call
  `props.control.approve(props.approval.id, response)`. These are the two
  places an answer leaves the page.
- `cmd/ox/web/src/NewSessionView.tsx` — a new session's `.log` holds at most the
  submitted first prompt; it gets no button.
- `cmd/ox/web/e2e/session.spec.ts` (~line 590–640) — `overflowing`,
  `sendAndWait`, and the four following-output tests.
- `internal/fake/fake.go` — a prompt `wait <ms>` keeps the turn running that
  long before the reply, which lets a spec scroll up while output is still to
  come.

## Decisions

- **Detecting "scrolled up".** `SessionView` holds a signal `atBottom`, set
  from the scroller's own `onScroll` as `event.currentTarget.scrollTop > -1`.
  The scroll offset is the scroller's state, written by the user's scrolling
  and by `keepRowInPlace`; it is not a viewport measurement, so the rule in
  `cmd/ox/web/AGENTS.md` that the script reads no viewport measurements holds.
  The tolerance of one pixel absorbs fractional offsets at fractional device
  pixel ratios. Scroll anchoring moves the offset only while it is already
  below zero, so `atBottom` changes only when the user scrolls or the page
  returns to the bottom.
- **Returning to the bottom.** One function in `SessionView`, `toBottom()`,
  sets the scroller's `scrollTop` to zero, instantly. The scroll event it
  fires sets `atBottom`, which hides the button. A smooth scroll is ruled out:
  Chrome fixes a smooth scroll's target when it starts, so output that arrives
  during it leaves the transcript short of the bottom.
- **The button.** `<button type="button" class="icon arrow-down jump"
  aria-label="Jump to bottom" title="Jump to bottom">`, rendered under
  `<Show when={!atBottom()}>`, with `onClick={toBottom}` and
  `onPointerDown={keepPromptFocus}` so pressing it leaves focus, and a phone's
  keyboard, where they were. Icon: `lucide-static/icons/arrow-down.svg`. It
  floats over the transcript, so `SessionView` wraps `.log` and the button in
  `<div class="log-frame">` (`position: relative`, taking `.log`'s place in
  the column), and the button sits at `position: absolute; bottom: 1rem; left:
  50%; translate: -50% 0`, round, with the card background, border, and
  `--shadow-sm`, as chat apps draw it. The button is outside `role="log"`, so
  it is not transcript content. The sibling rule
  `.log ~ [role="alert"], .log ~ [role="status"]` becomes
  `:is(.log, .log-frame) ~ …`, keeping `NewSessionView`'s unwrapped `.log`.
- **Send returns to the bottom.** `SessionView`'s `send` handler calls
  `toBottom()` after `control.send()`. An empty composer never submits, since
  the browser's form validation stops it.
- **Answers return to the bottom.** `ApprovalRequest` takes an `answer:
  (response: ApprovalResponse) => void` prop in place of calling
  `control.approve` itself; `SessionView` passes
  `(response) => { control.approve(approval.id, response); toBottom(); }`.
  `PermissionCard.submit` and `QuestionForm.send` call `props.answer`. Every
  answer, including Deny, Other, and Decline, returns to the bottom. `control`
  stays a prop for the drafts.
- **Requirements.** *Following output* changes and *Jump to bottom* is added
  (see Requirement changes). The scroll state is the view's own and nothing
  else reads it: a reconnect or a second page leaves it alone; leaving the
  session and coming back builds a new view at the bottom with the button
  hidden, as today. A send or answer that `oxd` refuses has already scrolled,
  which is the result the user expected when they acted; the error shows as it
  does today.
- **Ownership.** `atBottom` is owned by `SessionView` and lives as long as the
  view. It mirrors the scroller's offset, which only the scroll event reports,
  so there is no second source to keep in sync. No cleanup.

## Check your assumptions

1. **The transcript follows again after you send or answer.**
   - **Plan:** Old text: "**Following output.** The transcript follows new
     output while it is scrolled to its bottom." New text: "**Following
     output.** The transcript follows new output while it is scrolled to its
     bottom. Sending a prompt, or answering an approval or a question, returns
     it to its bottom." Every answer counts, including Deny and Decline.
   - **Alternative:** Return to the bottom only on Allow and Submit, leaving
     the view where it was after a refusal; the user would scroll back by hand
     to see Claude's response to the refusal.
2. **A button returns a scrolled-up transcript to its bottom.**
   - **Plan:** New requirement: "**Jump to bottom.** While the transcript is
     scrolled up from its bottom, a button over it returns it to its bottom,
     where it follows new output again." The jump is instant.

## Test plan

All in `cmd/ox/web/e2e/session.spec.ts`, beside the following-output tests,
using `overflowing`, `transcript(page).hover()` with `page.mouse.wheel(0,
-500)` to scroll up, and the "Jump to bottom" button found by role and name.

- **Replace** "output does not follow while scrolled up": it sends while
  scrolled up, which now returns to the bottom. New form: send `wait 3000`,
  scroll up, and expect the reply `You said: wait 3000` once it arrives to be
  out of the viewport.
- **Replace** "scrolling back to the bottom resumes following": its send now
  returns to the bottom on its own. New form: send `wait 3000`, scroll up,
  wheel back down, and expect the reply in the viewport.
- **New** "Jump to bottom shows only while scrolled up": hidden at the bottom,
  visible after scrolling up, hidden again after wheeling back down.
- **New** "Jump to bottom returns to the bottom and output follows again":
  send `wait 3000`, scroll up, press Jump to bottom, expect the last prompt in
  the viewport and the button hidden, then the reply in the viewport once it
  arrives.
- **New** "sending while scrolled up returns to the bottom": scroll up,
  `sendAndWait`, expect the last reply in the viewport. Fails before the
  change.
- **New** "answering an approval while scrolled up returns to the bottom":
  `overflowing`, `ask(page, "Bash", …)`, scroll up, `allow`, expect the last
  reply in the viewport.
- **New** "answering a question while scrolled up returns to the bottom":
  `overflowing`, `ask(page, "AskUserQuestion", questions)`, scroll up, answer
  both questions and Submit, expect the last reply in the viewport. Covers
  `QuestionForm`, the second place an answer leaves the page.

## Implementation plan

1. `cmd/ox/web/src/ApprovalRequest.tsx`: add the `answer` prop to `Props`;
   `PermissionCard.submit` and `QuestionForm.send` call it instead of
   `props.control.approve`. Update the component comment.
2. `cmd/ox/web/src/SessionView.tsx`: add the `log` ref, `atBottom` signal,
   `onScroll`, and `toBottom()`; wrap `.log` and the button in `.log-frame`;
   call `toBottom()` from `send`; pass
   `answer` to each `ApprovalRequest`.
3. `cmd/ox/web/src/style.css`: add `.arrow-down` to the icon list, the
   `.log-frame` and `.jump` rules, and change the `.log ~ […]` rule to
   `:is(.log, .log-frame) ~ […]`.
4. `cmd/ox/web/e2e/session.spec.ts`: the tests above.
5. `docs/functional-requirements.md`: the approved requirement text.

## Requirement changes

- **Changed:** **Following output.** Old: "The transcript follows new output
  while it is scrolled to its bottom." New: "The transcript follows new output
  while it is scrolled to its bottom. Sending a prompt, or answering an
  approval or a question, returns it to its bottom."
- **Added:** **Jump to bottom.** While the transcript is scrolled up from its
  bottom, a button over it returns it to its bottom, where it follows new
  output again.
