# Review title

Delete placeholder text and unused sections, including this guidance. Give every finding an ID in severity order, most severe first: `F1`, `F2`, and so on. Each finding gets its own section. Do not put findings in bullets, and do not nest them under severity or category headings.

Severity describes the cost of leaving the finding as it is, and orders the work. The outcome is decided separately.

- `High`: a broken guarantee, wrong results, lost work, a process running with settings other than those shown, or a security hole that normal use can trigger.
- `Medium`: wrong behavior that only an uncommon but realistic case triggers, or code that makes future changes materially riskier or more expensive.
- `Low`: an unlikely case, or no effect on behavior and little effect on future changes.

Outcomes:

- `Fixed`: the review changed the code, tests, or documentation.
- `Rejected`: speculative, a misreading of the intended behavior, or cheaper to keep than to fix. The section states the reason.
- `Recorded`: a confirmed defect that cannot be fixed yet, written into `docs/known-issues.md`.

Categories: `Intent`, `Guarantees`, `Settings`, `Settling`, `Process I/O`, `Ownership`, `Claude Code`, `Security`, `Correctness`, `Testing`, `Simplicity`, `Conventions`, `Documentation`, as the skill defines them.

## Scope and coverage

What you reviewed, at which revision, and the plan you checked it against, if any.

Gaps:

- Each material gap in coverage.

## Summary

| ID | Severity | Category   | Outcome  | Title                                |
| -- | -------- | ---------- | -------- | ------------------------------------ |
| F1 | High     | Guarantees | Fixed    | Reconnect sends the first prompt again |
| F2 | Low      | Simplicity | Rejected | Retry for a failure that cannot occur  |

If there are no findings, replace the table with `No findings.` and omit the finding sections.

## F1. Reconnect sends the first prompt again

High · Guarantees · Fixed · `cmd/ox/web/src/ox.tsx:42`

What happens, when, and what it affects, in short paragraphs. Use a list only for a real enumeration, such as several locations.

### Evidence

How you confirmed it: the trace, reproduction, probe, or test output.

### Outcome

For Fixed, the change you made, the test that failed before it, and the decision the user made if the fix needed one. For Rejected, the reason. For Recorded, the `docs/known-issues.md` entry and what it waits on.

## Checks run

- Each check you ran and its result, including new tests run against the base revision and each removed condition.
- Each check you could not run, such as `make live`, and why.

## Commit messages

One message per change, as the skill describes.

```
Subject in the imperative

Body in the style of git log.
```

## Verdict

Summarize the result, referring to findings by ID.
