# Review: question form without raw input, with its buttons in view

## Scope and coverage

Uncommitted changes in the `question-form` worktree on base `a38aced` (HEAD, `git merge-base origin/main HEAD`): `cmd/ox/web/src/ApprovalRequest.tsx`, `cmd/ox/web/src/style.css`, and `cmd/ox/web/e2e/session.spec.ts`, against `agents/plans/2026-11-24-question-form.md`, with the user's approval that permission cards keep Submit in view. The change resolves #55.

Gaps:

- `/ox-work`'s final response was not passed with the request, so its Tests proved and Checks were not available. The checks below were run by this review.
- The fake harness holds one pending approval at a time, so the two-approval layout was checked by cloning the pending card in a temporary Playwright probe, since removed.

## Summary

| ID | Severity | Category    | Action | Status   | Title                                              |
| -- | -------- | ----------- | ------ | -------- | -------------------------------------------------- |
| F1 | Medium   | Correctness | Fix    | Resolved | The buttons' cover hides the next approval's title |

## F1. The buttons' cover hides the next approval's title

Medium · Correctness · Fix · Resolved · `cmd/ox/web/src/style.css:362`

`.approval .actions` fills the strip beneath it with `box-shadow: 0 2rem 0 var(--card)`. The shadow paints whether or not the row is stuck. With two approvals pending, as when Claude makes parallel tool calls, the first card's row sits at its natural place above the second card, and its shadow covers the 1rem gap and the top 1rem of the second card, hiding that card's title ("Allow Bash?", "Claude asks"). The plan asks for this layout to be checked with two approvals pending.

The cover also depends on the row being exactly 2rem tall; a shorter row would leave a gap between the shadow and the row.

### Evidence

A temporary spec at 1280 × 1000 asked for a `Bash` approval and cloned the card into `.pending` as a second approval. The first row measured 634–666px, its shadow 666–698px, and the second card's title 682–703px: the screenshot shows only the title's descenders. At 390 × 700, with a long question, the stuck row sits at 554–586px and the composer's box starts at 602px, so the strip the shadow covers is the 16px between them.

### Action

Make the cover part of the row's own box, so it never reaches past the row into the next card: replace the shadow with `padding-bottom: 1rem; margin-bottom: -1rem; bottom: -1rem` on `.approval .actions`. The negative margin keeps the card's layout unchanged, the padding falls in the gap between cards, and the stuck row's buttons rest where they rest now (probe: buttons at 586px in both versions, the row's background reaching the box at 602px), with the next card's title uncovered (row ends at 682px, title starts at 682px). Update the comment above the rule to describe it.

No test can observe the overlap without asserting styling, and the fake harness cannot hold two approvals, so check it in headless Chrome with two cards pending, as the plan's step 4 requires, and keep the existing "a long question keeps its buttons in view" and "a long permission card keeps Submit in view" tests passing.

## Checks run

- `make test`: passed.
- `make e2e`: 163 passed, 4 skipped.
- The three new tests against the base source: all three fail. "a long question keeps its buttons in view" fails there on a strict-mode violation, because the base's `<details>` also has the `group` role; with only `style.css` reverted it fails on Decline not being in the viewport, and "a long permission card keeps Submit in view" fails on Submit.
- "a question form shows no raw input" fails with only `ApprovalRequest.tsx` reverted.
- `docs/functional-requirements.md` is unchanged, as the plan's Requirement changes (None) require.
- `make live` is not required: the change does not alter how Ox drives Claude Code. The page's requests to `oxd` are unchanged, so the `make dev` check is not required either.

## Verification 1

Uncommitted tree on base `a38aced`, with `.approval .actions` now `bottom: -1rem; margin-bottom: -1rem; padding-bottom: 1rem` and no shadow.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | A temporary two-card probe (cloned pending card) shows the first row ending at the second card's title (634–682px and 682–703px at 1280 × 1000; 484–532px and 532–553px at 390 × 700), the title fully visible in the screenshot. With the shadow, the same probe hid the title's top 16px. A long question at 390 × 700 keeps its buttons at 586px with the row's background reaching the composer's box at 602px, both mid-scroll and at the end. No kept test: the fake harness holds one approval, and the overlap is visible only as styling. |

No new findings.

Checks: `make test` passed; `make e2e` 163 passed, 4 skipped. The probe spec was removed.

## Verdict

`Ready to commit`: F1 is resolved.
