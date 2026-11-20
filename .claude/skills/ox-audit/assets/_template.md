# Audit title

Delete placeholder text and unused sections, including this guidance. Give every finding an ID: `Plan` findings first, then `Rejected`, each group by cost, highest first: `F1`, `F2`, and so on. Each finding gets its own section. Do not put findings in bullets, and do not nest them under cost or action headings.

Cost describes what keeping the structure costs, and orders the work. Choose the level by the size of the harm as well as its kind: how often normal use reaches it, how long it lasts, and what it costs the user. The action is decided separately.

- `High`: the structure has caused defects that broke a requirement, lost work, or ran a process with settings other than those shown, or normal use can still reach such a wrong state.
- `Medium`: the structure has caused defects in uncommon but realistic cases, or forces checks, guards, or syncing on every change that touches it.
- `Low`: the structure permits wrong states only in unlikely cases, and costs readers little.

Actions: `Plan`, `Rejected`, as the skill defines them.

## Scope and evidence

The revision and working tree audited, the history covered (since which revision or audit), and the earlier audits read.

Gaps:

- Each material gap in coverage.

## Summary

| ID | Cost | Action   | Title                                     | Places |
| -- | ---- | -------- | ----------------------------------------- | ------ |
| F1 | High | Plan     | Session state copied into each connection | 4      |
| F2 | Low  | Rejected | Two similar argument parsers              | 2      |

If there are no findings, replace the table with `No findings.` and omit the finding sections.

## F1. Session state copied into each connection

High · Plan

The structure, and the wrong states it lets the code build, in short paragraphs.

### Places

- `path/to/file.go:42` — what this place does with the state or rule.

### Defects

Each defect the structure caused, with its review finding, known issue, or commit, and whether its fix changed the structure or guarded the symptom. `None.` when the finding rests on a reachable wrong state alone.

### Evidence

How you confirmed the structure and its reachable wrong states: the trace, reproduction, or probe.

### Replacement

For `Plan`, the owner or representation that leaves no wrong state to build, what it removes, the cost of the change, and whether it may change what the user sees. For `Rejected`, the reason.

## Checks run

- Each check you ran and its result.
