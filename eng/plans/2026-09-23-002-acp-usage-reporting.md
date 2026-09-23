# ACP usage reporting

## Goal

ACP clients cannot show how full a session's context is or how much it has
cost, because Ox ignores the usage OpenRouter reports and sends no ACP usage
update. When this work is done, Ox saves the usage from each completed model
request and sends a usage update after each commit that changes the context,
after each compaction, and after replay. Each usage update carries the context
tokens, the model's context limit, and the session cost in US dollars.

## Related code

- `src/openrouter.rs` — `CompletionStream::next` already reads the stream to
  the end after the finish chunk. `process_sse_event` currently drops the usage
  chunk, and `Chunk` has no `usage` field. `Client::summarize` returns only the
  summary text. The `fixture` module builds scripted SSE replies.
- `src/sessions.rs` — `AssistantMessage` and `CompactionCheckpoint` define the
  stored JSON for transcript entries. `CompactionCheckpoint` derives `Eq`,
  which an `f64` field does not allow.
- `src/compaction.rs` — `compact` may call `summarize` several times across
  pieces and ranked cuts before it commits one checkpoint. `request_estimate`
  is the request estimate for the projection the next model request sends.
- `src/acp/prompt.rs` — `run_model_loop` commits each assistant batch, and
  `PromptRun::compact` wraps automatic compaction. Both change the context.
- `src/acp/convert.rs` — builds ACP updates, including `replay_transcript`.
- `src/acp.rs` — `load_session` replays the transcript. `compact_session`
  runs `/compact` and currently has no way to send ACP updates.
- `agent-client-protocol-schema` 1.7.0 — `SessionUpdate::UsageUpdate(UsageUpdate
  { used, size, cost })` and `Cost::new(amount, currency)` are available
  without an unstable feature.

## Decisions

- **Only `usage_update` is sent.** Token usage on `PromptResponse` is behind
  the `unstable_end_turn_token_usage` feature, so Ox does not enable it.
- **Usage is saved in the transcript.** Session cost must survive reload and
  restart. It comes from folding the transcript, like saved settings, so no
  second store exists. Each assistant message stores the model usage of the
  model request that produced it. Each compaction checkpoint stores the summed
  cost of every summarizer request made by the `compact` call that committed
  it, including cuts it tried and rejected.
- **Uncommitted work is not counted.** A model request that fails, is
  cancelled, or is rejected has no saved entry, so its cost is absent from the
  session cost. A `compact` call that commits no checkpoint also records
  nothing. OpenRouter sends usage only in the final chunk, so an interrupted
  stream has no usage to save in any case.
- **Request usage explicitly.** OpenRouter currently sends usage for streamed
  requests without an opt-in, but its documentation recommends
  `"usage": {"include": true}`. Set it on ordinary and summarizer requests.
- **Missing usage is allowed, and malformed usage is an error.** If a stream
  ends without a usage object, the completion has no model usage (`None`). If a
  usage object is present but lacks `prompt_tokens`, `completion_tokens`, or
  `cost`, the chunk is malformed and the request fails, like any other malformed
  chunk. Ox reads `usage` from any chunk, so usage in the same chunk as the
  finish reason is also captured.
- **Context tokens come from reported usage when it is current, and from the
  request estimate otherwise.** Look at the latest assistant message or
  compaction checkpoint in the transcript. If it is an assistant message with
  model usage, the context tokens are its input tokens plus output tokens. If
  it is a checkpoint, or an assistant message without model usage, the context
  tokens are `compaction::request_estimate` for the current transcript and
  settings. This way the count drops after compaction, before another model
  request reports real numbers. A transcript with no assistant message has no
  usage update.
- **Cost is reported in USD.** OpenRouter reports `cost` in credits, and its
  documentation says credits use US dollars as their base currency. The cost
  is omitted when no saved entry has a reported cost.
- **Sending points.** The prompt run sends a usage update after each commit in
  `run_model_loop` and after each automatic compaction that commits a
  checkpoint. A send failure there is `PromptOutcome::AcpUpdate`, the same as
  other updates. The batch that `finish` saves after a stop sends no usage
  update; the next usage update counts it. `/compact` sends one after its
  checkpoint commits. `load_session` sends one after replay. Headless runs
  already discard updates.

## Naming

- **Model usage** (`ModelUsage`) — The input tokens, output tokens, and cost
  that OpenRouter reports for one model request. Fields: `input_tokens`,
  `output_tokens`, `cost`. Stored as `AssistantMessage::usage` and carried as
  `Completion::usage`.
- **Summarizer cost** (`CompactionCheckpoint::summarizer_cost`) — The summed
  cost of the summarizer requests made by the `compact` call that committed a
  checkpoint. `None` when none of them reported usage.
