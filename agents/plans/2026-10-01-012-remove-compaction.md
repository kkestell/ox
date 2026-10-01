# Remove compaction

## Goal

Resolve the findings of `agents/reviews/2026-10-01-011-day-simplicity-review.md`
by deleting code. Compaction is removed entirely so it can be reimplemented
later. When a model provider rejects a request as too large for the model
context, the Ox server panics with a clear message. The OpenAI stream parser
drops the redundant output it keeps only for cross-checks.

- OX-0045: fixed by removing compaction.
- OX-0046: fixed by removing compaction; summarizer requests no longer exist.
- OX-0047: won't fix. The startup error names the failing provider, and the user
  can retry or run `ox auth logout` for the unused provider.
- OX-0048: fixed by simplifying `openai.rs`.

## Related code

- `crates/ox-server/src/compaction.rs` — the whole feature: estimates, budgets,
  cuts, summaries, projection, and admission checks.
- `crates/ox-server/src/acp/prompt.rs:383` — `save_turn_start` checks images and
  admission through compaction.
- `crates/ox-server/src/acp/prompt.rs:473` — `request_completion` compacts
  automatically and retries after an input context overflow.
- `crates/ox-server/src/acp/prompt.rs:565` — `deliver_subagent_messages` checks
  admission.
- `crates/ox-server/src/acp.rs:104`, `:122`, `:216`, `:655` — the `/compact`
  command, `Dispatch`, `compact_session`, and `spawn_compaction`.
- `crates/ox-server/src/acp/convert.rs:119` — `usage_update` falls back to the
  request estimate.
- `crates/ox-server/src/sessions.rs` — the `CompactionCheckpoint` entry, its
  validation, `append_checkpoint`, and its cost in `transcript_cost` and
  `children_cost`.
- `crates/ox-server/src/model.rs` — `SUMMARIZER_MAX_TOKENS`, `ordinary_body`,
  `summarizer_body`, `Clients::summarize`, `Provider::user_message`,
  `summarizer_effort`, and the `turn_provider` argument of
  `Provider::transcript`. `is_input_context_overflow` stays.
- `crates/ox-server/src/openrouter.rs`, `crates/ox-server/src/openai.rs` — the
  summarizer body and `summarize`, the `turn_provider` argument of
  `chat_messages` and `input`, and the 8,000-token catalog filter.
- `crates/ox-server/src/openai.rs:617` — `Assembly` and its use in `process`.
- `crates/ox-server/src/skills.rs:112` — reserves the name `compact`.

## Decisions

- **The panic fires only on a provider's input context overflow.** Ox no longer
  estimates request size. Both providers already classify the rejection as
  `InputContextOverflow`, which is exact. The turn start that caused it stays
  saved, so the session keeps failing; the user starts a new session. The
  release profile aborts on panic, so the whole Ox server process ends, and the
  Ox client shows the panic message as a diagnostic from the server's stderr.
  The same applies to a subagent's request.
- **`/compact` becomes ordinary text.** With no built-in command, `dispatch`
  returns a `TurnInput` directly and the `Dispatch` enum is deleted.
- **The usage update reports reported usage only.** The context tokens are the
  latest assistant batch's reported input and output tokens, or 0 when that
  batch reported none. The update is still absent before the first assistant
  batch.
- **Image admission checks the transcript directly.** A model without image
  input rejects the turn when any turn start in the prospective transcript has
  an image: a user message with an image part, or a skill invocation with
  images. Without compaction every saved image reaches every request.
- **The OpenAI completion comes from completed items only.** Use the terminal
  output when it is nonempty, otherwise the `response.output_item.done` items.
  Keep the done-item map with its duplicate and contiguity checks, the reasoning
  part position for separators in provisional reasoning, the successful
  terminal-event requirement, and validation of the final message and tool
  calls. Delete the added-item map, the text and argument delta maps, the
  accumulated reasoning string, and every check comparing them or the done items
  with the terminal output. `response.output_item.added` and
  `response.function_call_arguments.delta` join the ignored event types.
- **Existing databases are recreated.** Saved `compaction_checkpoint` rows no
  longer decode.

## Naming

- `input context overflow` — a model provider's rejection of a request as too
  large for the model context, as `InputContextOverflow` already names it.
- Panic message:
  `the session exceeds the model context limit; start a new
  session`.

## Test plan

- Add `an_input_context_overflow_panics_with_a_clear_message` in
  `acp/prompt.rs`: an OpenRouter 400 reply with
  `maximum context length
  exceeded` panics with the message above
  (`#[should_panic(expected = ...)]`). The OpenAI classification stays owned by
  `refusal_context_overflow_truncation_and_stalls_have_distinct_outcomes`.
