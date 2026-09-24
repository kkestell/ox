# Model request parameters, transcript appends, and skill invocation messages

## Goal

Fix the three medium findings in
`eng/reviews/2026-09-23-006-abstraction-opportunities-review.md` without
changing observable behavior:

1. Model request code looks up an already validated model by ID again and
   returns "unknown model" errors that cannot occur. Its callers also pass the
   same model, effort level, and system prompt to every function.
2. The session store writes transcript entry kinds by hand in four append
   methods, and the prompt run turns its settings change into transcript
   entries in three places.
3. The model's view of a skill invocation, its text followed by its images, is
   rebuilt separately in `openrouter.rs` and `compaction.rs`. The check for a
   turn start, and the search for the latest one, are also repeated.

When done, validation produces one `ModelRequestParameters` value and every
estimate, request body, compaction, and usage update takes it and cannot fail
for an unknown model. Transcript entries are encoded in one place, and the
store appends a slice of them. A skill invocation becomes a user message
through one function.

## Related code

- `src/acp.rs:51` — `validate_settings`, which becomes
  `ModelRequestParameters::new`. It is called by `compact_session` (`:253`),
  `set_config_option` (`:320`), and `load_session` (`:396`).
- `src/acp/prompt.rs:130` — `PromptRun` fields `settings`, `settings_change`,
  and `system_prompt`, and their uses in `open` (`:191`), `save_turn_start`
  (`:240`), `hook_context` (`:448`), `save_feedback` (`:637`),
  `request_completion` (`:672`), `compact` (`:752`), `send_usage` (`:777`), and
  `approve` (`:826`).
- `src/openrouter.rs:253` — `ordinary_body`, `summarizer_body` (`:277`),
  `Client::stream_completion` (`:332`), `Client::summarize` (`:344`),
  `skill_invocation_text` (`:415`), and `chat_messages` (`:456`).
- `src/compaction.rs` — `budget`, `request_estimate`, `projected_estimate`,
  `input_fits`, `ranked_cuts`, `summary_request_fits`, `material`,
  `repeated_invocation`, and `compact`.
- `src/acp/convert.rs:115` — `usage_update`.
- `src/sessions.rs:376` — `SessionSettingsChange`. The four append methods are
  at `:868`–`:970`, `read` is at `:813`, and `decode_entry` is at `:1061`.
  Transcript validation checks for a turn start at `:603` and `:636`.

## Decisions

- **`ModelRequestParameters` lives in `openrouter.rs`.** Its fields are public:
  `model: &'static CatalogModel`, `effort: EffortLevel`, and
  `system_prompt: String`. `ModelRequestParameters::new(model_id, effort, system_prompt) -> io::Result<Self>`
  is the only validation. It returns the two `InvalidData` messages that
  `validate_settings` returns today, and ACP callers map the error with
  `Error::into_internal_error` as they do now. The catalog is installed once per
  process, so a `&'static CatalogModel` from it stays valid.
- **`PromptRun` stores `parameters` and `mode` instead of `settings` and
  `system_prompt`.** This keeps the model and effort level in one place. Hook
  context reads `parameters.model.id`, `parameters.effort`, and `mode`. The one
  call to `config_options` builds a `SessionSettings` from them.
- **`compact_session` validates the saved settings, then applies the selected
  effort.** It builds `ModelRequestParameters` from the saved settings and then
  sets `parameters.effort` to the ACP selection, as it does today.
- **The size check for summarizer requests takes `&CatalogModel`.**
  `summarizer_body`, `Client::summarize`, `summary_request_fits`, and `budget`
  take the catalog model rather than the full `ModelRequestParameters`, because
  they use neither the effort level nor the system prompt.
- **The store appends transcript entries, keeping one typed method per boundary.**
  `append_turn_start(id, entries: &[TranscriptEntry]) -> io::Result<SessionSummary>`
  takes the settings entries followed by the turn start. It derives the session
  title from the last entry and panics if that entry is not a turn start.
  `append_batch(id, &AssistantBatch)` and `append_hook_feedback(id, &HookFeedback)`
  keep their signatures and build their entries internally. The small copies
  this adds are negligible next to a model request. All three call one private
  `append(id, session_title, entries) -> io::Result<SessionSummary>`, which runs
  the shared transaction: update activity and adopt the session title, insert
  each entry, read the session summary, and commit. That adds one
  primary-key read to each batch and feedback append.
- **`SessionSettingsChange` is removed.** `PromptRun::open` builds
  `settings_entries: Vec<TranscriptEntry>`: a model entry for an empty
  transcript, then effort and mode entries when they differ from the saved
  settings. `save_turn_start` appends the turn start, checks input admission on
  the transcript plus these entries, saves them, and extends the transcript with
  them. It locks the model when the first entry is a model entry.
- **The skill invocation message stays in `openrouter.rs`.** The invocation text
  is model-facing wording, so
  `openrouter::skill_invocation_message(&SkillInvocation) -> UserMessage`
  replaces `skill_invocation_text`. Its parts are that text followed by the
  image attachments. `user_content` encodes the result exactly as the current
  invocation branch does, and `material` gets the same fields as today.

## Naming

- `ModelRequestParameters` — The validated catalog model, effort level, and
  system prompt that every ordinary model request in a turn sends with the
  transcript. Used as the type name and as `parameters` in fields, arguments,
  and local variables.
