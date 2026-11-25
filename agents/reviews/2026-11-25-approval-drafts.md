# Review: keep approval answers in the session control

## Scope and coverage

Reviewed the uncommitted changes on branch `approval-drafts` against base
`5c3707dfb69bcf11f52a9823b7e0d627e2777445` (HEAD is the base; all changes are in
the working tree): `cmd/ox/web/src/ox.tsx`, `ApprovalRequest.tsx`,
`SessionView.tsx`, `cmd/ox/web/e2e/session.spec.ts`, `docs/architecture.md`, and
`docs/functional-requirements.md`. Checked against the plan
`agents/plans/2026-11-25-approval-drafts.md`, which resolves #107. The user
approved the plan's **Unsent text** requirement change.

The change delivers the plan's Goal and keeps its Decisions: drafts live in the
session control keyed by approval ID, one map per component kind, created at
component setup; `SessionView` renders only approvals not in `answering`;
`ApprovalRequest` takes the control and drops `hidden`; `endApproval` runs on
`approval_answered` and `approval_cancelled`, and `reconcileApprovals` runs in
`load` beside `reconcilePrompt`. The **Unsent text** edit in
`docs/functional-requirements.md` matches the plan's New text word for word, and
no other requirement changes. The `docs/architecture.md` edits match the plan's
Documentation updates and the code.

Lifecycle edges checked: the `session/approve` request in flight, refused in
view, refused after switching away, accepted (all three harnesses publish
`approval_answered` before replying, on one ordered queue per connection), and
cut off by a reconnect; `approval_answered` from this page and from a second
page; `approval_cancelled` on exit, idle, and Claude's cancel; reload and
reconnect through `load`; session destroyed or missing from a reload; switching
views. Inputs checked: approval IDs from Claude (`control_request` UUIDs), Ur,
the fake, and the scene; `request_id` on updates and snapshots; AskUserQuestion
input shapes and malformed input falling back to the card.

Issue #107 is resolved: a card's reply and a form's answers and shown question
survive switching to the draft and back, and a refusal after switching away
returns with its reply.

Gaps: none. `make live` and a `make dev` headless Chrome check cannot observe
the change: it does not change how Ox drives Claude Code or how the page reaches
`oxd`.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                  |
| -- | -------- | ------------ | -------- | ------ | ------------------------------------------------------ |
| F1 | Low      | Conventions  | Fix      | Resolved | The approval ID has two parameter names in the control |
| F2 | Low      | Requirements | Rejected | —      | A refused card comes back with its input collapsed     |
| F3 | Low      | Testing      | Rejected | —      | No test fails without the draft cleanup                |
| F4 | Low      | Construction | Rejected | —      | `CardDraft` keeps `reply` beside an optional `choice`  |

## F1. The approval ID has two parameter names in the control

Low · Conventions · Fix · `cmd/ox/web/src/ox.tsx:388`

The session control names the same approval ID `approvalId` in the new
`cardDraft` and `formDraft` (`ox.tsx:388`, `ox.tsx:398`) and `requestId` in
`endApproval` (`ox.tsx:298`) and the existing `approve`. The protocol's name is
`request_id`.

### Evidence

Read from the source.

### Action

Rename the `approvalId` parameters of `cardDraft` and `formDraft` to
`requestId`, and update their comments if they name it. No test: the rename
changes no behavior.

## F2. A refused card comes back with its input collapsed

Low · Requirements · Rejected · `cmd/ox/web/src/SessionView.tsx:65`

An approval being answered now unmounts and remounts from its draft if `oxd`
refuses the answer, so view state outside the draft, such as an expanded
`<details class="input">` (`ApprovalRequest.tsx:80`), comes back closed. The
base kept it mounted and hidden, so the expansion survived.

### Evidence

A temporary e2e probe in two concern reviewers expanded the input, held
`session/approve`, clicked Allow, and failed the held request. Allow returned
checked; the input was closed. The probe passed on the base.

### Action

Rejected: the plan's "One mechanism for refusals" decision chose to unmount and
restore from the draft. The expansion is not an answer, no requirement promises
it, and switching sessions already collapses it, before and after this change.
The answer itself, the choice and reply, returns as **Allow and Deny** and
**Actions show at once** require.

## F3. No test fails without the draft cleanup

Low · Testing · Rejected · `cmd/ox/web/src/ox.tsx:729`

Removing the `endApproval` call in `receive` (`ox.tsx:729`), or the
`reconcileApprovals` call in `load` (`ox.tsx:673`), fails no e2e test.

### Evidence

Each call was removed in turn in a scratch copy of the worktree, and the e2e
suite passed apart from an unrelated probe.

### Action

Rejected: approval IDs are never reused within a session, so a leftover draft is
never read again and the cleanup has no effect the page can show. A test would
have to inspect the control's maps, which `AGENTS.md` ("Verification") rules
out. The cleanup is the plan's Construction decision, bounding each draft's life
to its approval's, and follows the `finishPrompt`/`reconcilePrompt` pattern.

## F4. `CardDraft` keeps `reply` beside an optional `choice`

Low · Construction · Rejected · `cmd/ox/web/src/ox.tsx:196`

`CardDraft = { choice?: "allow" | "deny" | "other"; reply: string }` permits a
non-empty `reply` with `choice` Allow or Deny. `pick` and the reply field's
`onInput` keep that from happening.

### Evidence

Read from `ApprovalRequest.tsx`: picking Allow or Deny clears the reply, and
typing picks Other.

### Action

Rejected: the plan's Shape decision fixes this type. `reply` is the value of the
Other text field, which the card shows in every state, so it is valid whatever
the choice. One component writes the draft, in two handlers, so the rule is
decided in one place.

## Checks run

- `make test` on the final tree: Go and Node suites passed.
- `make e2e` on the final tree: 201 passed, 5 skipped.
- `tsc` and biome on the changed files: clean.
- The three new e2e tests, with the base `ApprovalRequest.tsx`, `SessionView.tsx`,
  and `ox.tsx`: each failed at its own assertion (`session.spec.ts:781` reply
  value, `:790` reply value after refusal, `:1082` "2 of 2" not visible).
- Spot-checks: removing `endApproval` in `receive` and `reconcileApprovals` in
  `load` each left the suite passing (F3).
- Refusal with an expanded input: probe passes on the base and fails on the
  change (F2).
- `make live`: not run; the change does not alter how Ox drives Claude Code.
- `make dev` headless Chrome check: not run; the change does not alter how the
  page reaches `oxd`.

## Verification 1

Checked the uncommitted working tree on base `5c3707d` after the F1 fix. The
only change since the review renames the `approvalId` parameters of `cardDraft`
and `formDraft` to `requestId`; the diff is otherwise as reviewed. `tsc`, biome
on the changed files, and the 20 approval and question e2e specs in
`session.spec.ts` pass.

| ID | Status   | Evidence                                                                                    |
| -- | -------- | ------------------------------------------------------------------------------------------- |
| F1 | Resolved | `cardDraft` and `formDraft` take `requestId` (`ox.tsx:388`, `ox.tsx:398`); no `approvalId` remains |

No new findings.

## Verdict

Ready to commit. F1 is resolved; F2, F3, and F4 are rejected.
