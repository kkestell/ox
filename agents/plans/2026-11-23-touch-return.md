# Return starts a new line on a touch screen

## Goal

On a phone, Return in the composer sends the prompt, because `sendOnEnter`
sends on every Enter without Shift and a phone keyboard has no Shift+Return.
The user cannot write a newline and sends by accident. After the change, on a
touch screen Return inserts a newline and only Send sends, as the Claude and
ChatGPT phone apps do. On a computer, Enter still sends and Shift+Enter still
starts a new line.

## Related code

- `cmd/ox/web/src/SessionView.tsx:13` — `sendOnEnter`, the composer's keydown
  handler. It submits the form on Enter without Shift or IME composition.
- `cmd/ox/web/src/SessionView.tsx:119` and
  `cmd/ox/web/src/NewSessionView.tsx:79` — the two composers that use it.
- `cmd/ox/web/e2e/ui.ts:105-122` — `newSession` and `ask` send with
  `press("Enter")`. Both run inside the touch-emulated blocks
  (`session.spec.ts` "on a touch screen", `workspace.spec.ts` "on a phone").
- `cmd/ox/web/e2e/session.spec.ts:446` — "Shift+Enter starts a new line", the
  model for the new test.

## Decisions

- **Touch detection.** `sendOnEnter` reads `matchMedia("(pointer: coarse)")`
  on each keydown and, when it matches, returns without handling the key, so
  the textarea inserts the newline. A primary coarse pointer is a phone or
  tablet; a desktop, including a touchscreen laptop, has a fine primary
  pointer and keeps Enter-to-send. Reading the query at keydown keeps no
  state, and follows the device if its primary pointer changes. Probed in
  headless Chrome: Playwright's `hasTouch` makes `(pointer: coarse)` match,
  and a default context does not, so the e2e touch blocks observe the
  behavior.
- **Scope.** Only the two composer textareas change. The single-line inputs in
  `ApprovalRequest.tsx` and the model search in `SettingsFields.tsx` keep Enter
  as submit; a single-line field has no newline to protect, and Return is the
  phone keyboard's submit key there.
- **Requirements.** "Enter to send" changes as below. The change is local to
  the keydown handler, so "Only the user sends" and every send lifecycle case
  hold as before: on a touch screen fewer keystrokes send, and Send's own path
  is untouched.
- **Overlap with in-flight changes.**
  - `optimistic-ui` rewrites much of `SessionView.tsx` and
    `NewSessionView.tsx`, including the import line that names
    `sendOnEnter`, and edits `session.spec.ts` and `functional-requirements.md`
    (not "Enter to send"). This change edits only the body of `sendOnEnter`,
    so `sendOnEnter` keeps its name, signature, and module; whichever lands
    second rebases with at most adjacent-hunk conflicts.
  - `no-page-bounce` edits `e2e/ui.ts` just below `newSession` and `ask`
    (removing `openKeyboard` and friends) and deletes the touch-block test
    "pending approvals leave the composer above the keyboard", the one touch
    test that calls `ask`. The new test goes at the top of the
    "on a touch screen" block, away from that deletion.

## Check your assumptions

1. **On a phone or tablet, Return starts a new line and only Send sends.**
   - **Plan:** Changes "Enter to send" from "Enter sends and Shift+Enter starts
     a new line; Enter while composing with an input method does not send." to
     "On a computer, Enter sends and Shift+Enter starts a new line; Enter while
     composing with an input method does not send. On a phone or tablet,
     Return starts a new line and only Send sends."
   - **Alternative:** Keep Return-sends on tablets and change only phones,
     which leaves a tablet's on-screen keyboard with no way to type a newline.
2. **A tablet with a keyboard case counts as a touch screen.**
   - **Plan:** On an iPad or Android tablet, Return starts a new line even when
     a hardware keyboard is attached; the user taps Send to send.
   - **Alternative:** Enter-to-send whenever a hardware keyboard is attached.
     A browser cannot tell a hardware keyboard from an on-screen one, so this
     would need a guess that sometimes sends from the on-screen keyboard, the
     accident this change removes.

## Test plan

- Return on a touch screen inserts a newline and sends nothing: e2e, a new
  test "Return starts a new line" at the top of the "on a touch screen" block
  in `session.spec.ts`. It starts a session with `newSession`, fills
  "first line", presses Enter, types "second line", and asserts the prompt's
  value is "first line\nsecond line" and the transcript's prompts are still
  only the first. It fails before the change, when Enter sends. No existing
  touch test presses Enter in the composer.
- Enter sends on a computer: covered by the existing desktop specs that press
  Enter in the composer (`session.spec.ts:61` and others), and Shift+Enter and
  IME composition by `session.spec.ts:446` and `:455`, unchanged.
- `newSession` and `ask` in `e2e/ui.ts` click Send instead of pressing Enter,
  so they send on every device the specs emulate.

## Implementation plan

1. `cmd/ox/web/src/SessionView.tsx`: in `sendOnEnter`, leave the key to the
   textarea when `matchMedia("(pointer: coarse)").matches`, with a one-line
   comment that a phone keyboard has no Shift+Return, so Return there starts a
   new line and Send sends.
2. `cmd/ox/web/e2e/ui.ts`: `newSession` and `ask` send with
   `send(page).click()` instead of `prompt(page).press("Enter")`.
3. `cmd/ox/web/e2e/session.spec.ts`: add "Return starts a new line" to the
   "on a touch screen" block.
4. `docs/functional-requirements.md`: apply the requirement change below.

## Requirement changes

- **Changed:** **Enter to send.**
  - Old: "Enter sends and Shift+Enter starts a new line; Enter while composing
    with an input method does not send."
  - New: "On a computer, Enter sends and Shift+Enter starts a new line; Enter
    while composing with an input method does not send. On a phone or tablet,
    Return starts a new line and only Send sends."
