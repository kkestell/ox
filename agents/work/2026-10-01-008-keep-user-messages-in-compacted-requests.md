# Keep user messages in compacted requests

## Plan

`agents/plans/2026-10-01-008-keep-user-messages-in-compacted-requests.md`

## Summary

Compacted model requests now keep every user message and the latest skill
invocation in place, list covered tool calls in action logs, and follow them
with a summary in the five-heading format and the recent entries unchanged. One
compaction picks one cut by the recent allowance. The plan's goal is met.
OX-0044 is marked fixed.

## Departures from the plan

- `compacted_suffixes_keep_turn_provider_for_metadata_and_byte_estimates` lost
  its byte assertion and is now
  `compacted_suffixes_keep_turn_provider_for_metadata`. The assertion compared
  the projection with summed `entry_bytes`, which is gone; `cut` measures recent
  entries directly and `projected_tokens` measures the whole request.
- Tests outside `compaction.rs` that built large transcripts from user messages
  or expected the summary first in the request were updated:
  `automatic_compaction_precedes_the_next_model_request`,
  `explicit_input_overflow_retries_once_only_after_a_smaller_checkpoint`,
  `openai_compaction_is_saved_before_the_next_subscription_request`,
  `manual_compact_command_uses_the_active_prompt_without_saving_a_message`, and
  `a_subagent_compacts_its_own_conversation`. Each now puts the bulk in an
  assistant answer and reads the summary after the kept user message.
- The `summarizer_cost` doc comment in `sessions.rs` no longer mentions rejected
  cuts.

## Decisions

- `cut` sums the bytes of recent entries candidate by candidate from the end of
  the transcript, so each entry is encoded once. The sum is an estimate for
  choosing the cut; the admission check uses the full projection.
- A manual `/compact` on a session whose recent entries all fit the recent
  allowance covers only up to the first candidate after the latest checkpoint,
  and commits nothing when the summary would not shrink the request.

## Automated checks

- `make check` — Passed.
- `make e2e` — Not run; terminal behavior did not change.

## Manual verification

None. A live check needs a session long enough to reach the automatic threshold.

## Follow-up work

- `agents/plans/2026-10-01-009-calibrate-compaction-estimates.md` (OX-0030).
