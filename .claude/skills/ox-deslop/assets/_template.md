# Audit title

Delete placeholder text and unused sections, including this guidance. Give every finding an ID: `Plan` and `Record` findings first, then `Rejected`, each group by severity, highest first: `F1`, `F2`, and so on. Each `Plan` and `Record` finding gets its own section. `Rejected` findings with a short reason can share a section that lists each with its reason. Do not nest findings under severity, lens, or action headings.

Severity describes the cost of leaving the finding as it is, and orders the work. The action is decided separately.

- `High`: a broken requirement, wrong results, lost work, a process running with settings other than those shown, or a security hole that normal use can trigger.
- `Medium`: wrong behavior that only an uncommon but realistic case triggers, a test or document that misleads whoever relies on it, or code that makes future changes materially riskier or more expensive.
- `Low`: an unlikely case, or local cleanup with a concrete benefit.

Lenses: `Dead code`, `Duplication`, `Broken guarantees`, `Over-engineering`, `Hollow tests`, `Comments and docs`, as the skill defines them.

Origin: `Introduced`, `Worsened`, or `Present at baseline`, relative to the baseline named below, or `Unknown`.

Actions: `Plan`, `Record`, `Rejected`, as the skill defines them.

## Scope and evidence

The revision and working tree audited, the baseline, and the earlier audits and reviews read.

Areas, one child each, with the files and workflows it covered:

1. Area: files; workflows.

Gaps:

- Each material gap in coverage, including areas or files left out and checks that could not run.

## Summary

Nothing was fixed.

| ID | Severity | Lens              | Origin  | Action   | Title                                  |
| -- | -------- | ----------------- | ------- | -------- | -------------------------------------- |
| F1 | High     | Broken guarantees | Unknown | Plan     | Reconnect sends the first prompt again |
| F2 | Low      | Over-engineering  | Unknown | Rejected | Retry for a failure that cannot occur  |

If there are no findings, replace the table with `No findings warrant a change.` and omit the finding sections.

## Changes

| Change    | Findings | Owner                   | Issue |
| --------- | -------- | ----------------------- | ----- |
| Reconnect | F1       | `cmd/ox/web/src/ox.tsx` | #N    |

The change to take up first, and why.

## F1. Reconnect sends the first prompt again

High · Broken guarantees · Introduced · Plan

What happens, when, and what it affects, in short paragraphs. Cite the functional requirement by title when one applies. When several findings share a structure, name it.

### Places

- `path/to/file.go:42` — what this place does.

### Evidence

How you confirmed it: the trace, reproduction, probe, or test output, and whether it was observed live, reproduced, or traced in the source.

### Action

For `Plan`, the change at the cause, what it removes, and whether it may change what the user sees. For `Record`, what the `bug` issue waits on. For `Rejected`, the reason. Name the `bug` issue opened for a `Plan` or `Record` finding.

## Checks run

- Each check you or a child ran and its result.
- Each check something outside your control blocked, and what blocked it.
