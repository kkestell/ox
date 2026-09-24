# Simplify batch ownership, tool outcomes, and turn settings

## Goal

Address the three findings in
[`2026-09-24-001-code-simplification-review.md`](../reviews/2026-09-24-001-code-simplification-review.md).
Give batch execution and cleanup one local owner, derive tool result identities
from their calls, and save effort and mode with each turn's input. Preserve ACP
output, model requests, hook behavior, and persistence on interruption.

Use [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), [testing guidance](../testing.md), and
[`AGENTS.md`](../../AGENTS.md) as the implementation constraints. Both stored
batches and turn starts change shape; apply the database recreation policy in
`AGENTS.md` when implementing them.

## Related code

- `src/acp/prompt.rs` — Turn startup, incomplete batch ownership, sequential
  tool execution, commit and cleanup, hook input, and model requests.
- `src/sessions.rs` — Transcript types, batch validation, settings recovery,
  turn-start appends, stored JSON, and checkpoint validation.
- `src/acp.rs` — Dispatch into prompt input, settings capture and restoration,
  model locking, headless entry, and integration coverage.
- `src/acp/convert.rs` — Live tool updates, replay, and usage reporting.
- `src/hooks.rs` — Tool reports supplied to after-tools hooks.
- `src/openrouter.rs` — Transcript encoding and ordinary request construction.
- `src/compaction.rs` — Request projection, estimates, candidate ranking,
  summarizer material, and repeated skill invocations.

## Decisions

### Batch ownership and outcomes

`PromptRun::process_batch` will own one local `UncommittedAssistantBatch` from
the validated assistant message through the save attempt. Pending tool updates
and tool execution run inside a phase whose result returns to this owner.
Every exit from that phase reaches cleanup before leaving `process_batch`.
Usage reporting and after-tools or before-stop hooks remain in
`run_model_step`, after successful batch processing.

`execute` takes the incomplete batch explicitly. It appends each observed
outcome before sending the finished tool update. On interruption,
`process_batch` fills the unstarted suffix, prepares its remaining updates,
attempts the commit, and sends those updates according to the existing cleanup
policy. `complete` consumes the incomplete batch; `commit` accepts an owned
`AssistantBatch` and extends the in-memory transcript only after persistence.
The outer `finish` only converts the final prompt outcome.

Execution is interrupted only by cancellation, an ACP update error, a
permission error, or a hook error, and each has its own placeholder outcome for
unstarted calls. A storage error comes only from the commit, which runs after
every call has an outcome, so `process_batch` returns it directly with no
suffix to fill and no placeholder. The placeholder match stays local to
`process_batch`; every other prompt outcome shares one `unreachable!` arm
there.

Keep the current error precedence: a failed cleanup commit replaces the
interruption with a storage error containing its context; a failure while
sending a remaining update becomes the final ACP error. Remaining updates are
skipped when the current outcome is an ACP update error. A failed store append
is attempted once. After-run hooks receive the result after cleanup, while the
session operation guard remains held.

Both batch types will use `outcomes: Vec<ToolOutcome>`. Outcome `i` belongs to
tool call `i`; the incomplete vector is an observed prefix, and a complete
batch requires equal lengths. Retain assistant-message validation at
construction, append, and database-read boundaries. Remove the `ToolResult`
type and its identity-copying constructor. ACP conversion, hook reporting,
OpenRouter encoding, and compaction pair calls with outcomes directly.

### Turn starts

Keep `TranscriptEntry::Model` as the single initial model entry. Replace the
top-level effort, mode, user-message, and skill-invocation variants with
`TranscriptEntry::TurnStart(TurnStart)`:

```rust
struct TurnStart {
    effort: EffortLevel,
    mode: SessionMode,
    input: TurnInput,
}

enum TurnInput {
    UserMessage(UserMessage),
    SkillInvocation(SkillInvocation),
}
```

Persist `TurnStart` under the kind `turn_start`, with required effort, mode,
and input fields. Encode `TurnInput` using the existing tagged-enum convention:
`type`, `content`, and snake-case variant names. Reject unknown fields. Keep
`UserMessage` and `SkillInvocation` as the input payloads.

