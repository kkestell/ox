# Review title

Delete placeholder text and unused sections, including this guidance. Give every finding an ID in severity order, most severe first: `F1`, `F2`, and so on; findings added during verification take the next IDs. Each finding gets its own section. Do not put findings in bullets, and do not nest them under severity or category headings.

Severity describes the cost of leaving the finding as it is, and orders the work. The action is decided separately.

- `High`: a broken requirement, wrong results, lost work, a process running with settings other than those shown, or a security hole that normal use can trigger.
- `Medium`: wrong behavior that only an uncommon but realistic case triggers, or code that makes future changes materially riskier or more expensive.
- `Low`: an unlikely case, or no effect on behavior and little effect on future changes.

Actions: `Fix`, `Record`, `Rejected`, as the skill defines them. Status: `Open` or `Resolved` for `Fix` and `Record`; `—` for `Rejected`.

Categories: `Intent`, `Requirements`, `Settings`, `Settling`, `Process I/O`, `Construction`, `Ownership`, `Claude Code`, `Security`, `Correctness`, `Testing`, `Simplicity`, `Conventions`, `Documentation`, as the skill defines them.

## Scope and coverage

What you reviewed: the base revision, the working tree state, and the plan you checked it against, if any.

Gaps:

- Each material gap in coverage.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                  |
| -- | -------- | ------------ | -------- | ------ | -------------------------------------- |
| F1 | High     | Requirements | Fix      | Open   | Reconnect sends the first prompt again |
| F2 | Low      | Simplicity   | Rejected | —      | Retry for a failure that cannot occur  |

If there are no findings, replace the table with `No findings.` and omit the finding sections.

## F1. Reconnect sends the first prompt again

High · Requirements · Fix · `cmd/ox/web/src/ox.tsx:42`

What happens, when, and what it affects, in short paragraphs. Use a list only for a real enumeration, such as several locations.

### Evidence

How you confirmed it: the trace, reproduction, probe, or test output.

### Action

For `Fix`, the change to make at the cause, and the test to keep: its layer, the behavior it guards, and the existing test that should carry it. For `Record`, the `bug` issue to open: worst cost, how it was confirmed, and what it waits on. When the fix needed a product decision, the user's answer. For `Rejected`, the reason.

## Checks run

- Each check you ran and its result, including new tests run without the change and each spot-checked removal.
- Each check something outside your control blocked, and what blocked it.

## Verification 1

The revision checked, and the checks run.

| ID | Status   | Evidence                                              |
| -- | -------- | ----------------------------------------------------- |
| F1 | Resolved | `TestReconnectKeepsFirstPrompt` fails without the fix |

New findings, each in its own section above with the next ID.

## Verdict

`Fixes needed` or `Ready to commit`, referring to findings by ID. Update it after each verification round.
