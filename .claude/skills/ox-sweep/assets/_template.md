# Sweep title

Delete placeholder text and unused sections, including this guidance. Give every finding an ID: `Plan` and `Record` findings first, then `Rejected`, each group by severity, highest first: `F1`, `F2`, and so on. Each `Plan` and `Record` finding gets its own section. `Rejected` findings with a short reason can share a section that lists each with its reason. Do not nest findings under severity, category, or action headings.

Severity describes the cost of leaving the finding as it is, and orders the work. The action is decided separately.

- `High`: a broken requirement, wrong results, lost work, a process running with settings other than those shown, or a security hole that normal use can trigger.
- `Medium`: wrong behavior that only an uncommon but realistic case triggers, or a test or document that misleads whoever relies on it.
- `Low`: an unlikely case, or dead code with a concrete cost to keep.

Categories: `Requirements`, `Ownership`, `Settling`, `Settings`, `Process I/O`, `Claude Code`, `Security`, `Correctness`, `Testing`, `Documentation`, `Simplicity`, as `/ox-review` defines them. Dead code is `Simplicity`.

Actions: `Plan`, `Record`, `Rejected`, as the skill defines them.

## Scope and evidence

The revision and working tree swept, and the earlier sweeps, audits, and fixed defects read.

Children, with the workflows and requirements, the input source, or the tests and docs each covered:

1. Child: workflows; requirements.

Gaps:

- Each material gap in coverage, including workflows, inputs, or requirements left out and checks that could not run.

## Summary

Nothing was fixed.

| ID | Severity | Category     | Action   | Title                                  |
| -- | -------- | ------------ | -------- | -------------------------------------- |
| F1 | High     | Requirements | Plan     | Reconnect sends the first prompt again |
| F2 | Low      | Settling     | Rejected | A stalled reader holds its connection  |

If there are no findings, replace the table with `No findings warrant a change.` and omit the finding sections.

## Changes

| Change    | Findings | Owner                   | Issue |
| --------- | -------- | ----------------------- | ----- |
| Reconnect | F1       | `cmd/ox/web/src/ox.tsx` | #N    |

The change to take up first, and why.

## F1. Reconnect sends the first prompt again

High · Requirements · Plan

What happens, when, and what it affects, in short paragraphs. Cite the functional requirement by title when one applies.

### Places

- `path/to/file.go:42`: what this place does.

### Evidence

How you confirmed it: the trace, reproduction, probe, or test output, and whether it was observed live, reproduced, or traced in the source.

### Origin

The commit that introduced the defect, and that change's review in `agents/reviews/` when there is one, or `Unknown`.

### Action

For `Plan`, the change at the cause, and whether it may change what the user sees. For `Record`, what the `bug` issue waits on. For `Rejected`, the reason. Name the `bug` issue opened for a `Plan` or `Record` finding.

## Checks run

- Each check you or a child ran and its result.
- Each check something outside your control blocked, and what blocked it.
