# Repeat the skill invocation across later turns

## Plan

`agents/plans/2026-10-01-007-repeat-the-skill-invocation-across-turns.md`

## Summary

Model requests now repeat the latest skill invocation after the summary whenever
the summary covers it, across later user messages, until another skill
invocation begins. The plan's goal is met.

## Departures from the plan

None.

## Automated checks

- `make check` — Passed.

## Manual verification

None. A live check needs a run long enough to compact after a plain user message
follows a skill invocation.
