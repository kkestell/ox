# Session compaction

## Goal

Keep long sessions usable within a model's context limit. `/compact` should
compact an existing session on request, and a prompt run should compact before
a model request becomes too large. The complete transcript must remain available
for replay after a restart. Until a compaction checkpoint commits, failure or
cancellation must leave the model request conversation unchanged.

## Related code

- `src/acp.rs` advertises `/compact`, currently treats it as a stub, and owns
  the session operation guard and active session's system prompt.
- `src/acp/prompt.rs` saves a user message before its first model request and
  makes further model requests after committed assistant batches.
- `src/openrouter.rs` holds the model catalog, builds requests from the entire
  transcript, and turns OpenRouter failures into `io::Error`.
- `src/sessions.rs` validates and saves the transcript and folds durable session
  settings from it.
- `src/acp/convert.rs` replays displayable transcript entries.
- `eng/scratch/compaction-survey.md` surveys trigger, summary, and recent-turn
  choices; its measurements are a quick code survey, not quality benchmarks.

## Decisions

- Add a compaction checkpoint as a transcript entry. It contains a nonempty
  compaction summary and the exclusive transcript index of the completed prefix
  it covers. The index is stable because transcript entries are append-only.
  Append the checkpoint in one store transaction and update the prompt run's
  transcript copy only after that transaction commits. Keep all earlier entries
  for replay. The latest checkpoint controls model requests; older checkpoints
  remain in the transcript but are omitted from model requests and replay.
  Validate that each covered prefix ends no later than the checkpoint's own
  position and advances beyond the previous checkpoint's covered prefix.
- A checkpoint may cover only a prefix ending after a complete assistant batch.
  Prefer keeping the most recent two complete user turns, then one, when they
  fit the target budget and leave an eligible prefix to compact. Retain fewer
  when needed to make progress. During a long tool loop, a cut may fall between
  committed assistant batches of the current turn. The compaction summary must
  then carry the active user request. Never split an assistant message from its
  tool results or a settings block from its user message.
- Build each ordinary model request from the active session's unchanged system
  prompt, the latest compaction summary as a labeled user-role message, and the
  transcript entries after the checkpoint's covered prefix. Without a
  checkpoint, send the full transcript as today. Preserve complete recent
  entries, including continuation metadata, through the existing encoder.
  Do not persist the synthetic summary message as a user message.
- Generate a new compaction summary from the previous compaction summary and
  the newly covered transcript entries, skipping all checkpoint entries. Use
  the session model at Low effort with no tools, no ACP output, and a
  4,096-token output cap. The summarizer input includes user messages, assistant
  answer text, tool names and arguments, and bounded
  tool outcomes (up to 2,000 characters each); omit visible reasoning and
  continuation metadata. The prompt in `src/prompts/compaction_prompt.md` asks
  for the goal, constraints, completed work, relevant files, unresolved errors,
  and next step. Use it as a dedicated system prompt for summarization; the
  active session's unchanged system prompt applies to ordinary model requests.
  Accept only a finished, nonempty text completion with no tool calls; reject
  refusals and token-limit completions.
- Budget the complete summarizer request, including its instructions, previous
  summary, and output allowance. If the input does not fit, summarize bounded
  pieces in order, carrying the provisional summary into the next request.
  Split oversized text fields at character boundaries and label their source
  and continuation; these pieces are summary input, not transcript cuts.
  Keep every intermediate summary provisional and append only one checkpoint
  after all selected input is covered and the final projection is accepted.
  Fail without committing if the fixed summarizer input leaves no room for a
  piece or any summary request fails.
- Put a context limit beside each model in the model catalog, using verified
  OpenRouter values. Estimate request tokens from the serialized request size
  at three bytes per token, including the system prompt and tool schemas.
  Reserve the larger of 8,000 tokens or 10% of the context limit for model
  output. Trigger automatic compaction when the estimate reaches 80% of the
  remaining budget; prefer a cut that brings it below 60%, allowing room for
  the summary when selecting the cut. The remaining budget is the admission
  limit; 60% is a reduction target. Accept a result above the target when it
  strictly reduces the request estimate and fits the admission limit. Require
  that same reduction for every committed compaction. Keep these initial
  constants near the budget calculation, not as user-facing settings. The
  estimate is a heuristic; provider overflow remains possible.
- Before saving a user message, estimate the prospective request. If it exceeds
  the admission limit, estimate it after the largest eligible cut, including
  the new message, any unanswered messages that cannot be cut, fixed request
  overhead, and an allowance for a summary when needed. If that still exceeds
  the limit, reject the input without saving it. Accepted input is still saved
  before its first model request. This prevents locally rejected input from
  blocking all later prompts in the session.
