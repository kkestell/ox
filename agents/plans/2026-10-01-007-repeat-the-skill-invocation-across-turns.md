# Repeat the skill invocation across later turns

## Goal

OX-0029: when a compaction checkpoint covers a skill invocation, model requests
repeat that skill invocation after the summary only while it is the latest turn
start. Once the user sends a plain message, such as "Try again", the next
compaction drops the skill's instructions and arguments, and the model stops
following them. When this work is done, the latest skill invocation repeats
after the summary whenever the summary covers it, across any number of later
user messages, until another skill invocation begins.

## Related code

- `crates/ox-server/src/compaction.rs:138` — `repeated_invocation`, which picks
  the skill invocation to repeat. It is the only rule that changes.
- `crates/ox-server/src/compaction.rs:170` — `projection_at`, which places the
  repeated skill invocation between the summary and the uncovered entries.
- `crates/ox-server/src/compaction.rs:244` — `ranked_cuts`, which adds the
  repeated skill invocation's bytes to each cut that covers it. It already calls
  `repeated_invocation` with the whole transcript and filters by cut, so it
  follows the new rule unchanged.
- `crates/ox-server/src/sessions.rs:188` — `latest_turn_start`. The compaction
  module stops using it; `sessions.rs:658` still does.

## Decisions

- **The latest skill invocation in the transcript is the one repeated, and only
  when the summary covers it.** If a later skill invocation exists, it replaces
  the earlier one. If the latest skill invocation is uncovered, it is already in
  the request, so nothing is repeated.
- **The repetition does not end when the skill's work ends.** A session that
  moves on to unrelated plain messages keeps receiving the last skill invocation
  after each summary. The transcript does not record when a skill's work is
  done, and the issue asks for repetition until a new skill invocation.

## Test plan

- `crates/ox-server/src/compaction.rs`, new table-driven test
  `a_covered_skill_invocation_repeats_until_a_later_skill_invocation`. Each case
  builds a transcript with one checkpoint and checks the projection's second
  message:
  - a covered skill invocation followed by a covered user message turn: the
    skill invocation follows the summary (the regression);
  - a covered skill invocation followed by an uncovered user message turn: the
    skill invocation follows the summary, then the user message;
  - two covered skill invocations: only the later one follows the summary;
  - a covered skill invocation followed by an uncovered skill invocation:
    nothing is repeated, and the uncovered skill invocation follows the summary.
- `crates/ox-server/src/compaction.rs`
  `manual_compaction_uses_a_summary_and_keeps_the_complete_transcript`: the
  final projection now expects the summary followed by the `/goal` skill
  invocation, since the third checkpoint covers the later "active request" turn.
- `crates/ox-server/src/compaction.rs`
  `ranked_cuts_follow_the_latest_checkpoint_smallest_request_first`: make the
  covered "earlier request" a skill invocation, and expect each cut's bytes to
  include that skill invocation's bytes, so the estimate is shown to match the
  projection when a user message turn follows the skill invocation.

## Implementation plan

- In `repeated_invocation`, find the index of the latest turn start whose input
  is a skill invocation, and return it when it is below `cut`. Rewrite its doc
  comment to state the new rule, and update the comment above the
  `repeated_invocation` call in `ranked_cuts` if it no longer matches.
- Remove `self` from the `sessions` import in `compaction.rs` if nothing else in
  the non-test code uses it.
- Add and update the tests above.
