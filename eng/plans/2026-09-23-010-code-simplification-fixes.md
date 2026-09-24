# Simplify committed batches, finished outcomes, and compaction ranking

## Goal

Address all three findings in
[`2026-09-23-010-code-simplification-review.md`](../reviews/2026-09-23-010-code-simplification-review.md).
Keep each committed assistant batch together, carry the final answer only in a
finished outcome, and rank compaction candidates by their request estimates.
Preserve model requests, ACP output, hook behavior, and interruption handling.

Use [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), [testing guidance](../testing.md), and
[`AGENTS.md`](../../AGENTS.md) as the implementation constraints.

## Related code

- `src/sessions.rs` — `TranscriptEntry`, `AssistantBatch`, batch validation,
  transcript validation, stored JSON, checkpoint validation, and session cost.
- `src/acp/prompt.rs` — Batch commit and cleanup, `run_after_tools`,
  `run_before_stop`, `run_after_run`, `PromptOutcome`, and `PromptOutput`.
- `src/acp/convert.rs` — Transcript replay and context-token reporting.
- `src/openrouter.rs` — `chat_messages` expands transcript entries into model
  messages, preserving tool arguments and continuation metadata.
- `src/compaction.rs` — Candidate cuts, request estimates, summarizer material,
  checkpoint projection, and actual-size checks.
- `src/acp.rs` — ACP prompt responses, headless output, and integration tests
  that inspect saved tool results.

## Decisions

- Replace the top-level `AssistantMessage` and `ToolResult` transcript variants
  with `AssistantBatch(AssistantBatch)`. Persist one `assistant_batch` entry
  containing `message` and `results`, including for messages with no tool calls.
  Keep `AssistantMessage` and `ToolResult` as the batch's component types.
- Derive stored JSON serialization for `AssistantBatch` with unknown fields
  rejected. Deserialization does not establish validity: reuse batch validation
  from construction and transcript validation at database-read and append
  boundaries. Validate the message and the exact ordered pairing of results.
- A checkpoint's `covered_prefix` remains an exclusive transcript-entry index.
  Its last covered entry must now be an assistant batch. Preserve the existing
  summary, bounds, and increasing-prefix checks. The stored shape and indices
  require database recreation under `AGENTS.md`; add no compatibility decoder.
- Keep `UncommittedAssistantBatch` and its cleanup responsibilities. A committed
  batch becomes one in-memory entry only after the store transaction succeeds.
- Retain the names `PromptOutcome` and `PromptOutput`. Change the internal
  finished outcome to `Finished(String)` and make `PromptOutput` an enum with
  `Finished(String)`, `Cancelled`, `TokenLimit`, and `Refused`. Errors remain in
  the existing result type. Convert to ACP stop reasons at the ACP response
  boundary; `after_run` reports the enum directly.
- Create `Finished(answer)` only after every applicable `before_stop` hook
  accepts the committed answer. A continuation carries no final answer. Keep
  cleanup and its error precedence ahead of `after_run` reporting.
- Use a stable sort by request estimate alone for compaction candidates. The
  existing threshold booleans are monotonic in that estimate and cannot change
  its order. Keep admission filtering, candidate retries, and the check that
  the actual summary produces a smaller admitted request.

## Naming

- **Assistant batch** — One assistant message plus exactly one final tool
  result for each call in the message, saved in one transaction. Use
  `AssistantBatch`, `TranscriptEntry::AssistantBatch`, and the stored kind
  `assistant_batch` for this same unit.
- **Uncommitted assistant batch** — A validated assistant message whose tool
  outcomes are incomplete or have not yet been saved. Keep
  `UncommittedAssistantBatch` for this state.
- **Compaction checkpoint** — A saved transcript entry with a compaction
  summary and the exclusive index of the completed prefix it covers. Keep
  `CompactionCheckpoint` and `covered_prefix`.
- **Prompt outcome** — The internal reason a prompt run stopped, determining
  how unfinished tool calls are completed and whether Ox returns an ACP stop
  reason or an error. Keep `PromptOutcome`.
- **Final answer** — The text of the assistant message committed with the
  finished OpenRouter stop that ended a prompt run. Carry it in the `Finished`
  variants, including an empty string when that is the accepted answer.
- **Request estimate** — Ox's heuristic token estimate for a serialized model
  request, using three bytes per token for text and a fixed allowance per image.
  Use `estimate` as the sole compaction ranking key.

## Test plan

Adapt existing coverage in place; these changes need no new test function.

- In `src/sessions.rs`, preserve database reopen and ordered-result coverage in
  `a_saved_batch_survives_database_reopen_in_order`. Update its expected stored
  entry and checkpoint indices. Keep constructor rejection of missing, extra,
  duplicate, reordered, and wrongly named results. Replace obsolete isolated
  result cases in `read_rejects_a_malformed_transcript` with malformed stored
  batches so JSON decoding cannot bypass validation. Retain unknown-field,
  invalid-call, settings, and hook-feedback checks. Check zero, out-of-range,
  non-batch, and non-increasing checkpoint prefixes.