- Check before every ordinary model request, after the user message or prior
  assistant batch is saved. Automatic compaction uses the 80% trigger. When
  there is no eligible prefix, allow a request within the admission limit even
  above the trigger; otherwise return a clear context-size error. A failed
  summary attempt stops the prompt run with previously committed entries intact.
- Manual `/compact` uses the same compaction operation under the session
  operation guard and cancellation signal, without saving the command as a
  user message. It bypasses the automatic threshold and is a no-op when there
  is no eligible prefix or a valid summary produces no reduction. Summary
  failures remain errors. An empty session needs no OpenRouter client. Manual
  compaction never continues the ordinary model loop.
- Classify an explicit OpenRouter input-context overflow at the OpenRouter
  boundary. On that pre-stream failure, force compaction regardless of the
  local estimate and retry that ordinary model request once, only after a
  strictly smaller projection commits. If no reduction is possible, return
  the context-size error without resending the same request. The retry allowance
  is per ordinary model request, including requests after later tool batches.
  Do not retry other HTTP or stream failures, and do not interpret
  an output token-limit completion as input-context overflow. If compaction
  fails or is cancelled, keep the previous checkpoint and stop the prompt run
  with its saved user message and assistant batches intact. Observe cancellation
  during each summary request and immediately before checkpoint persistence.
  Commit is the activation boundary; cancellation after commit stops further
  work without undoing the checkpoint.

## Naming

- **Compaction summary**: Model-generated text carrying relevant older
  conversation into later model requests. It is distinct from the existing
  session summary, which is session metadata.
- **Compaction checkpoint**: A transcript entry containing a compaction summary
  and the exclusive index of the transcript prefix it covers.
- **Context limit**: The model catalog's maximum token budget for a model
  request and its output.
- **Request estimate**: Ox's heuristic token estimate for one prospective
  model request. All other project terms retain their definitions in
  `eng/glossary.md`.

## Test plan

- Extend session store and request-encoding tests to show that a checkpoint
  survives reload, invalid prefix indices or split tool pairs are rejected,
  replay still shows the complete conversation, and model requests include
  exactly one current compaction summary plus the correct recent entries.
  Include nonadvancing indices and a checkpoint covering entries appended
  after it; preserve saved settings across checkpoints.
- Extend prompt-run fixture tests for manual compaction, the automatic threshold
  before the first model request and between tool batches, a second compaction
  within the same user turn that carries forward the previous summary and
  active request, and the no-eligible-prefix case. Cover manual compaction below
  the trigger and a valid request above the trigger with no eligible prefix.
- Cover rejected oversized input followed by a small valid prompt, including
  after reload and after earlier unanswered messages. Cover a useful reduction
  that fits the admission limit but cannot reach the 60% target.
- Exercise summary input spanning several requests, including one oversized
  text field. A failure in a later piece must leave the old checkpoint active.
- Cover an empty or oversized summary, cancellation, and store failure: no new
  checkpoint becomes active, while earlier committed entries remain readable.
  Include cancellation after summary completion but before persistence.
  Cover an overflow below the local trigger, one retry with a smaller request,
  no retry when reduction fails, and no retry for an output token limit or other
  failure. Consolidate overlapping existing tests as required by `eng/testing.md`.

## Implementation plan

1. In `src/sessions.rs`, add the checkpoint transcript entry, encoding,
   validation of its summary and advancing covered prefix, and transactional
   append. Update saved-settings folding to ignore it.
2. Add `src/compaction.rs` and `src/prompts/compaction_prompt.md` for request
   estimation, safe cut selection, bounded summarizer input, and model-request
   projection. Keep the projection derived from the saved transcript.
3. In `src/openrouter.rs`, add catalog context limits and a tool-free,
   bounded summarizer request. Send projected messages for ordinary model
   requests and expose explicit input-context overflow separately from other
   request failures.
4. In `src/acp/prompt.rs`, reject input that cannot fit before saving it, check
   the budget before each ordinary model request, run and commit compaction
   when needed, and apply the one-time overflow retry per model request.
   Observe prompt cancellation throughout summarization and do not hold the
   store mutex across an await.
5. In `src/acp.rs`, route `/compact` through the guarded compaction path using
   the active session's captured system prompt and saved model. In
   `src/acp/convert.rs`, omit checkpoint entries from replay.

## Documentation updates

- Update `eng/architecture.md` for the checkpoint projection, command, and
  prompt-run boundary, input rejection before persistence, and the dedicated
  summarizer system prompt; update `eng/glossary.md` with the new terms; and keep
  `AGENTS.md` current with the new source file and changed responsibilities.
