# Save turn errors in the transcript

## Plan

`agents/plans/2026-10-01-006-save-turn-errors.md`

## Summary

A turn that ends with an error now saves a turn error as its last transcript
entry. Replay shows it as a failed "Turn error" tool call, and model requests
and summarizer material skip it. If saving the turn error fails, the turn's
error names both failures. The plan's goal is met.

## Departures from the plan

- `PromptOutcome`'s `Display` now formats ACP update and permission errors with
  `error_text`. The ACP error's own `Display` quotes its data as JSON, so the
  saved text read `Internal error: "connection closed"`.
- No subagent test needed a change; none asserts a failed child transcript.

## Decisions

- `a_failed_batch_append_leaves_the_transcript_unchanged` now refuses every
  entry kind except `turn_start`. It covers both the failed batch and the failed
  turn error save.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed.

## Manual verification

None. A live check needs a model provider to fail a request on demand.