- Delete, in `acp/prompt.rs`:
  `openai_compaction_is_saved_before_the_next_subscription_request`,
  `oversized_input_is_rejected_before_save_and_a_valid_prompt_can_follow`,
  `automatic_compaction_precedes_the_next_model_request`, and
  `explicit_input_overflow_retries_once_only_after_a_smaller_checkpoint`.
- Delete
  `manual_compact_command_uses_the_active_prompt_without_saving_a_message` in
  `acp.rs`, `a_subagent_compacts_its_own_conversation` in `subagents.rs`,
  `checkpoints_cover_a_growing_prefix_that_ends_at_an_assistant_batch` in
  `sessions.rs`, and the tests in `compaction.rs`.
- Rewrite:
  - `changing_provider_routes_the_next_prompt_and_manual_compaction` in `acp.rs`
    becomes `changing_provider_routes_the_next_prompt`, without the manual
    compaction part and the summary replies.
  - `slash_commands_have_acp_metadata_and_prompt_dispatch` in `acp.rs` drops the
    `compact` command and cases; `/compact` dispatches as a user message.
  - `a_saved_batch_survives_database_reopen_in_order` and
    `children_cost_sums_the_saved_costs_of_every_child_session` in `sessions.rs`
    drop their checkpoints; the expected children cost becomes 0.75.
  - `resume_lists_and_loads_a_different_session_after_close` in
    `crates/ox/src/acp.rs` checks that the commands update arrives for each
    session and that the commands are empty, instead of `["compact"]`.
  - `catalog_filter_keeps_recent_usable_models_by_name_with_known_efforts` in
    `openrouter.rs` drops the `summarizer_effort` assertions and the
    `acme/small` fixture model.
  - `subscription_requests_encode_transcript_tools_and_reasoning_directly` in
    `openai.rs` drops the summarizer request and its reply.
  - `separately_completed_items_require_a_successful_terminal_event` in
    `openai.rs` drops the "Missing item" case, which relied on the added-item
    map.
  - The skill name table in `skills.rs` drops the `compact` case.
- Image admission stays owned by
  `invalid_prompt_startup_sends_no_request_or_user_message`.

## Implementation plan

1. Delete `crates/ox-server/src/compaction.rs`,
   `crates/ox-server/src/prompts/compaction_prompt.md`, and `mod compaction` in
   `lib.rs`.
2. In `acp/prompt.rs`: replace `compaction::has_images` with the transcript
   check, delete the `input_fits` checks in `save_turn_start` and
   `deliver_subagent_messages`, and delete `compact`. In `request_completion`,
   build the request from `provider.transcript(&self.transcript)` and send it
   once. Map stream start and stream item errors through one function that
   panics on `model::is_input_context_overflow` and otherwise returns
   `PromptOutcome::ModelRequest`. Remove "including automatic compaction" from
   the `selected_settings` comment.
3. In `acp.rs`: delete `compact_session`, `spawn_compaction`, `Dispatch`, and
   the `compact` entry of `available_commands`; `dispatch` returns `TurnInput`.
   Update the comments on `available_commands`, `send_available_commands`, and
   `start_prompt`, and the `/compact` sentences in `acp/operations.rs`.
4. In `acp/convert.rs`: simplify `usage_update` as decided and drop the
   `CompactionCheckpoint` arm of `replay_transcript`.
5. In `sessions.rs`: delete `CompactionCheckpoint`, its validation,
   `append_checkpoint`, its encoding and decoding, and its cost. `children_cost`
   sums `$.message.usage.cost` over `assistant_batch` rows only.
6. In `model.rs`, `openrouter.rs`, and `openai.rs`: delete the summarizer code,
   `model::ordinary_body`, `Provider::user_message`, the `turn_provider`
   argument (start from `None`), the `CompactionCheckpoint` arms, and the
   8,000-token catalog filter with its comment.
7. In `skills.rs`: delete the `compact` name reservation.
8. In `subagents.rs:416`: the comment says a queued message that cannot start
   ends the subagent with a failure.
9. In `openai.rs`: simplify `Assembly`, `process`, and `finish` as decided.
10. In `scripts/bench.py`: delete the `compactions` and `summarizer_cost`
    metrics.
11. Update the tests as listed, then the docs.
12. In `agents/issues.csv`, set OX-0045, OX-0046, and OX-0048 to `fixed` and
    OX-0047 to `wontfix`; remove the four items from `agents/todo.md`.
13. Recreate the local database.

## Documentation updates

- `agents/architecture.md`: delete the Compaction component and the compaction
  summary sentence under Sources of authority; drop compaction from the
  dependency sentence; the constraint reads "A prompt run and its subagents
  share the turn's provider"; add compaction to the list of things Ox does not
  have.
- `agents/glossary.md`: delete Compaction checkpoint and Action log.
