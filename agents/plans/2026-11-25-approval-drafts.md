# Keep approval answers in the session control

## Goal

A permission card's choice and typed reply, and a question form's answers and
shown question, live in the approval components. The session view is keyed by
session, so switching to another session or a draft and back remounts them
blank, and an answer refused after the user switched away also returns blank.
When the change is done, each pending approval's unsent answers survive
switching views and reconnecting, and a refused answer returns as the user left
it from any view. Resolves #107, from F3 in
`agents/audits/2026-11-25-ox-sweep.md`.

## Related code

- `cmd/ox/web/src/ApprovalRequest.tsx` — `PermissionCard` holds `choice` and
  `reply` in signals (line 30); `QuestionForm` holds `answers` in a store and
  `page` in a signal (line 128). Both are lost on unmount.
- `cmd/ox/web/src/SessionView.tsx:113` — renders every pending approval and
  keeps one being answered mounted but hidden, so a refusal shows it as left.
  This works only while the view stays mounted.
- `cmd/ox/web/src/App.tsx:181` — the keyed `Match` that remounts the session
  view on every switch.
- `cmd/ox/web/src/ox.tsx:196` — `createSessionControl`, which outlives the view
  and already owns the composer text and the approvals in flight (`answering`,
  `approve`). `finishPrompt` and `reconcilePrompt` are the pattern for ending
  page state from an update and from a reloaded snapshot.
- `cmd/ox/web/src/ox.tsx:652` (`receive`) and `ox.tsx:580` (`load`) — where
  `approval_answered`, `approval_cancelled`, and reloaded snapshots arrive.
- `cmd/ox/web/e2e/session.spec.ts:757` and `:971`-`:1050` — the existing
  approval and question form specs, and `askQuestions`, `ask`, and `hold`.

## Decisions

- **Owner.** Unsent approval answers are the page's own fact, not `oxd`'s, so
  they go in the session control beside `answering`, keyed by approval ID, and
  not in the session snapshot that `reconcile` rewrites from `oxd`. Approval IDs
  are unique for the life of a session in all three harnesses (Claude's
  `control_request` IDs, and `rand.Text()` in Ur and the fake), so a key is
  never reused.
- **Shape.** The control keeps two maps, `cardDrafts` and `formDrafts`, each
  from approval ID to a Solid store and its setter, created on first request.
  Which component an approval gets is fixed by its tool, so each component asks
  for its own kind and reads a typed store with no narrowing. The types move to
  `ox.tsx`:
  - `CardDraft = { choice?: "allow" | "deny" | "other"; reply: string }`
  - `QuestionAnswer = { labels: string[]; other: boolean; text: string }` (today's
    `Answer`)
  - `FormDraft = { answers: QuestionAnswer[]; page: number }`

  The control exposes `cardDraft(approvalId)` and
  `formDraft(approvalId, questions: number)`, which return the existing store
  or create one with an empty answer per question and `page: 0`. Creating a
  store is not a reactive write, so the components call these once at setup.
- **One mechanism for refusals.** With the answers in the control, an approval
  being answered no longer needs to stay mounted. `SessionView` renders only the
  approvals not in `answering`, and shows the "Waiting on you" section only when
  one remains. `ApprovalRequest` loses its `hidden` prop and takes the control
  in place of `onAnswer`, calling `control.approve` itself. A refusal remounts
  the approval from its draft.
- **Requirements.** **Unsent text** (changed, see Requirement changes),
  **Questions from Claude**, **Allow and Deny**, and **One answer per
  approval** hold through the lifecycle:
  - In flight: the approval disappears at once; its draft stays. If `oxd`
    refuses, the approval returns with its draft and the error, in whichever
    view the user is in when it returns.
  - Reply lost or reconnect: the control survives; the reloaded snapshot either
    still holds the approval, which shows with its draft, or does not, and its
    draft is dropped.
  - Switching views and back: the view remounts and reads the draft.
  - A second page: each page keeps its own drafts. An answer from the other
    page ends the approval with `approval_answered`, which drops this page's
    draft.
  - Process exits, Claude cancels, or the session goes idle:
    `approval_cancelled` drops the draft.
  - Reload: the control is gone, so drafts are discarded, as unsent text is.
- **Construction.** A draft exists only while its approval is pending on this
  page, enforced by the control: `endApproval(requestId)` deletes both maps'
  entries for the ID, called from `receive` on `approval_answered` and
  `approval_cancelled`, and `reconcileApprovals(snapshot)` calls it for every
  draft whose ID is not in `snapshot.approvals`, called from `load` beside
  `reconcilePrompt`. Deleting a session's control on `session/destroyed`
  already drops all of its drafts.
