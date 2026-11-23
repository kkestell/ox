# Wide Markdown tables scroll inside their own box

## Scope and coverage

Reviewed the uncommitted changes to `cmd/ox/web/src/style.css` and `cmd/ox/web/e2e/session.spec.ts` on branch `transcript-horizontal-scroll`, base `b5419ba` (`git merge-base origin/main HEAD`, also `HEAD`), against `agents/plans/2026-11-22-transcript-horizontal-scroll.md`, which resolves #8. The product question was settled as the plan recommends: a table that cannot fit scrolls sideways inside its own box.

Gaps:

- No `/ox-work` final response came with the request, so its Tests proved and Checks were not available. The new test was run without the change, and `make test` and `make e2e` were run on the final tree, in this review.
- The suite runs Chromium only. Touch drags on a table and iOS Safari behavior stay unconfirmed, as the plan's Test plan states.

## Summary

| ID | Severity | Category   | Action   | Status | Title                                           |
| -- | -------- | ---------- | -------- | ------ | ----------------------------------------------- |
| F1 | Low      | Simplicity | Fix      | Resolved | `max-width: 100%` on the table has no effect    |
| F2 | Low      | Testing    | Rejected | —      | The test table puts a path in both columns      |

## F1. `max-width: 100%` on the table has no effect

Low · Simplicity · Fix · Resolved · `cmd/ox/web/src/style.css:253`

With `display: block`, the table box takes the full width of its container, so `max-width: 100%` never limits it. Every container of `.markdown` is a block or a stretched flex item: the reply article in `.transcript`, `.card > .plan` in a tool call, and `.approval .plan`, a column flex item whose `overflow-y: auto` already zeroes its automatic minimum width. None of them is a shrink-to-fit context where the declaration could matter. The plan's Fix lists the declaration, but the behavior it describes needs only `display: block` and `overflow-x: auto`. Without it, the rule matches `.markdown pre`, which scrolls the same way with `overflow-x: auto` alone.

### Evidence

With the rule reduced to `display: block; overflow-x: auto; border-collapse: collapse;`, the new e2e test passes. Removing `display: block` instead fails it (table viewport ratio 0.587), and removing `overflow-x: auto` fails it (transcript overflow 254 px). Both remaining declarations are therefore needed and protected; `max-width: 100%` is not.

### Action

Change the rule to `.markdown table { display: block; overflow-x: auto; border-collapse: collapse; }`. The existing test `a wide table scrolls inside its reply instead of the transcript` keeps guarding the behavior, and needs no change.

## F2. The test table puts a path in both columns

Low · Testing · Rejected · `cmd/ox/web/e2e/session.spec.ts:1233`

The plan's test table held a title and a path per row. The implementer put the same inline-code path in both columns, because the planned table wrapped its title column and fit at 390 px.

### Action

Rejected: the change is right. A table that fits at 390 px passes against the unchanged `style.css`, so the planned test would not have protected the fix. With a path in both columns, the table's natural width exceeds the screen and the test fails without the change: the table sits at viewport ratio 0.587, which matches the plan's estimate of a right edge near 600 px. The `Title` header over a path costs nothing.

## Checks run

- New test against the unchanged `.markdown table { border-collapse: collapse; }`: fails, `toBeInViewport` ratio 0.587.
- Removal spot checks on the new rule: without `display: block`, fails (ratio 0.587); without `overflow-x: auto`, fails (transcript `scrollWidth - clientWidth` 254); without `max-width: 100%`, passes (F1).
- `make test`: passed.
- `make e2e`: 118 passed, 4 skipped.
- `make live` and a headless Chrome check through `make dev` are not required: the change is CSS only and does not change how Ox drives Claude Code or how the page reaches `oxd`.

## Verification 1

Checked the uncommitted tree on base `b5419ba`. The only change since the review is the `.markdown table` rule, now `display: block; overflow-x: auto; border-collapse: collapse;`.

| ID | Status   | Evidence                                                                                                                                                            |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `max-width: 100%` removed. `a wide table scrolls inside its reply instead of the transcript` passes, and fails against the original rule (viewport ratio 0.587). |

No new findings.

Checks: the new test passes on the final tree and fails against the original rule; `make test` passed; `make e2e`: 118 passed, 4 skipped.

## Verdict

Ready to commit.