- `turn start` — A user message or skill invocation, as in
  `eng/architecture.md`. Used in `TranscriptEntry::is_turn_start` and
  `sessions::latest_turn_start`.
- `settings entries` — The model, effort, and mode entries a turn saves
  immediately before its turn start. Used in `PromptRun::settings_entries`.

## Test plan

This change keeps behavior the same, so it adds no test functions. The existing
tests own the guarantees:

- Request encoding, efforts, and the skill invocation image message:
  `requests_group_messages_and_send_continuation_metadata_once` and
  `requests_send_each_effort_of_each_model`.
- Validation errors: `loading_a_session_with_a_model_outside_the_catalog_fails_before_replay`
  and `invalid_prompt_startup_sends_no_request_or_user_message`.
- Settings entries and the model lock:
  `absent_selection_uses_saved_settings_without_duplicate_entries`,
  `different_efforts_are_saved_and_sent_for_sequential_turns`, and
  `configuration_selections_validate_and_restore_saved_values`.
- Input admission: `oversized_input_is_rejected_before_save_and_a_valid_prompt_can_follow`.
- Encoding, decoding, session title, and activity:
  `a_saved_batch_survives_database_reopen_in_order` and
  `turn_start_append_adopts_a_session_title_once_and_updates_activity`.
- Compaction and repeated invocations:
  `manual_compaction_uses_a_summary_and_keeps_the_complete_transcript` and
  `automatic_compaction_precedes_the_next_model_request`.
- Hook context fields: `lifecycle_hooks_run_at_their_points_and_their_feedback_reaches_the_model`.

In the image part of `manual_compaction_uses_a_summary_and_keeps_the_complete_transcript`,
add a skill invocation with an image attachment to `image_transcript`. The
`material` assertions then cover the invocation branch that now goes through
`skill_invocation_message`.

Update test call sites to the new signatures. Seeded transcripts pass
`&[TranscriptEntry::Model(…), TranscriptEntry::UserMessage(…)]` to
`append_turn_start` instead of a `SessionSettingsChange`. The openrouter and
compaction tests build `ModelRequestParameters::new(default_model(), EffortLevel::Default, …)`.
Report the change in test functions and test lines. The count should stay the
same and the number of lines should drop.

## Implementation plan

1. In `src/openrouter.rs`, add `ModelRequestParameters` and its `new`. Change
   `ordinary_body(&ModelRequestParameters, &[TranscriptEntry]) -> Value` and
   `stream_completion(&ModelRequestParameters, &[TranscriptEntry])`. Change
   `summarizer_body` and `summarize` to take `&CatalogModel`, and make
   `summarizer_body` return `Value`. Remove both unknown-model errors.
2. In `src/compaction.rs`, make `budget(&CatalogModel) -> Budget`. Make
   `request_estimate`, `projected_estimate`, `input_fits`, and `ranked_cuts`
   take `&ModelRequestParameters` and stop returning `io::Result`. Make
   `summary_request_fits(&CatalogModel, …) -> bool`. Change
   `compact(store, client, cancellation, id, &ModelRequestParameters, transcript)`.
3. In `src/acp/convert.rs`, change
   `usage_update(&[TranscriptEntry], &ModelRequestParameters) -> Option<SessionUpdate>`
   and remove its model lookup.
4. In `src/acp.rs`, remove `validate_settings`. `compact_session` and
   `load_session` build `ModelRequestParameters`, and `load_session` clones the
   system prompt for it. `set_config_option` validates saved settings through
   `ModelRequestParameters::new` and discards the result. Remove the `map_err`
   calls on `usage_update`.
5. In `src/acp/prompt.rs`, replace the `settings`, `settings_change`, and
   `system_prompt` fields with `parameters`, `mode`, and `settings_entries`, and
   update `open`, `hook_context`, `approve`, and the `config_options` call. Use
   `parameters.model.accepts_images` for the image check, and remove the
   `map_err` calls that were only for unknown models.
6. In `src/sessions.rs`, add `encode_entry(&TranscriptEntry) -> (&'static str, String)`
   next to `decode_entry`, and make `insert_entry` take `&TranscriptEntry`. Add
   the private `append`, and rewrite `append_turn_start`, `append_batch`,
   `append_hook_feedback`, and `append_checkpoint` on top of it. Add
   `read_transcript(tx, id) -> io::Result<Vec<TranscriptEntry>>` for `read` and
   `append_checkpoint`. Remove `SessionSettingsChange`.
7. In `src/acp/prompt.rs`, rewrite `save_turn_start` around `settings_entries`
   as described in Decisions.
8. In `src/sessions.rs`, add `TranscriptEntry::is_turn_start` and
   `latest_turn_start(&[TranscriptEntry]) -> Option<(usize, &TranscriptEntry)>`.
   Use them in `settings_block_end`, `check_hook_feedback_placement`,
   `compaction::repeated_invocation`, and `PromptRun::hook_invocation`.
9. In `src/openrouter.rs`, replace `skill_invocation_text` with
   `skill_invocation_message`. Use `user_content(&skill_invocation_message(invocation))`
   in `chat_messages`, and in `material` handle an invocation's message parts
   the same way as a user message's parts.
10. Update tests as described in the Test plan and run full validation.

## Documentation updates

- `eng/glossary.md` — Add **Model request parameters** with the definition in
  Naming.
- `AGENTS.md` — In the `src/openrouter.rs` entry, list model request parameters
  and the skill invocation message. In the `src/sessions.rs` entry, list
  transcript entry encoding.
