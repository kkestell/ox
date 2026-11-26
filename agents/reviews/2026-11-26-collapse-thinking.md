# Review: collapse thinking behind a Thinking row

## Scope and coverage

Uncommitted changes on `collapse-thinking` against base `origin/main`, checked
against `agents/plans/2026-11-26-collapse-thinking.md`. Web client only, so
reviewed without child sessions.

Gaps:

- No `/ox-work` final response was supplied, so the new test's failure on the
  old code was judged from the diff (the old markup has no `group` role named
  Thinking) and not by running it.

## Summary

| ID | Severity | Category      | Action | Status | Title                                           |
| -- | -------- | ------------- | ------ | ------ | ----------------------------------------------- |
| F1 | Low      | Documentation | Fix    | Resolved | Reasoning requirement is not dprint-formatted   |
| F2 | Low      | Correctness   | Fix    | Resolved | Closed Thinking row does not pack with the next row |

## F1. Reasoning requirement is not dprint-formatted

Low · Documentation · Fix · `docs/functional-requirements.md:123`

The new Reasoning text matches the plan's wording but wraps at shorter lines
than dprint produces. The pre-commit hook refuses a commit whose documents are
unformatted.

### Evidence

`make docs` rewrites lines 123-126 (reflowed to 80 columns); the diff against
the working copy is only line wrapping.

### Action

Run `make docs`. The wording stays as approved. No test.

## F2. Closed Thinking row does not pack with the next row

Low · Correctness · Fix · `cmd/ox/web/src/style.css:305`

The plan says adjacent collapsed rows of either kind pack the same way. The
tighter-spacing rule is `details.tool:not([open]) + details:is(.tool, .reasoning)`,
whose left side matches only tool calls. After a closed Thinking row, the next
row (tool call or Thinking row) gets `margin-top: -0.25rem` instead of
`-0.75rem`, so those pairs sit 0.5rem looser than two tool calls.

### Evidence

Selector read: `details.reasoning:not([open]) + details` matches neither the
`+` rule nor any other; only the base `margin-top: -0.25rem` applies. Thinking
rows often precede tool calls, so the case is common.

### Action

Change the left side to `details:is(.tool, .reasoning):not([open]) +
details:is(.tool, .reasoning)`. No e2e test: the spec rules forbid asserting
spacing, so the user judges it in the session view.

## Checks run

- `npx playwright test e2e/session.spec.ts`: 135 passed, including the
  updated reasoning test.
- `npm run lint`: no errors (warnings only).
- Intent: the requirement text equals the plan's New text word for word;
  nothing else in the requirements changed.
- Row markup, shared CSS, `keepRowInPlace` comment, and `brain.svg` (present in
  `lucide-static`) match the plan's Decisions.

## Verdict

Ready to commit.

## Verification 1

Working tree on base `a54d917`. Checks: `dprint check` on the requirements
file, `biome check` (warnings only), the reasoning e2e test (passes).

| ID | Status   | Evidence                                                                                           |
| -- | -------- | -------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `dprint check docs/functional-requirements.md` passes; wording unchanged from the plan's New text |
| F2 | Resolved | `style.css:305` now matches `details:is(.tool, .reasoning):not([open]) + details:is(.tool, .reasoning)` |

No new findings.