`PromptInput.turn_input` holds `TurnInput`. Prompt startup resolves the settings
snapshot, then constructs the saved turn start with the captured effort and
mode. Every turn stores both values, including unchanged values and a return
to Default or Ask. Transcript validation requires the model entry to be
followed immediately by a turn start. `saved_settings` returns the supplied
defaults for an empty transcript; otherwise it reads the initial model and the
latest turn start instead of folding setting changes. Keep the existing
saved-settings fallback when no selection is supplied and the saved model's
authority over later selections.

Build the optional first model entry and the turn-start entry together in
`save_turn_start`. Use that same prospective transcript for admission and the
same appended slice for persistence. Retain `append_turn_start`'s slice-based
interface for these one or two entries. Remove the staged `settings_entries`
field, effort/mode change detection, and settings-block parser.

### Compaction projection

`projection` and `projection_at` currently manufacture a user-message
transcript entry for the compaction summary. With settings attached to turn
starts, they will instead return encoded OpenRouter messages as `Vec<Value>`.
Use `openrouter::chat_messages` to encode the selected transcript entries and
insert the labeled summary as a user-role message. Encode any repeated skill
invocation immediately after it.

Change `ordinary_body` and `Client::stream_completion` to consume these encoded
messages. `ordinary_body` still supplies the system message and request
parameters. Request estimates, actual requests, and compaction candidate
estimates use this same encoding. The summary-only base used by `ranked_cuts`
must use the same summary message constructor as `projection_at`.

This removes the synthetic transcript entry without introducing another
transcript type or assigning settings to a summary. Preserve the current
message order, image accounting, summary label, and ranking policy. Checkpoint
prefixes remain transcript-entry indices; update their values for the new
turn-start layout.

## Naming

- **Turn input** — The user message or skill invocation that starts a turn;
  represented by `TurnInput` and `PromptInput.turn_input`.
- **Turn start** — The saved turn input together with its effort level and
  session mode; represented by `TurnStart`, `TranscriptEntry::TurnStart`, and
  the stored kind `turn_start`.
- **Tool outcome** — Completed, failed, or cancelled, with explanatory text;
  keep `ToolOutcome` and use `outcomes` for the ordered batch vector.
- **Tool result** — A call's identity paired with its outcome. Derive this
  pairing from the call and corresponding outcome when projecting a batch;
  retire the separate `ToolResult` struct.
- **Assistant batch** — One assistant message and a final outcome for every
  call in order; keep `AssistantBatch` and its single stored entry.
- **Uncommitted assistant batch** — A validated assistant message whose
  outcomes are incomplete or unsaved; keep `UncommittedAssistantBatch`, owned
  locally by `process_batch`.

## Test plan

- In `src/acp/prompt.rs`, adapt the existing ordered-call, cancellation,
  completed-patch, failed-append, and failed-update tests. Cover failures while
  announcing pending calls as well as after an observed outcome within the
  existing failed-update case. Verify observed outcomes survive, unstarted
  calls get the correct outcome, failed commits leave the transcript unchanged,
  and interrupted batches produce no usage update or after-tools hook.
- Retain the existing hook-lifecycle and hook-error coverage in
  `src/acp/prompt.rs`: before-tool errors complete the batch, after-tools errors
  keep the committed batch, and after-run reports the final result after
  cleanup. Preserve accepted-answer and continuation behavior in the existing
  before-stop test.
- In `src/sessions.rs`, adapt database-reopen and malformed-transcript coverage
  to ordered outcomes and turn starts. Keep missing/extra outcome, invalid
  call, unknown-field, hook placement, and checkpoint-boundary checks. Replace
  cases for duplicated result identities and settings-block ordering with
  missing/invalid turn settings, malformed turn input, and a model entry not
  followed by a turn start. Preserve session-title
  adoption for text, image-only messages, and skill invocations.
- Replace settings-fold assertions with latest-turn settings assertions in
  `src/sessions.rs`; include returning to Default and Ask. Adapt
  `different_efforts_are_saved_and_sent_for_sequential_turns` and rename
  `absent_selection_uses_saved_settings_without_duplicate_entries` to describe
  saved-settings fallback. Keep the model lock, reload, and mid-turn selection
  tests in `src/acp.rs`, including headless Auto mode and Ask permission behavior.