- **Ownership.** New state: the two draft maps in the session control. Needed
  so approval answers survive switching views (**Unsent text**). Races: an
  `approval_answered` update for this page's own answer arrives before the
  `session/approve` reply, which drops the draft while `answering` still hides
  the approval; nothing reads the draft after that. A refusal arriving after
  the approval was cancelled shows nothing, since the snapshot no longer holds
  the approval. Cleanup: `endApproval` and `reconcileApprovals` as above. The
  commit body repeats this.

## Check your assumptions

1. **The Unsent text requirement names approval answers.**
   - **Plan:** The requirement promises that a half-answered approval, including
     the question a form is on, survives switching sessions and reconnecting,
     and is discarded on reload, as typed prompt text is. Choices and form
     answers are not "text", so the current wording leaves this unpromised.
     Old: "**Unsent text.** Unsent text, in a session or in a workspace's
     new-session draft, survives switching views and reconnecting. Reloading
     the page discards it, and removing a workspace discards its draft." New:
     given under Requirement changes.
   - **Alternative:** Leave the requirement as it is and fix the behavior
     anyway. The user loses nothing now, but a later change could drop the
     answers again without breaking a stated requirement.

## Test plan

- A question form keeps its answers and its shown question after switching to
  the new-session draft and back: e2e, new test in `session.spec.ts` beside
  "a form keeps its question and answers across a reconnect", using
  `askQuestions`. Answer Red, Next, check Appendix, click New session, return
  with the session's button (`sessions(page).getByRole("button", { name: /^approve AskUserQuestion/ })`),
  and assert "2 of 2", Appendix checked, and, after Previous, Red checked. No
  existing test leaves the view.
- A permission card keeps its typed reply after switching away and back: e2e,
  new test using `ask(page, "Bash", '{"command":"ls"}')`. Fill "Other response",
  switch to the draft and back, and assert the reply's value and that the
  Other radio is checked.
- A refused answer sent before switching away returns with its reply: e2e,
  new test. Type a reply, hold `session/approve`, submit, switch to the draft
  and back, fail the held request, and assert the alert and the reply's value.
- The existing "an answer hides its approval before oxd answers, and a refusal
  brings it back with the choice" test keeps covering the in-view refusal
  through the new unmount-and-restore path, unchanged.
- Each new test fails before the change, since the remounted components start
  blank.

## Implementation plan

1. `cmd/ox/web/src/ox.tsx`: add `CardDraft`, `QuestionAnswer`, and `FormDraft`;
   add the two maps, `cardDraft`, `formDraft`, `endApproval`, and
   `reconcileApprovals` to `createSessionControl`; update its doc comment to
   name each pending approval's unsent answers. In `receive`, call
   `controls.get(id)?.endApproval(params.request_id)` for `approval_answered`
   and `approval_cancelled` beside `finishPrompt`. In `load`, call
   `reconcileApprovals(snapshot)` beside `reconcilePrompt`.
2. `cmd/ox/web/src/ApprovalRequest.tsx`: change `Props` to
   `{ approval: Approval; control: SessionControl }`. `PermissionCard` reads
   and writes `choice` and `reply` through `control.cardDraft(approval.id)`;
   `QuestionForm` reads and writes `answers` and `page` through
   `control.formDraft(approval.id, questions.length)`. Both answer with
   `props.control.approve(props.approval.id, response)`. Drop the `Choice` and
   `Answer` types and the `hidden` attribute, and update the component comment.
3. `cmd/ox/web/src/SessionView.tsx`: render only the approvals not in
   `control.answering()`, show the section only when one remains, pass
   `control`, and replace the comment about staying mounted.
4. `cmd/ox/web/e2e/session.spec.ts`: add the three tests.
5. `docs/functional-requirements.md`: apply the approved **Unsent text** text.
6. `docs/architecture.md`: under "Session control", add each pending approval's
   unsent answers to what the control owns, and replace "an answered approval
   hides" with "an approval being answered disappears and returns with its
   answers if refused".

## Requirement changes

- **Changed:** **Unsent text.**
  - Old: "Unsent text, in a session or in a workspace's new-session draft,
    survives switching views and reconnecting. Reloading the page discards it,
    and removing a workspace discards its draft."
  - New: "Unsent text, in a session or in a workspace's new-session draft, and
    an approval's unsent answers survive switching views and reconnecting; a
    question form stays on the question the user left it on. Reloading the page
    discards them, and removing a workspace discards its draft."

## Documentation updates

- `docs/architecture.md`, "Session control, drafts, and optimistic state": what
  the session control owns, and how an answered approval shows while in flight.