- **Session cost** — The sum of every saved model usage cost and summarizer
  cost in a transcript.
- **Context tokens** — The number of tokens Ox reports as currently in the
  session's context, as described in Decisions.
- **Usage update** — The ACP update that reports context tokens, the context
  limit, and session cost. It is `SessionUpdate::UsageUpdate` in code and
  `usage_update` on the wire.

## Test plan

Extend existing tests. Add no new test functions.

- `streams_assemble_text_reasoning_and_fragmented_tool_calls`
  (`src/openrouter.rs`): add a usage chunk after the finish chunk and assert
  the completion's model usage. Extend
  `incomplete_or_malformed_streams_are_errors_and_partial_calls_never_complete`
  with a usage object that lacks `cost`. Existing fixture replies without
  usage already show that no usage chunk means `None`.
- Existing request encoding tests: assert that ordinary and summarizer
  requests include the usage opt-in.
- `a_text_answer_is_saved_in_the_transcript` (`src/acp/prompt.rs`): reply
  with usage, then assert that the saved assistant message has that model
  usage and that the final update is a usage update. It must have `used` equal
  to input plus output tokens, `size` equal to the catalog context limit, and
  a USD cost.
- `manual_compact_command_uses_the_active_prompt_without_saving_a_message`
  (`src/acp.rs`): the summarizer reply carries usage. Assert the checkpoint's
  summarizer cost, and assert that one usage update is sent with estimated
  context tokens and a session cost equal to the saved assistant message cost
  plus the summarizer cost.
- `configuration_selections_validate_and_restore_saved_values` (`src/acp.rs`):
  save an assistant batch with model usage before loading, and assert that
  replay ends with a usage update with the saved values.
- Update the stored transcript fixtures and the `AssistantMessage` literals in
  tests for the new fields, without adding assertions.

## Implementation plan

1. `src/sessions.rs`: add `ModelUsage { input_tokens: u64, output_tokens: u64,
   cost: f64 }` with serde derives and `deny_unknown_fields`. Add
   `usage: Option<ModelUsage>` to `AssistantMessage` and
   `summarizer_cost: Option<f64>` to `CompactionCheckpoint`, and remove `Eq`
   where `f64` requires it. Add
   `pub fn session_cost(transcript: &[TranscriptEntry]) -> Option<f64>` next
   to the other transcript folds.
2. `src/openrouter.rs`: request usage in both streamed request bodies. Add a
   private `ApiUsage { prompt_tokens,
   completion_tokens, cost }` and `usage: Option<ApiUsage>` on `Chunk`. In
   `process_sse_event`, save any chunk's usage in a new `CompletionStream`
   field before the assembly check. Add `usage: Option<ModelUsage>` to
   `Completion`. In `next`, after reading the stream to the end, move the saved
   usage into the buffered completion, and have `Assembly::finish` copy it into
   the message. Change `summarize` to return the summary and its
   `Option<ModelUsage>`. Update the comment in `next` that describes the
   trailing chunks. Add a fixture helper `usage(input, output, cost)` that
   returns a usage chunk.
3. `src/compaction.rs`: in `compact`, add up the cost of every `summarize`
   result and store it as the checkpoint's `summarizer_cost`.
4. `src/acp/convert.rs`: add `pub fn usage_update(transcript, settings,
   system_prompt) -> io::Result<Option<SessionUpdate>>`. It calculates the
   context tokens, takes `size` from the catalog context limit, and adds the
   session cost as `Cost::new(amount, "USD")`.
5. `src/acp/prompt.rs`: add a `send_usage` helper that builds a usage update
   from `self.transcript` and sends it. Call it after the commit in
   `run_model_loop` and in `PromptRun::compact` when compaction returns `true`.
6. `src/acp.rs`: give `compact_session` a `send_update` parameter, and send
   the usage update after a committed checkpoint. The `PromptRequest` handler
   passes a sender built like the prompt run's. In `load_session`, send the
   usage update after `replay_transcript`.

## Documentation updates

- `eng/architecture.md`: in the Transcript section, state that an assistant
  message stores its model usage and a checkpoint stores its summarizer cost.
  In the Projection boundary section, describe the usage update, its sending
  points, and the rule for context tokens.
- `eng/glossary.md`: add model usage, summarizer cost, session cost, context
  tokens, and usage update. Add table rows for context tokens (`used` /
  `usage.prompt_tokens + usage.completion_tokens`), context limit (`size` /
  —), and session cost (`cost` / sum of `usage.cost`).
- `AGENTS.md`: mention usage parsing for `src/openrouter.rs`, model usage and
  summarizer cost for `src/sessions.rs`, the usage update for
  `src/acp/convert.rs`, and the usage updates after `/compact` and load for
  `src/acp.rs`.