- Preserve exact external identities, raw arguments, status, and text in the
  existing OpenRouter request, ACP update/replay, and hook-report assertions.
  Use distinct outcomes for multiple calls so a shifted association fails.
  Wire requests must omit the turn's saved settings from message content and
  retain text/image order and continuation metadata.
- Adapt the existing compaction tests to assert encoded projected messages.
  Retain summary replacement, repeated active skill invocations, full replay,
  image allowances, tool-result excerpts, ranking, and failure atomicity.
  Compare candidate size accounting with the corresponding projected request
  in the existing ranking coverage. Preserve automatic-compaction, overflow
  retry, and usage-update tests after updating checkpoint indices.

## Implementation plan

Steps 1 and 2 each compile and pass full validation on their own. Steps 3
through 6 form one change: replacing the transcript variants breaks encoding,
replay, and projection until all four steps are done, so validate them
together.

1. In `src/acp/prompt.rs`, introduce `process_batch` around pending updates,
   execution, completion, persistence, and interruption cleanup. Pass the local
   incomplete batch into `execute`; make `complete` consuming and `commit`
   accept the completed batch. Return a commit failure directly and remove the
   storage placeholder outcome. Remove `PromptRun.uncommitted_batch` and reduce
   `finish` to outcome conversion. Preserve the model loop's subsequent usage
   and hook ordering. Adapt the existing interruption tests with this change.
2. In `src/sessions.rs`, replace batch `results` with `outcomes`, replace
   `pair_results` with the count invariant, and remove `ToolResult`. Update the
   local incomplete batch and cleanup suffix in `src/acp/prompt.rs`. Change
   finished/replayed tool conversion in `src/acp/convert.rs` and
   `ToolReport::new` in `src/hooks.rs` to accept a call and outcome. Pair calls
   with outcomes in `src/openrouter.rs` and `src/compaction.rs`; update fixtures
   and stored-batch assertions across these modules and `src/acp.rs`.
3. In `src/sessions.rs`, add `TurnInput` and `TurnStart`, replace the four
   transcript variants, and update stored encoding and decoding. Return
   `Option<(usize, &TurnStart)>` from `latest_turn_start`. Recover saved settings
   from that value and the initial model. Remove `settings_block_end` and use
   a direct enumerated scan for transcript validation that requires a turn
   start immediately after the model entry; read current skill and
   before-run placement through the turn input. Derive the session title from
   the last appended turn start's input.
4. In `src/acp/prompt.rs`, accept `PromptInput.turn_input`, construct the turn
   start after resolving settings, and remove staged settings changes. Keep
   image validation, input admission, first-model notification, and durable
   save before the returned future runs. Update `hook_invocation` to read the
   typed turn input. In `src/acp.rs`, wrap dispatched user messages, skill
   invocations, and headless input in `TurnInput` and update prompt fixtures.
5. Update `src/acp/convert.rs` replay and `src/openrouter.rs` transcript encoding
   to read user messages and skill invocations through `TurnStart.input`.
   Preserve their existing external content and skip the saved effort and mode.
   Apply the turn-start and settings test changes across store, prompt, and ACP
   coverage.
6. In `src/compaction.rs`, return encoded messages from both projection functions
   and share summary-message construction with ranking. Update `ordinary_body`,
   `stream_completion`, and all callers to consume those messages. Adapt
   summarizer material and repeated-invocation detection to typed turn starts.
   Update projection tests and checkpoint indices without changing cut
   eligibility, ranking, or the final request-size checks.
7. Update the documentation below and remove obsolete test helpers and cases
   for the retired representations as their replacements are completed.

## Documentation updates

- `eng/architecture.md` — Describe settings stored on turn starts, outcomes
  associated by call order, local batch ownership and cleanup, and compaction's
  encoding directly into request messages. Update the corresponding invariants.
- `eng/glossary.md` — Add turn input and turn start; update saved settings,
  transcript entry, skill invocation, tool result, assistant batch, uncommitted
  assistant batch, and commit to reflect their new representations and owner.
- `AGENTS.md` — Update the descriptions of `src/sessions.rs`,
  `src/acp/prompt.rs`, `src/openrouter.rs`, and `src/compaction.rs`.
