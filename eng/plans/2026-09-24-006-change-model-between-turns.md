# Change the model between turns

## Goal

A session's model is fixed when its first turn starts. After that, the ACP
`model` option lists only that model, and selecting another is an error. Users
want to switch models partway through a session, for example to a stronger model
for a hard step or a cheaper one for routine work.

When this is done, the model is chosen the same way as the effort level and
session mode. The ACP `model` option always lists the whole model catalog. A
change applies to the next turn, and each turn start saves the model it used.
Loading a session restores the model of its latest turn start.

Use [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), [testing guidance](../testing.md), and
[`AGENTS.md`](../../AGENTS.md) as the implementation constraints.

## Related code

- `src/sessions.rs` — `TranscriptEntry::Model`, the model entry that opens a
  nonempty transcript. `TurnStart` saves the effort level and session mode.
  `validate_transcript` requires the model entry first, then a turn start, and
  no model entry after that. `saved_settings` combines the model entry with the
  latest turn start. `append_turn_start` takes a slice so the first turn can
  save the model entry with it. The row kind `model` is encoded and decoded.
- `src/acp/prompt.rs` — `PromptRun::open` replaces the selected model with the
  saved one once the transcript is not empty. `save_turn_start` rejects a turn
  input with images when the model does not accept images, prepends the model
  entry to the first turn, and then sends a `ConfigOptionUpdate` that narrows
  the model option to one choice.
- `src/acp.rs` — `config_options(settings, model_locked)`. `set_config_option`
  reads the stored session only to lock the model and check the saved model.
  `load_session` passes `model_locked`. `compact_session` takes the model from
  the saved settings and the effort level from the ACP selections.
- `src/compaction.rs` — `projection` builds the chat messages of the next model
  request, and `material` skips the model entry.
- `src/openrouter.rs` — `chat_messages` skips the model entry and sends
  continuation metadata unchanged in each assistant message.
- `src/acp/convert.rs` — `replay_transcript` skips the model entry.

## Decisions

- The model moves into the turn start, which then holds the model, effort
  level, and session mode captured for its turn. The model entry goes away. A
  nonempty transcript opens with a turn start. This keeps all three settings
  in one place, with one rule for how they are saved and restored.
- A prompt run uses its settings snapshot for every model request in the turn,
  including automatic compaction. `PromptRun::open` no longer reads the model
  from the transcript.
- `/compact` uses the model and effort level of the ACP selections. It prepares
  the context for the next turn, which uses the selected model, so it summarizes
  and checks its budget against that model's context limit.
- Continuation metadata is sent unchanged, whichever model produced it. In this
  session, requests to Anthropic, OpenAI, Google, and DeepSeek models succeeded
  when they included continuation metadata from each of the others, including
  Anthropic signatures and OpenAI encrypted reasoning.
- OpenRouter rejects a request that contains an image when it is sent to a model
  that does not accept images ("No endpoints found that support image input";
  confirmed in this session). So `save_turn_start` rejects the prompt before
  saving anything when the selected model does not accept images and the
  projection of the prospective transcript contains an image. This replaces
  the current check on the turn input alone, which the new check covers. Once a
  checkpoint covers the earlier images, the projection has none and the prompt
  is accepted. The error is `the selected model does not accept images; choose
  a model that accepts images`.
- Switching to a model with a smaller context limit needs no new code. Input
  admission and automatic compaction already measure against the settings
  snapshot's model.
- `set_config_option` no longer reads the session store. Load already rejects a
  latest turn start whose model or effort level is outside the model catalog,
  and every later selection comes from the catalog.
- Changing the model sends no usage update. The next usage update reports the
  new model's context limit.
- A turn start's model string is not checked against the model catalog when
  the transcript is read, just as the model entry is not checked today. Only the
  latest turn start's model must be in the model catalog, and load checks that.

## Naming

- **Turn start** — Change the glossary definition to: a transcript entry holding
  a turn input with the model, effort level, and session mode captured for that
  turn. It is `TurnStart` in code, with the new field `model`.
- **Saved settings** — Change the glossary definition to: the model, effort
  level, and session mode of the latest turn start. They are the durable
  authority for the session settings after load. An empty transcript has no
  saved settings.
- **Model entry** — Removed from the glossary, code, and comments.
- `compaction::has_images` — Whether the projection of the next model request
  contains an image. It appears only in `src/compaction.rs` and
  `save_turn_start`.

## Test plan

- `src/sessions.rs`: rename `a_transcript_opens_with_its_model_and_first_turn_start`
  to `a_nonempty_transcript_opens_with_a_turn_start`. A transcript that opens
  with an assistant batch is an error, and a row of the removed kind `model` is
  an error.
