# Calibrate compaction estimates

## Plan

`agents/plans/2026-10-01-009-calibrate-compaction-estimates.md`

## Summary

Request estimates now divide by the bytes per token measured from the latest
assistant batch's reported input tokens, falling back to three. Summarizer
requests keep three bytes per token. The plan's goal is met. OX-0030 is marked
fixed.

## Departures from the plan

None.

## Decisions

- `cut` measures bytes per token once and uses it for every candidate, so the
  loop rebuilds no extra request.
- `turn_start_before` was split out of `turn_provider_before` so the batch's
  turn model and turn provider share one lookup.

## Automated checks

- `make check` — Passed.
- `make e2e` — Not run; terminal behavior did not change.

## Manual verification

None. A live check needs a session long enough to reach the automatic threshold.

## Follow-up work

None.