- In `src/openrouter.rs`, retain the exact wire-message assertions in
  `requests_group_messages_and_send_continuation_metadata_once` and existing
  reasoning tests. One batch must expand to one assistant message followed by
  its tool messages in order, with unchanged raw arguments and metadata.
- In `src/acp/prompt.rs`, adapt ordered-result, replay, hook-lifecycle,
  cancellation, failed-append, and failed-update tests to inspect batches.
  Change the failed-append SQL trigger to `assistant_batch` so it still injects
  a real commit failure. Preserve assertions that observed results survive and
  the in-memory transcript advances only after persistence succeeds.
- Extend the existing before-stop continuation and after-run outcome cases to
  assert `Finished` contains only the accepted answer. A continuation followed
  by cancellation, refusal, token limit, or failure must not expose an earlier
  answer. Keep after-run failure and timeout from changing the returned result.
- Adapt ACP response and headless integration cases in `src/acp.rs` to the new
  enum and nested results. Preserve ACP stop reasons, headless success output,
  and signal/shutdown cleanup. Retain usage assertions after commit, compaction,
  and load, including costs and fallback estimates when usage is absent.
- In `src/compaction.rs`, retain complete-transcript replay, repeated skill
  invocation, image estimates, bounded tool-result excerpts, and failed-summary
  coverage. Update fixtures and prefix expectations for one entry per batch.
  Replace the 60% target assertion with an explicit before/after reduction and
  admission assertion. Exercise candidate ordering and stable ties within the
  existing compaction coverage, without reproducing the removed tuple formula.

## Implementation plan

1. Update `src/sessions.rs` to store `AssistantBatch` as one transcript entry.
   Give `AssistantBatch` a shared validation method and have `new` use it;
   validate results directly from their slice. Update encoding, decoding,
   `append_batch`, settings folding, and session-cost access. Remove
   `assistant_batch_end`, cross-entry orphan-result handling, and the collection
   and binary search of completed-batch offsets.
2. Update transcript validation in `src/sessions.rs`: validate each batch
   directly; require `after_tools` feedback to follow a batch with tool calls
   and `before_stop` feedback to follow one without calls. Preserve consecutive
   feedback of the same kind and current-skill checks. Validate checkpoint
   boundaries by inspecting the entry immediately before `covered_prefix`
   after checking its bounds.
3. Update `src/acp/prompt.rs` so `commit` pushes the saved batch intact.
   Have `run_after_tools` build owned tool reports from the just-committed
   batch's paired calls and results before running any hooks. Remove its calls
   argument and transcript suffix arithmetic. Keep reports stable while hook
   feedback is appended, and preserve incomplete-batch cleanup.
4. Update `src/openrouter.rs` to expand batches directly into the existing
   assistant and tool wire messages. Update `src/acp/convert.rs` to replay the
   message followed by paired calls and results, removing the remembered calls
   slice and ID search. Read model usage through the batch in usage updates.
5. Update `src/compaction.rs` to find candidate cuts immediately after batch
   entries. Extract answer text, call arguments, and result excerpts from each
   batch in their existing order. Keep request estimates counting every
   expanded wire message, and retain checkpoint projection and repeated skill
   invocation behavior. Adapt affected fixtures across these modules and
   `src/acp.rs` as part of the representation change.
6. Update `src/acp/prompt.rs` to carry the accepted answer in
   `PromptOutcome::Finished(String)` and return the `PromptOutput` enum. Remove
   `PromptRun.answer`, its initialization, and final-answer filtering. Match
   output variants exhaustively in `run_after_run`. In `src/acp.rs`, map them
   to ACP responses and extract only `Finished(answer)` for headless success.
   Preserve existing error distinctions and update outcome assertions.
7. In `src/compaction.rs`, remove `Budget.cut_target` and the `original`
   parameter from `ranked_cuts`; sort stably by `estimate`. Keep `original` in
   `compact` for the actual reduction check and update the existing compaction
   test's assertion and wording.

## Documentation updates

- `eng/architecture.md` — Describe one stored entry per assistant batch,
  feedback placement after batches, and checkpoint boundaries at batch entries.
  Clarify that a finished prompt outcome carries the accepted final answer.
- `eng/glossary.md` — Update transcript entry and assistant batch definitions
  to reflect the stored unit; keep assistant message and tool result as parts
  of that unit. Clarify the final answer's relationship to the finished outcome.
- `AGENTS.md` — Update the `src/sessions.rs` and `src/acp/prompt.rs` descriptions
  for batch entries and finished outcomes.