- `src/sessions.rs`: rename
  `saved_settings_come_from_the_model_entry_and_latest_turn_start` to
  `saved_settings_come_from_the_latest_turn_start`. Two turn starts with
  different models, effort levels, and session modes restore the second one's
  settings.
- `src/acp.rs`: in `configuration_selections_validate_and_restore_saved_values`,
  after a turn start is saved, selecting another model succeeds and the option
  still lists the whole model catalog. Loading lists the whole model catalog
  with the latest turn start's model selected.
- `src/acp.rs`: in
  `setting_changes_apply_to_the_next_turn_while_the_system_prompt_stays_captured`,
  change the model as well while the first turn runs. The second request uses
  the new model, and the second turn start saves it.
- `src/acp.rs`: `manual_compact_command_uses_the_active_prompt_without_saving_a_message`
  selects a model that differs from the one in the saved turn start, and the
  summarizer request uses the selected model.
- `src/acp.rs`: `loading_a_session_with_a_model_outside_the_catalog_fails_before_replay`
  saves an earlier turn start with a model outside the catalog and a latest one
  with a catalog model, and that load succeeds, then keeps its current failing
  case for a latest turn start outside the catalog.
- `src/acp/prompt.rs`: `a_text_answer_is_saved_in_the_transcript` no longer
  expects a `ConfigOptionUpdate`; the first updates are the session info and the
  answer text.
- `src/acp/prompt.rs`: in `invalid_prompt_startup_sends_no_request_or_user_message`,
  after the image-capable session finishes, a text prompt in that session with
  a model that does not accept images is rejected with the new error, saves
  nothing, and sends no request.
- `src/compaction.rs`: add `images_leave_the_projection_once_a_checkpoint_covers_them`.
  `has_images` is true for a transcript with an image in a turn start and false
  after a checkpoint covers it, and true when the covered turn start is a skill
  invocation with an image repeated after the summary.
- Every other test that builds a transcript drops the model entry and gives each
  turn start the test catalog's default model, through the existing
  `TranscriptEntry::turn` helper and `turn_with` in `src/acp/prompt.rs`.

## Implementation plan

1. `src/sessions.rs`: add `model: String` to `TurnStart` and remove
   `TranscriptEntry::Model` with its row kind. `validate_transcript` requires a
   nonempty transcript to open with a turn start. `saved_settings` reads all
   three settings from the latest turn start. `append_turn_start` takes one
   `&TurnStart`. `TranscriptEntry::turn` uses
   `openrouter::fixture::DEFAULT_MODEL`. Update the doc comments on
   `TranscriptEntry`, `TurnStart`, and `saved_settings`.
2. `src/openrouter.rs`, `src/compaction.rs`, and `src/acp/convert.rs`: remove
   the model-entry match arms. Add `compaction::has_images`, which looks for an
   `image_url` part in `projection`.
3. `src/acp/prompt.rs`: `PromptRun::open` uses `input.selected_settings` as
   given. `save_turn_start` saves the model in the turn start, drops the
   model entry and the `ConfigOptionUpdate`, and checks images with
   `compaction::has_images` on the prospective transcript.
4. `src/acp.rs`: remove `model_locked` from `config_options`, `load_session`,
   and `set_config_option`, along with the store read and saved-settings check
   in `set_config_option`. `compact_session` builds its model request
   parameters from `active.selections`.
5. Update the tests above, then recreate the local session database.

## Documentation updates

- `eng/architecture.md`:
  - Transcript: a nonempty transcript opens with a turn start. Every turn start
    stores its model, effort level, and session mode. Replace the model-entry
    paragraph with one saying all three may change between turns, and that load
    restores them from the latest turn start.
  - Settings: replace "A session with turns keeps the model in its model entry"
    with the rule that a loaded session starts from its saved settings. A
    prompt is rejected before its turn start is saved when the selected model
    does not accept images and the next model request would contain one. A
    latest turn start whose model or effort level is outside the model catalog
    fails load.
  - Process state: the transcript remains authoritative for the last saved
    model, effort level, and session mode.
  - System prompt: compaction uses the prompt run's model, or for `/compact`
    the model of the ACP selections.
  - Invariants 3 and 4: a nonempty transcript begins with a turn start, and
    every turn start stores the model, effort level, and session mode captured
    for its turn, which every model request in that turn uses.
- `eng/glossary.md`: Session drops "Its OpenRouter model is fixed when the
  first turn starts." Turn start and Saved settings as in Naming. Transcript
  entry drops the model entry, and the Model entry term goes.
- `AGENTS.md`: the `src/sessions.rs` entry describes turn starts that save each
  turn's input with its model, effort level, and mode, and saved settings from
  the latest turn start.
